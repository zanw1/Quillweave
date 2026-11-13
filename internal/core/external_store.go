package core

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// migrateExternalStore protects durable external sessions from older workspace writers.
func (store *Store) migrateExternalStore() error {
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version >= 5 {
		return nil
	}
	if version != 4 {
		return errors.New("Unsupported external workspace database version")
	}
	if err := store.requireIdleUpgrade(); err != nil {
		return err
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count); err != nil {
		return err
	}
	backup := store.path + ".before-external-agents.sqlite3"
	if count > 0 {
		if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
			if _, err := store.db.Exec("VACUUM INTO ?", backup); err != nil {
				return fmt.Errorf("Create external workspace recovery backup: %w", err)
			}
			if err := os.Chmod(backup, 0600); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return store.withWriteTransaction(func(db *sql.Conn) error {
		if _, err := db.ExecContext(background(), `
CREATE TABLE IF NOT EXISTS writing_sessions(
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL, document TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS writing_sessions_project ON writing_sessions(project_id);
CREATE TABLE IF NOT EXISTS run_cancel_requests(
 run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE, requested_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS external_receipts(
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 request_id TEXT NOT NULL, operation TEXT NOT NULL, request_hash TEXT NOT NULL,
 result TEXT NOT NULL, PRIMARY KEY(project_id,request_id));
PRAGMA user_version=5;`); err != nil {
			return err
		}
		return nil
	})
}

// externalReceipt replays a committed request and refuses accidental reuse for different content.
func externalReceipt(db documentDB, projectID, operation, requestID, fingerprint string) (any, bool, error) {
	if requestID == "" {
		return nil, false, externalFailure("INVALID_INPUT", "A stable request_id is required for this write")
	}
	var storedOperation, storedHash, result string
	err := db.QueryRowContext(background(), "SELECT operation,request_hash,result FROM external_receipts WHERE project_id=? AND request_id=?", projectID, requestID).Scan(&storedOperation, &storedHash, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if storedOperation != operation || storedHash != fingerprint {
		return nil, false, externalFailure("REQUEST_CONFLICT", "This request_id was already used for different content")
	}
	var value any
	err = decodeJSON([]byte(result), &value)
	return value, true, err
}

// saveExternalReceipt commits the response in the same transaction as the domain mutation.
func saveExternalReceipt(db documentDB, projectID, operation, requestID, fingerprint string, result any) error {
	_, err := db.ExecContext(background(), "INSERT INTO external_receipts(project_id,request_id,operation,request_hash,result) VALUES(?,?,?,?,?)", projectID, requestID, operation, fingerprint, jsonText(result))
	return err
}
