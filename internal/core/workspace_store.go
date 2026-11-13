package core

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// migrateWorkspaceStore protects new map and review records from older writers and retains a recovery copy.
func (store *Store) migrateWorkspaceStore() error {
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version >= 4 {
		return nil
	}
	if version != 3 {
		return errors.New("Unsupported workspace database version")
	}
	if err := store.requireIdleUpgrade(); err != nil {
		return err
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		backup := store.path + ".before-workspace.sqlite3"
		if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
			if _, err := store.db.Exec("VACUUM INTO ?", backup); err != nil {
				return fmt.Errorf("Create workspace migration recovery backup: %w", err)
			}
			if err := os.Chmod(backup, 0600); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return store.withWriteTransaction(func(db *sql.Conn) error {
		if err := db.QueryRowContext(background(), "PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version == 4 {
			return nil
		}
		if version != 3 {
			return errors.New("Workspace database changed during upgrade")
		}
		if _, err := db.ExecContext(background(), "UPDATE conversations SET document=json_set(document,'$.origin_chapter_id',chapter_id,'$.chapter_id',''),chapter_id='' WHERE chapter_id!=''"); err != nil {
			return err
		}
		if _, err := db.ExecContext(background(), "UPDATE integrations SET document=json_remove(document,'$.hindsight_url','$.hindsight_key')"); err != nil {
			return err
		}
		_, err := db.ExecContext(background(), "PRAGMA user_version=4")
		return err
	})
}
