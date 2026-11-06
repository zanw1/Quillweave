package core

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"quillweave/internal/platform"
	websearch "quillweave/internal/search"
	"strings"
)

const searchSchema = `
CREATE TABLE IF NOT EXISTS search_settings(id INTEGER PRIMARY KEY CHECK(id=1),revision INTEGER NOT NULL CHECK(revision>=0),document TEXT NOT NULL CHECK(json_valid(document)),CHECK(COALESCE(json_extract(document,'$.schema_version'),0)=1));
CREATE TABLE IF NOT EXISTS search_runs(id TEXT PRIMARY KEY,revision INTEGER NOT NULL DEFAULT 0 CHECK(revision>=0),project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,request_scope TEXT NOT NULL,request_id TEXT NOT NULL,payload_hash TEXT NOT NULL,profile_id TEXT NOT NULL,purpose TEXT NOT NULL CHECK(purpose IN ('test','research')),operation TEXT NOT NULL CHECK(operation IN ('search','fetch')),status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','empty','failed','cancelled','interrupted')),execution_owner TEXT NOT NULL,document TEXT NOT NULL CHECK(json_valid(document)),created_at TEXT NOT NULL,finished_at TEXT,CHECK((project_id IS NULL AND request_scope='global') OR (project_id IS NOT NULL AND request_scope='project:'||project_id)),UNIQUE(request_scope,request_id));
CREATE INDEX IF NOT EXISTS search_runs_project_time ON search_runs(project_id,created_at DESC);
CREATE INDEX IF NOT EXISTS search_runs_active ON search_runs(status,execution_owner);
CREATE TRIGGER IF NOT EXISTS search_legacy_insert_guard BEFORE INSERT ON integrations WHEN EXISTS(SELECT 1 FROM search_settings WHERE id=1) AND (COALESCE(json_type(NEW.document,'$.providers'),'')<>'array' OR COALESCE(json_array_length(NEW.document,'$.providers'),-1)<>0 OR COALESCE(json_extract(NEW.document,'$.search_enabled'),1)<>0) BEGIN SELECT RAISE(ABORT,'Search configuration was upgraded; use the updated application');END;
CREATE TRIGGER IF NOT EXISTS search_legacy_update_guard BEFORE UPDATE OF document ON integrations WHEN EXISTS(SELECT 1 FROM search_settings WHERE id=1) AND (COALESCE(json_type(NEW.document,'$.providers'),'')<>'array' OR COALESCE(json_array_length(NEW.document,'$.providers'),-1)<>0 OR COALESCE(json_extract(NEW.document,'$.search_enabled'),1)<>0) BEGIN SELECT RAISE(ABORT,'Search configuration was upgraded; use the updated application');END;`

// readSearchSettings reads a versioned public aggregate through the caller's transaction or snapshot.
func readSearchSettings(db documentDB) (websearch.Settings, uint64, error) {
	var raw string
	var revision uint64
	if err := db.QueryRowContext(background(), "SELECT revision,document FROM search_settings WHERE id=1").Scan(&revision, &raw); err != nil {
		return websearch.Settings{}, 0, err
	}
	var settings websearch.Settings
	if err := decodeJSON([]byte(raw), &settings); err != nil {
		return settings, 0, fmt.Errorf("Read search settings: %w", err)
	}
	if settings.SchemaVersion != 1 {
		return settings, 0, errors.New("Unsupported search settings version")
	}
	return settings, revision, nil
}

// migrateSearchStore upgrades metadata atomically and makes legacy search writers fail closed.
func (store *Store) migrateSearchStore() error {
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 3 {
		return errors.New("This database requires a newer application version")
	}
	if version == 3 {
		return nil
	}
	if err := store.requireIdleUpgrade(); err != nil {
		return err
	}
	var populated int
	if err := store.db.QueryRow("SELECT (SELECT COUNT(*) FROM projects)+(SELECT COUNT(*) FROM settings)+(SELECT COUNT(*) FROM integrations)").Scan(&populated); err != nil {
		return err
	}
	if populated > 0 {
		backup := store.path + ".before-search.sqlite3"
		if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
			if _, err := store.db.Exec("VACUUM INTO ?", backup); err != nil {
				return fmt.Errorf("Create search migration recovery backup: %w", err)
			}
			if err := os.Chmod(backup, 0600); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return store.withWriteTransaction(func(db *sql.Conn) error {
		var current int
		if err := db.QueryRowContext(background(), "PRAGMA user_version").Scan(&current); err != nil {
			return err
		}
		if current == 3 {
			return nil
		}
		if current > 3 {
			return errors.New("Unsupported database version")
		}
		old, err := readIntegrations(db)
		if err != nil {
			return err
		}
		settings := settingsFromLegacy(old)
		if _, err := db.ExecContext(background(), searchSchema); err != nil {
			return err
		}
		if _, err := db.ExecContext(background(), "INSERT INTO search_settings(id,revision,document) VALUES(1,0,?)", jsonText(settings)); err != nil {
			return err
		}
		shell := old.WithoutSecrets()
		shell.Providers = []SearchProvider{}
		shell.SearchEnabled = false
		shell.SearchRevision = 0
		if _, err := db.ExecContext(background(), "INSERT INTO integrations(id,document) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document", jsonText(shell)); err != nil {
			return err
		}
		_, err = db.ExecContext(background(), "PRAGMA user_version=3")
		return err
	})
}

// requireIdleUpgrade refuses a known live old instance instead of migrating underneath its cached requests.
func (store *Store) requireIdleUpgrade() error {
	entries, err := os.ReadDir(store.path + ".runtime")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !ownerPattern.MatchString(entry.Name()) {
			continue
		}
		lock, acquired, err := platform.TryFileLock(filepath.Join(store.path+".runtime", entry.Name(), "owner.lock"))
		if err != nil {
			return err
		}
		if !acquired {
			return errors.New("Close other application instances before upgrading search settings")
		}
		if err := lock.Close(); err != nil {
			return err
		}
	}
	return nil
}

// settingsFromLegacy preserves IDs, order, disable state and limits without ever copying a credential value.
func settingsFromLegacy(old IntegrationSettings) websearch.Settings {
	settings := websearch.Settings{SchemaVersion: 1, Enabled: old.SearchEnabled, Profiles: []websearch.Profile{}, FallbackPolicy: "legacy", FallbackProfileIDs: []string{}, Bindings: map[string]websearch.Selection{}}
	names := map[string]bool{}
	for _, provider := range old.Providers {
		profile := websearch.DefaultProfile(provider.Kind)
		profile.ID = "legacy:" + provider.ID
		profile.LegacyID = provider.ID
		profile.Name = provider.Name
		if strings.TrimSpace(profile.Name) == "" {
			profile.Name = profile.Family
		}
		stem := profile.Name
		for index := 2; names[strings.ToLower(profile.Name)]; index++ {
			profile.Name = fmt.Sprintf("%s %d", stem, index)
		}
		names[strings.ToLower(profile.Name)] = true
		profile.Endpoint = provider.Endpoint
		profile.Enabled = provider.Enabled
		profile.Limits.MaxSearches = max(1, min(5, old.MaxSearches))
		settings.Profiles = append(settings.Profiles, profile)
		if provider.Enabled {
			if settings.DefaultProfileID == "" {
				settings.DefaultProfileID = profile.ID
			} else if len(settings.FallbackProfileIDs) < 4 {
				settings.FallbackProfileIDs = append(settings.FallbackProfileIDs, profile.ID)
			}
		}
	}
	for _, family := range []string{"deepseek", "kimi", "openrouter"} {
		profile := websearch.DefaultProfile(family)
		profile.ID = newID()
		stem := profile.Name
		for index := 2; names[strings.ToLower(profile.Name)]; index++ {
			profile.Name = fmt.Sprintf("%s %d", stem, index)
		}
		names[strings.ToLower(profile.Name)] = true
		settings.Profiles = append(settings.Profiles, profile)
	}
	return settings
}

// writeSearchSettings protects the aggregate against stale forms and shared-database peers.
func writeSearchSettings(db documentDB, settings websearch.Settings, expected uint64) (uint64, error) {
	if expected >= uint64(^uint64(0)>>1) {
		return 0, websearch.Fail("SEARCH_INVALID_ARGUMENT", "Search settings revision is out of range", 400)
	}
	result, err := db.ExecContext(background(), "UPDATE search_settings SET revision=revision+1,document=? WHERE id=1 AND revision=?", jsonText(settings), expected)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if count != 1 {
		return 0, websearch.Fail("SEARCH_REVISION_CONFLICT", "Search settings changed. Reload them before saving; your edits are retained", 409)
	}
	return expected + 1, nil
}
