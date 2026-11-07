//go:build quillweave_whitebox

package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// importTestArchiveFile exercises the actual bounded upload protocol and its acknowledgement retries.
func importTestArchiveFile(t *testing.T, engine *Engine, path string) Project {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	upload := invoke[archiveTransferInfo](t, engine, "begin_archive_import", map[string]any{"size": info.Size()})
	defer engine.finishArchiveTransfer(upload.ID)
	buffer := make([]byte, upload.ChunkBytes)
	offset := int64(0)
	for {
		count, err := file.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		args := map[string]any{"transfer_id": upload.ID, "offset": offset, "data": base64.StdEncoding.EncodeToString(buffer[:count])}
		result := invoke[struct {
			Offset int64 `json:"offset"`
		}](t, engine, "write_archive_chunk", args)
		if result.Offset != offset+int64(count) {
			t.Fatal("Upload offset diverged")
		}
		if offset == 0 {
			retry := invoke[struct {
				Offset int64 `json:"offset"`
			}](t, engine, "write_archive_chunk", args)
			if retry.Offset != result.Offset {
				t.Fatal("A lost chunk acknowledgement duplicated data")
			}
		}
		offset = result.Offset
	}
	project := invoke[Project](t, engine, "complete_archive_import", map[string]any{"transfer_id": upload.ID})
	retry := invoke[Project](t, engine, "complete_archive_import", map[string]any{"transfer_id": upload.ID})
	if retry.ID != project.ID {
		t.Fatal("A lost completion acknowledgement created a duplicate book")
	}
	return project
}

type pausedArchiveWriter struct {
	buffer        bytes.Buffer
	ready, resume chan struct{}
	once          sync.Once
}

// Write pauses a normal output consumer after the metadata SELECT has pinned the export snapshot.
func (writer *pausedArchiveWriter) Write(data []byte) (int, error) {
	count, err := writer.buffer.Write(data)
	if bytes.Contains(data, []byte(`"title":`)) {
		writer.once.Do(func() { close(writer.ready); <-writer.resume })
	}
	return count, err
}

// TestArchiveSnapshotWithMillionCharacterVersions keeps canon and runtime coherent across a concurrent commit.
func TestArchiveSnapshotWithMillionCharacterVersions(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Title = "Snapshot before"
	p.Chapters[0].Body = strings.Repeat("山", 1000000)
	p.Chapters[0].Versions = []Version{{ID: newID(), Title: "Earlier version", Body: strings.Repeat("海", 100000), Status: "draft", CreatedAt: now()}}
	insertProject(t, engine, p)
	job := Job{ID: newID(), ProjectID: p.ID, Status: "done", Text: "Runtime before", Prompt: Prompt{Messages: []Message{}}, Proposals: []StudioProposal{}}
	if err := engine.store.SaveRun(job.ID, p.ID, storedJob(&job)); err != nil {
		t.Fatal(err)
	}
	peer, err := OpenStore(engine.store.path)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	writer := &pausedArchiveWriter{ready: make(chan struct{}), resume: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(writer.resume) })
	done := make(chan error, 1)
	go func() {
		_, err := engine.store.exportArchiveTo(context.Background(), p.ID, writer, archiveMaxBytes)
		done <- err
	}()
	select {
	case <-writer.ready:
	case err := <-done:
		t.Fatalf("Export ended before its metadata snapshot: %v", err)
	}
	p.Title = "Snapshot after"
	p.Chapters[0].Body = strings.Repeat("林", 1000000)
	writeErr := peer.Save(&p, p.Revision)
	job.Text = "Runtime after"
	if writeErr == nil {
		writeErr = peer.SaveRun(job.ID, p.ID, storedJob(&job))
	}
	release.Do(func() { close(writer.resume) })
	exportErr := <-done
	if writeErr != nil || exportErr != nil {
		t.Fatalf("Concurrent export/write failed: %v / %v", exportErr, writeErr)
	}
	var archive struct {
		Project Project `json:"project"`
	}
	if err := json.Unmarshal(writer.buffer.Bytes(), &archive); err != nil {
		t.Fatal(err)
	}
	if archive.Project.Title != "Snapshot before" || archive.Project.Chapters[0].Body != strings.Repeat("山", 1000000) || len(archive.Project.ArchiveJobs) != 1 || archive.Project.ArchiveJobs[0].Text != "Runtime before" {
		t.Fatal("Export mixed different committed versions")
	}
	restored, err := engine.importArchiveFrom(bytes.NewReader(writer.buffer.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if restored.Chapters[0].Body != archive.Project.Chapters[0].Body || len(restored.Chapters[0].Versions) != 1 || restored.Chapters[0].Versions[0].Body != strings.Repeat("海", 100000) {
		t.Fatal("Million-character manuscript versions were not restored")
	}
}

// TestArchiveShutdownRemovesTransfers releases active transfer files even if the client never finishes.
func TestArchiveShutdownRemovesTransfers(t *testing.T) {
	engine := testEngine(t)
	transfer := invoke[archiveTransferInfo](t, engine, "begin_archive_import", map[string]any{"size": 10})
	path := engine.transfers[transfer.ID].path
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Shutdown retained an unfinished transfer")
	}
}

// TestLargeArchiveTransferRoundtrip restores the exact workload that previously exported an unusable backup.
func TestLargeArchiveTransferRoundtrip(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Chapters[0].Body = strings.Repeat("山", 50000)
	insertProject(t, engine, p)
	response := jsonText(map[string]any{"text": "A brief suggestion", "proposals": []StudioProposal{{Kind: "decision", Title: "守塔", Content: "守住北塔"}}})
	var calls atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, streamPayload(response)) })
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	config.ContextChars = 100000
	configureModel(t, engine, config)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID})
	for i := 0; i < 280; i++ {
		job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "session_id": session.ID, "action": "continue", "instruction": fmt.Sprintf("Review choice %d", i), "request_id": fmt.Sprintf("archive-%d", i)}))
		if job.Status != "done" {
			t.Fatal(job.Error)
		}
	}
	experiment := PolicyExperiment{Policies: []ExplorationPolicy{{ID: "base", Order: "breadth", MaxCalls: 1, MaxDepth: 1}, {ID: "candidate", Order: "depth", MaxCalls: 1, MaxDepth: 1}}, Cases: []DiscoveryCase{}}
	invoke[map[string]any](t, engine, "record_experiment", map[string]any{"project_id": p.ID, "experiment": experiment})
	p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": StoryMemory{Kind: "rule", Title: "代价", Content: "魔法需要代价", Status: "confirmed", SessionID: session.ID}})
	transfer := invoke[archiveTransferInfo](t, engine, "begin_archive_export", map[string]any{"project_id": p.ID})
	defer engine.finishArchiveTransfer(transfer.ID)
	if transfer.Size <= 40000000 {
		t.Fatalf("The regression workload no longer reaches the old limit: %d", transfer.Size)
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "large.quillweave.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.WriteArchiveTransfer(transfer.ID, file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	first := invoke[struct {
		Data   string `json:"data"`
		Offset int64  `json:"offset"`
	}](t, engine, "read_archive_chunk", map[string]any{"transfer_id": transfer.ID, "offset": 0})
	if first.Offset != int64(archiveChunkBytes) {
		t.Fatal("The public download frame is not bounded")
	}
	if _, err := engine.Dispatch("export", rawMessage(map[string]any{"project_id": p.ID, "format": "archive"})); err == nil {
		t.Fatal("Legacy export acknowledged a file beyond its transport limit")
	}
	restored := importTestArchiveFile(t, engine, file.Name())
	if restored.ID == p.ID || restored.Chapters[0].Body != p.Chapters[0].Body {
		t.Fatal("Large backup changed the manuscript")
	}
	var jobs, experiments int
	engine.store.db.QueryRow("SELECT COUNT(*) FROM runs WHERE project_id=?", restored.ID).Scan(&jobs)
	engine.store.db.QueryRow("SELECT COUNT(*) FROM experiments WHERE project_id=?", restored.ID).Scan(&experiments)
	sessions := invoke[[]Conversation](t, engine, "list_conversations", map[string]any{"project_id": restored.ID, "chapter_id": ""})
	if jobs != 280 || experiments != 1 || len(sessions) != 1 || len(sessions[0].Turns) != 560 || sessions[0].ID == session.ID || sessions[0].OriginChapterID != p.Chapters[0].ID || restored.Studio.Memories[0].SessionID != sessions[0].ID {
		t.Fatal("A runtime collection or rekeyed provenance link was lost")
	}
	rows := invoke[[]Job](t, engine, "get_runs", map[string]any{"project_id": restored.ID})
	if len(rows) == 0 || len(rows[0].Proposals) != 1 {
		t.Fatal("Pending author proposals were lost")
	}
	acceptProposalAfterPreview(t, engine, map[string]any{"project_id": restored.ID, "revision": restored.Revision, "job_id": rows[0].ID, "proposal_id": rows[0].Proposals[0].ID, "decision": "accepted"})
	original := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	if len(original.Studio.Decisions) != 0 {
		t.Fatal("Reviewing the restored book changed the original")
	}
	if calls.Load() != 280 {
		t.Fatal("Backup or restoration unexpectedly called a model")
	}
	t.Logf("Restored %d-byte v1 JSON with 280 tasks, 560 turns, memory provenance, pending proposals and an experiment through bounded frames", transfer.Size)
}

// TestArchiveTransferGuardsAndCleanup checks negative frames and removes every owned temporary file.
func TestArchiveTransferGuardsAndCleanup(t *testing.T) {
	engine := testEngine(t)
	reject(t, engine, "begin_archive_import", map[string]any{"size": 0})
	reject(t, engine, "begin_archive_import", map[string]any{"size": archiveMaxBytes + 1})
	transfer := invoke[archiveTransferInfo](t, engine, "begin_archive_import", map[string]any{"size": 5})
	reject(t, engine, "complete_archive_import", map[string]any{"transfer_id": transfer.ID})
	reject(t, engine, "write_archive_chunk", map[string]any{"transfer_id": transfer.ID, "offset": 1, "data": base64.StdEncoding.EncodeToString([]byte("x"))})
	args := map[string]any{"transfer_id": transfer.ID, "offset": 0, "data": base64.StdEncoding.EncodeToString([]byte("abc"))}
	invoke[map[string]any](t, engine, "write_archive_chunk", args)
	args["data"] = base64.StdEncoding.EncodeToString([]byte("def"))
	reject(t, engine, "write_archive_chunk", args)
	args["offset"] = 3
	args["data"] = base64.StdEncoding.EncodeToString([]byte("xyz"))
	reject(t, engine, "write_archive_chunk", args)
	invoke[map[string]any](t, engine, "finish_archive_transfer", map[string]any{"transfer_id": transfer.ID})
	invoke[map[string]any](t, engine, "finish_archive_transfer", map[string]any{"transfer_id": transfer.ID})
	files, err := os.ReadDir(engine.owner.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != "owner.lock" {
		t.Fatal("Cancelled transfer artifacts were retained")
	}
}

// TestArchiveImportRejectsMalformedDataAtomically preserves strict historical validation under streaming parsing.
func TestArchiveImportRejectsMalformedDataAtomically(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	exported := invoke[map[string]string](t, engine, "export", map[string]any{"project_id": p.ID, "format": "archive"})["content"]
	malformed := []string{exported + "{}", strings.Replace(exported, `"sources":[]`, `"sources":null`, 1), strings.Replace(exported, `"format":"quillweave-2"`, `"format":"unknown"`, 1), strings.Replace(exported, `"project":{`, `"project":{"id":"duplicate",`, 1)}
	for _, content := range malformed {
		if content == exported {
			t.Fatal("Malformed fixture did not change its target")
		}
		reject(t, engine, "import_archive", map[string]any{"content": content})
	}
	before, err := engine.store.List()
	if err != nil || len(before) != 1 {
		t.Fatal("A failed import published partial project data")
	}
	broken := append([]byte(exported), 0xff)
	if _, err := engine.importArchiveFrom(bytes.NewReader(broken)); err == nil {
		t.Fatal("Streaming import accepted invalid UTF-8")
	}
	files, err := os.ReadDir(engine.owner.dir)
	if err != nil || len(files) != 1 {
		t.Fatal("Failed import spools were retained")
	}
}
