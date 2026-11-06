package core

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type archiveTransfer struct {
	mu                       sync.Mutex
	id, kind, path, filename string
	file                     *os.File
	size, offset             int64
	touched                  time.Time
	closed                   bool
	result                   *Project
}
type archiveTransferInfo struct {
	ID         string `json:"id"`
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
	ChunkBytes int    `json:"chunk_bytes"`
}

// unlock renews inactivity from the end of an operation, including a long import or native copy.
func (transfer *archiveTransfer) unlock() {
	transfer.touched = time.Now()
	transfer.mu.Unlock()
}

// createArchiveTransfer reserves a bounded, engine-owned temporary file and clears abandoned client transfers.
func (engine *Engine) createArchiveTransfer(kind string, size int64) (*archiveTransfer, error) {
	engine.transfersMu.Lock()
	defer engine.transfersMu.Unlock()
	if engine.transfers == nil {
		engine.transfers = map[string]*archiveTransfer{}
	}
	for id, transfer := range engine.transfers {
		if !transfer.mu.TryLock() {
			continue
		}
		if time.Since(transfer.touched) > 30*time.Minute {
			delete(engine.transfers, id)
			transfer.closed = true
			transfer.file.Close()
			os.Remove(transfer.path)
		}
		transfer.mu.Unlock()
	}
	if len(engine.transfers) >= 4 {
		return nil, errors.New("Four backup transfers are already active. Finish or cancel one first")
	}
	file, err := os.CreateTemp(engine.owner.dir, "archive-*")
	if err != nil {
		return nil, err
	}
	transfer := &archiveTransfer{id: newID(), kind: kind, path: file.Name(), file: file, size: size, touched: time.Now()}
	engine.transfers[transfer.id] = transfer
	return transfer, nil
}

// lockArchiveTransfer obtains a live transfer without exposing arbitrary file paths to the client.
func (engine *Engine) lockArchiveTransfer(id, kind string) (*archiveTransfer, error) {
	engine.transfersMu.Lock()
	transfer := engine.transfers[id]
	engine.transfersMu.Unlock()
	if transfer == nil {
		return nil, errors.New("Backup transfer not found")
	}
	transfer.mu.Lock()
	if transfer.closed || transfer.kind != kind {
		transfer.mu.Unlock()
		return nil, errors.New("Backup transfer is closed or has a different operation")
	}
	transfer.touched = time.Now()
	return transfer, nil
}

// finishArchiveTransfer deletes only a file reserved by this engine and is safe to repeat after a lost acknowledgement.
func (engine *Engine) finishArchiveTransfer(id string) error {
	engine.transfersMu.Lock()
	transfer := engine.transfers[id]
	delete(engine.transfers, id)
	engine.transfersMu.Unlock()
	if transfer == nil {
		return nil
	}
	transfer.mu.Lock()
	defer transfer.mu.Unlock()
	if transfer.closed {
		return nil
	}
	transfer.closed = true
	return errors.Join(transfer.file.Close(), os.Remove(transfer.path))
}

// cleanupArchiveTransfers removes all client-transfer files after registered operations have stopped.
func (engine *Engine) cleanupArchiveTransfers() error {
	engine.transfersMu.Lock()
	ids := []string{}
	for id := range engine.transfers {
		ids = append(ids, id)
	}
	engine.transfersMu.Unlock()
	var result error
	for _, id := range ids {
		result = errors.Join(result, engine.finishArchiveTransfer(id))
	}
	return result
}

// beginArchiveExport streams one committed snapshot into a temporary file and exposes only chunk metadata.
func (engine *Engine) beginArchiveExport(projectID string) (archiveTransferInfo, error) {
	transfer, err := engine.createArchiveTransfer("export", 0)
	if err != nil {
		return archiveTransferInfo{}, err
	}
	transfer.mu.Lock()
	title, err := engine.store.exportArchiveTo(engine.ctx, projectID, transfer.file, archiveMaxBytes)
	if err == nil {
		var info os.FileInfo
		info, err = transfer.file.Stat()
		if err == nil {
			transfer.size = info.Size()
			transfer.filename = title + ".quillweave.json"
		}
	}
	info := archiveTransferInfo{ID: transfer.id, Filename: transfer.filename, Size: transfer.size, ChunkBytes: archiveChunkBytes}
	transfer.unlock()
	if err != nil {
		engine.finishArchiveTransfer(transfer.id)
		return archiveTransferInfo{}, err
	}
	return info, nil
}

// archiveOffset rejects fractional, negative and unsupported offsets before touching a transfer file.
func archiveOffset(args *arguments, key string) (int64, error) {
	value, ok := unsigned(args.fields[key])
	if !ok || value > uint64(archiveMaxBytes) {
		return 0, errors.New("Invalid backup size or offset")
	}
	return int64(value), nil
}

// uploadArchiveChunk accepts exact retries but rejects gaps, overlaps and writes beyond the declared file.
func (engine *Engine) uploadArchiveChunk(args *arguments) (any, error) {
	id := args.text("transfer_id")
	encoded := args.text("data")
	offset, err := archiveOffset(args, "offset")
	if args.err != nil {
		return nil, args.err
	}
	if err != nil {
		return nil, err
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(archiveChunkBytes) {
		return nil, errors.New("Backup chunk exceeds the transfer frame limit")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return nil, errors.New("Invalid backup chunk")
	}
	transfer, err := engine.lockArchiveTransfer(id, "import")
	if err != nil {
		return nil, err
	}
	defer transfer.unlock()
	if transfer.result != nil {
		return nil, errors.New("This backup has already been imported")
	}
	if offset < transfer.offset && offset+int64(len(data)) <= transfer.offset {
		previous := make([]byte, len(data))
		if _, err := transfer.file.ReadAt(previous, offset); err != nil {
			return nil, err
		}
		if !bytes.Equal(previous, data) {
			return nil, errors.New("A retried backup chunk differs from the original")
		}
	} else {
		if offset != transfer.offset || offset+int64(len(data)) > transfer.size {
			return nil, errors.New("Backup chunks must be uploaded in order within the declared size")
		}
		written, err := transfer.file.WriteAt(data, offset)
		if err != nil {
			return nil, err
		}
		if written != len(data) {
			return nil, io.ErrShortWrite
		}
		transfer.offset += int64(written)
	}
	return map[string]any{"offset": transfer.offset}, nil
}

// downloadArchiveChunk returns one bounded, repeatable slice of a completed backup snapshot.
func (engine *Engine) downloadArchiveChunk(args *arguments) (any, error) {
	id := args.text("transfer_id")
	offset, err := archiveOffset(args, "offset")
	if args.err != nil {
		return nil, args.err
	}
	if err != nil {
		return nil, err
	}
	transfer, err := engine.lockArchiveTransfer(id, "export")
	if err != nil {
		return nil, err
	}
	defer transfer.unlock()
	if offset > transfer.size {
		return nil, errors.New("Backup offset exceeds the file size")
	}
	data := make([]byte, min(int64(archiveChunkBytes), transfer.size-offset))
	count, err := transfer.file.ReadAt(data, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if count != len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	return map[string]any{"data": base64.StdEncoding.EncodeToString(data), "offset": offset + int64(count), "done": offset+int64(count) == transfer.size}, nil
}

// completeArchiveImport publishes all decoded records atomically and keeps the result for acknowledgement retries.
func (engine *Engine) completeArchiveImport(id string) (Project, error) {
	transfer, err := engine.lockArchiveTransfer(id, "import")
	if err != nil {
		return Project{}, err
	}
	defer transfer.unlock()
	if transfer.result != nil {
		return *transfer.result, nil
	}
	if transfer.offset != transfer.size {
		return Project{}, errors.New("The backup upload is incomplete")
	}
	if _, err := transfer.file.Seek(0, io.SeekStart); err != nil {
		return Project{}, err
	}
	project, err := engine.importArchiveFrom(transfer.file)
	if err == nil {
		transfer.result = &project
	}
	return project, err
}

// archiveCommand registers backup work for shutdown and shares the same bounded protocol over IPC and HTTP.
func (engine *Engine) archiveCommand(command string, args *arguments) (any, error) {
	engine.mu.Lock()
	if engine.closed {
		engine.mu.Unlock()
		return nil, errors.New("The application is closing")
	}
	engine.workers.Add(1)
	engine.mu.Unlock()
	defer engine.workers.Done()
	switch command {
	case "begin_archive_export":
		id := args.text("project_id")
		if args.err != nil {
			return nil, args.err
		}
		return engine.beginArchiveExport(id)
	case "read_archive_chunk":
		return engine.downloadArchiveChunk(args)
	case "begin_archive_import":
		size, err := archiveOffset(args, "size")
		if err != nil || size == 0 {
			return nil, errors.New("Select a nonempty backup within the supported file size")
		}
		transfer, err := engine.createArchiveTransfer("import", size)
		if err != nil {
			return nil, err
		}
		return archiveTransferInfo{ID: transfer.id, Size: size, ChunkBytes: archiveChunkBytes}, nil
	case "write_archive_chunk":
		return engine.uploadArchiveChunk(args)
	case "complete_archive_import":
		id := args.text("transfer_id")
		if args.err != nil {
			return nil, args.err
		}
		return engine.completeArchiveImport(id)
	case "finish_archive_transfer":
		id := args.text("transfer_id")
		if args.err != nil {
			return nil, args.err
		}
		return map[string]bool{"finished": true}, engine.finishArchiveTransfer(id)
	case "import_archive":
		content := args.text("content")
		if args.err != nil {
			return nil, args.err
		}
		if len(content) > legacyArchiveBytes {
			return nil, errors.New("Use the streaming backup transfer for archives larger than 40 MB")
		}
		return engine.importArchiveFrom(strings.NewReader(content))
	}
	return nil, errors.New("Unknown backup command")
}

// WriteArchiveTransfer streams a reserved export to a native save dialog's writer without a giant IPC response.
func (engine *Engine) WriteArchiveTransfer(id string, out io.Writer) error {
	engine.mu.Lock()
	if engine.closed {
		engine.mu.Unlock()
		return errors.New("The application is closing")
	}
	engine.workers.Add(1)
	engine.mu.Unlock()
	defer engine.workers.Done()
	transfer, err := engine.lockArchiveTransfer(id, "export")
	if err != nil {
		return err
	}
	defer transfer.unlock()
	bounded := &archiveWriter{writer: out, ctx: engine.ctx, limit: transfer.size}
	_, err = io.Copy(bounded, io.NewSectionReader(transfer.file, 0, transfer.size))
	return err
}
