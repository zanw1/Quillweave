package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"sort"
)

const archiveMaxBytes int64 = 4 << 30
const archiveRecordBytes int64 = 256 << 20
const archiveChunkBytes = 512 << 10
const legacyArchiveBytes = 40_000_000

type archiveWriter struct {
	writer  io.Writer
	ctx     context.Context
	written int64
	limit   int64
}

// Write bounds the complete archive without buffering its runtime history in memory.
func (writer *archiveWriter) Write(data []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(data)) > writer.limit-writer.written {
		return 0, errors.New("The archive exceeds the supported file size")
	}
	count, err := writer.writer.Write(data)
	writer.written += int64(count)
	return count, err
}

type archiveObject struct {
	writer io.Writer
	fields int
}

// field writes one named value with valid JSON separators and forwards every I/O error.
func (object *archiveObject) field(name string, write func(io.Writer) error) error {
	prefix := ""
	if object.fields > 0 {
		prefix = ","
	}
	object.fields++
	if _, err := io.WriteString(object.writer, prefix+jsonText(name)+":"); err != nil {
		return err
	}
	return write(object.writer)
}

// scalarFields copies metadata while collection fields are emitted from normalized records.
func (object *archiveObject) scalarFields(fields map[string]json.RawMessage, excluded ...string) error {
	keys := []string{}
	for key := range fields {
		if !contains(excluded, key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := fields[key]
		if err := object.field(key, func(out io.Writer) error { return writeArchiveRecord(out, string(value)) }); err != nil {
			return err
		}
	}
	return nil
}

// writeArchiveRecord enforces a restorable per-value bound before a successful export is acknowledged.
func writeArchiveRecord(out io.Writer, raw string) error {
	if int64(len(raw)) > archiveRecordBytes {
		return errors.New("One archive record exceeds the supported size")
	}
	_, err := io.WriteString(out, raw)
	return err
}

// streamArchiveRows emits one normalized record at a time without collecting task prompts in a slice.
func streamArchiveRows(db documentDB, out io.Writer, statement string, args ...any) error {
	rows, err := db.QueryContext(context.Background(), statement, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if _, err := io.WriteString(out, "["); err != nil {
		return err
	}
	first := true
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		if !first {
			if _, err := io.WriteString(out, ","); err != nil {
				return err
			}
		}
		first = false
		if err := writeArchiveRecord(out, raw); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = io.WriteString(out, "]")
	return err
}

// streamArchiveConversations keeps conversation metadata and turns in the same database snapshot.
func streamArchiveConversations(db documentDB, out io.Writer, projectID string) error {
	rows, err := db.QueryContext(context.Background(), "SELECT id FROM conversations WHERE project_id=? ORDER BY rowid", projectID)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ids) > 20000 {
		return errors.New("Too many conversations for one archive")
	}
	if _, err := io.WriteString(out, "["); err != nil {
		return err
	}
	for i, id := range ids {
		var raw string
		var fields map[string]json.RawMessage
		if err := db.QueryRowContext(context.Background(), "SELECT document FROM conversations WHERE id=? AND project_id=?", id, projectID).Scan(&raw); err != nil {
			return err
		}
		if err := decodeJSON([]byte(raw), &fields); err != nil {
			return err
		}
		var count int
		if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM conversation_turns WHERE session_id=?", id).Scan(&count); err != nil {
			return err
		}
		if count > 20000 {
			return errors.New("Too many turns in one archived conversation")
		}
		if i > 0 {
			if _, err := io.WriteString(out, ","); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(out, "{"); err != nil {
			return err
		}
		object := archiveObject{writer: out}
		if err := object.scalarFields(fields, "turns"); err != nil {
			return err
		}
		if err := object.field("turns", func(out io.Writer) error {
			return streamArchiveRows(db, out, "SELECT document FROM conversation_turns WHERE session_id=? ORDER BY ordinal", id)
		}); err != nil {
			return err
		}
		if _, err := io.WriteString(out, "}"); err != nil {
			return err
		}
	}
	_, err = io.WriteString(out, "]")
	return err
}

// streamArchiveProject emits the portable v1 shape while preserving all supported collections.
func streamArchiveProject(db documentDB, out io.Writer, id string) (string, error) {
	var raw string
	var metadata map[string]json.RawMessage
	if err := db.QueryRowContext(context.Background(), "SELECT document FROM projects WHERE id=?", id).Scan(&raw); err != nil {
		return "", err
	}
	if err := decodeJSON([]byte(raw), &metadata); err != nil {
		return "", err
	}
	var title string
	if err := decodeJSON(metadata["title"], &title); err != nil {
		return "", err
	}
	for _, bound := range []struct {
		table string
		limit int
	}{{"runs", 20000}} {
		var count int
		if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+bound.table+" WHERE project_id=?", id).Scan(&count); err != nil {
			return "", err
		}
		if count > bound.limit {
			return "", errors.New("Too many runtime records in one archive")
		}
	}
	if _, err := io.WriteString(out, "{"); err != nil {
		return "", err
	}
	object := archiveObject{writer: out}
	if err := object.scalarFields(metadata, "chapters", "entities", "facts", "sources", "compilations", "studio", "conversations", "jobs", "experiments", "writing_sessions", "wiki_sessions", "import_warnings"); err != nil {
		return "", err
	}
	collections := []struct{ key, kind string }{{"chapters", "chapter"}, {"entities", "entity"}, {"facts", "fact"}, {"sources", "source"}, {"compilations", "compilation"}}
	for _, collection := range collections {
		kind := collection.kind
		if err := object.field(collection.key, func(out io.Writer) error {
			return streamArchiveRows(db, out, "SELECT document FROM project_parts WHERE project_id=? AND kind=? ORDER BY position,id", id, kind)
		}); err != nil {
			return "", err
		}
	}
	if err := object.field("studio", func(out io.Writer) error {
		fields := map[string]json.RawMessage{}
		if err := decodeJSON(metadata["studio"], &fields); err != nil {
			return err
		}
		if _, err := io.WriteString(out, "{"); err != nil {
			return err
		}
		studio := archiveObject{writer: out}
		if err := studio.scalarFields(fields, "plans", "memories", "decisions", "changesets", "locations", "routes"); err != nil {
			return err
		}
		for _, collection := range []struct{ key, kind string }{{"plans", "plan"}, {"memories", "memory"}, {"decisions", "decision"}, {"changesets", "changeset"}, {"locations", "location"}, {"routes", "route"}} {
			kind := collection.kind
			if err := studio.field(collection.key, func(out io.Writer) error {
				return streamArchiveRows(db, out, "SELECT document FROM project_parts WHERE project_id=? AND kind=? ORDER BY position,id", id, kind)
			}); err != nil {
				return err
			}
		}
		_, err := io.WriteString(out, "}")
		return err
	}); err != nil {
		return "", err
	}
	if err := object.field("conversations", func(out io.Writer) error { return streamArchiveConversations(db, out, id) }); err != nil {
		return "", err
	}
	if err := object.field("jobs", func(out io.Writer) error {
		return streamArchiveRows(db, out, "SELECT json_remove(document,'$.provider_context','$.execution_owner') FROM runs WHERE project_id=? ORDER BY rowid", id)
	}); err != nil {
		return "", err
	}
	if err := object.field("writing_sessions", func(out io.Writer) error {
		return streamArchiveRows(db, out, "SELECT document FROM writing_sessions WHERE project_id=? ORDER BY rowid", id)
	}); err != nil {
		return "", err
	}
	if err := object.field("wiki_sessions", func(out io.Writer) error {
		return streamArchiveRows(db, out, "SELECT document FROM wiki_sessions WHERE project_id=? ORDER BY rowid", id)
	}); err != nil {
		return "", err
	}

	_, err := io.WriteString(out, "}")
	return title, err
}

// exportArchiveTo uses a dedicated WAL reader so model checkpoints can continue during a large export.
func (store *Store) exportArchiveTo(ctx context.Context, id string, out io.Writer, limit int64) (string, error) {
	database, err := sql.Open("sqlite", sqliteDSN(store.path))
	if err != nil {
		return "", err
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	transaction, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer transaction.Rollback()
	bounded := &archiveWriter{writer: out, ctx: ctx, limit: limit}
	if _, err := io.WriteString(bounded, `{"format":"quillweave-4","project":`); err != nil {
		return "", err
	}
	title, err := streamArchiveProject(transaction, bounded, id)
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(bounded, "}"); err != nil {
		return "", err
	}
	return title, transaction.Commit()
}

// legacyArchiveText retains the small JSON command without producing files its transport cannot restore.
func (store *Store) legacyArchiveText(ctx context.Context, id string) (string, string, error) {
	var buffer bytes.Buffer
	title, err := store.exportArchiveTo(ctx, id, &buffer, legacyArchiveBytes)
	if err != nil {
		return "", "", errors.Join(errors.New("Use the streaming backup transfer for large archives"), err)
	}
	return title + ".quillweave.json", buffer.String(), nil
}
