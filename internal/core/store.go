package core

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"sort"
)

type Store struct{ db *sql.DB }

type documentDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// OpenStore opens the existing SQLite tables and JSON documents with atomic commit settings.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;
    CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,document TEXT NOT NULL);
    CREATE TABLE IF NOT EXISTS settings(id INTEGER PRIMARY KEY CHECK(id=1),document TEXT NOT NULL);
    CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,document TEXT NOT NULL);
    PRAGMA user_version=1;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases database connections for shutdown and temporary-directory cleanup.
func (store *Store) Close() error { return store.db.Close() }

// List returns independent project documents ordered by their latest update.
func (store *Store) List() ([]Project, error) {
	return listProjects(store.db)
}

// listProjects reads projects and model references through a connection or shared write transaction.
func listProjects(db documentDB) ([]Project, error) {
	rows, err := db.QueryContext(context.Background(), "SELECT document FROM projects")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []Project{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var project Project
		if err := decodeJSON([]byte(raw), &project); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].UpdatedAt > projects[j].UpdatedAt })
	return projects, nil
}

// Get returns an explicit error for a missing project.
func (store *Store) Get(id string) (Project, error) {
	return readProject(store.db, id)
}

// readProject uses the transaction connection for project revision checks and subsequent writes.
func readProject(db documentDB, id string) (Project, error) {
	var raw string
	var project Project
	err := db.QueryRowContext(context.Background(), "SELECT document FROM projects WHERE id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return project, errors.New("Project not found")
	}
	if err != nil {
		return project, err
	}
	err = decodeJSON([]byte(raw), &project)
	return project, err
}

// Insert writes prose, history, and knowledge as one aggregate document.
func (store *Store) Insert(project *Project) error {
	if project.Revision > math.MaxInt64 {
		return errors.New("Revision is out of range")
	}
	raw, err := jsonBytes(project)
	if err != nil {
		return err
	}
	_, err = store.db.Exec("INSERT INTO projects(id,revision,document) VALUES(?,?,?)", project.ID, int64(project.Revision), string(raw))
	return err
}

// Save uses a SQL revision comparison to reject stale concurrent writes.
func (store *Store) Save(project *Project, expected uint64) error {
	return saveProject(store.db, project, expected)
}

// saveProject shares revision-checked writes between project edits and model assignments.
func saveProject(db documentDB, project *Project, expected uint64) error {
	if expected == math.MaxUint64 {
		return errors.New("Revision is out of range")
	}
	project.Revision = expected + 1
	project.UpdatedAt = now()
	if project.Revision > math.MaxInt64 || expected > math.MaxInt64 {
		return errors.New("Revision is out of range")
	}
	raw, err := jsonBytes(project)
	if err != nil {
		return err
	}
	result, err := db.ExecContext(context.Background(), "UPDATE projects SET revision=?,document=? WHERE id=? AND revision=?", int64(project.Revision), string(raw), project.ID, int64(expected))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return errors.New("Another operation changed this project. Keep your draft and refresh before saving.")
	}
	return nil
}

// ModelSettings reads legacy single-model settings and keeps credentials process-local.
func (store *Store) ModelSettings() (ModelSettings, error) {
	return readModelSettings(store.db)
}

// readModelSettings loads current settings inside reads or assignment transactions, including fresh databases.
func readModelSettings(db documentDB) (ModelSettings, error) {
	var raw string
	err := db.QueryRowContext(context.Background(), "SELECT document FROM settings WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultModels(), nil
	}
	if err != nil {
		return ModelSettings{}, err
	}
	return parseModels(raw)
}

// withWriteTransaction takes the SQLite write lock before validation to serialize cross-instance changes.
func (store *Store) withWriteTransaction(change func(*sql.Conn) error) (err error) {
	ctx := context.Background()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			if _, rollbackErr := connection.ExecContext(ctx, "ROLLBACK"); rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
				// Discard connections whose rollback failed so unfinished transactions cannot return to the pool.
				_ = connection.Raw(func(any) error { return driver.ErrBadConn })
			}
		}
	}()
	if err := change(connection); err != nil {
		return err
	}
	_, err = connection.ExecContext(ctx, "COMMIT")
	committed = err == nil
	return err
}

// SaveModelSettings checks project references and settings revision inside the same write transaction.
func (store *Store) SaveModelSettings(settings ModelSettings, expected uint64) error {
	return store.withWriteTransaction(func(connection *sql.Conn) error {
		return saveModelSettings(connection, settings, expected)
	})
}

// saveModelSettings writes a settings document through a locked connection with revision protection.
func saveModelSettings(connection *sql.Conn, settings ModelSettings, expected uint64) error {
	var previous string
	err := connection.QueryRowContext(context.Background(), "SELECT document FROM settings WHERE id=1").Scan(&previous)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	current := defaultModels()
	if exists {
		current, err = parseModels(previous)
		if err != nil {
			return err
		}
	}
	if current.Revision != expected {
		return errors.New("Model settings changed in another window. Reopen settings and try again")
	}
	projects, err := listProjects(connection)
	if err != nil {
		return err
	}
	for _, project := range projects {
		if settings.ValidateRoutes(project.ModelOverrides) != nil {
			return fmt.Errorf("Project \"%s\" still references this model. Change its model selection before deleting or disabling the profile", project.Title)
		}
	}
	raw, err := jsonBytes(settings.WithoutSecrets())
	if err != nil {
		return err
	}
	var result sql.Result
	if exists {
		result, err = connection.ExecContext(context.Background(), "UPDATE settings SET document=? WHERE id=1 AND document=?", string(raw), previous)
	} else {
		result, err = connection.ExecContext(context.Background(), "INSERT INTO settings(id,document) VALUES(1,?) ON CONFLICT(id) DO NOTHING", string(raw))
	}
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("Model settings save conflict. Try again")
	}
	return nil
}

// SetProjectModels shares the model-deletion transaction boundary and validates current model references.
func (store *Store) SetProjectModels(id string, expected uint64, overrides map[string]string) (Project, error) {
	var project Project
	err := store.withWriteTransaction(func(connection *sql.Conn) error {
		models, err := readModelSettings(connection)
		if err != nil {
			return err
		}
		if err := models.ValidateRoutes(overrides); err != nil {
			return err
		}
		project, err = readProject(connection, id)
		if err != nil {
			return err
		}
		if project.Revision != expected {
			return errors.New("The project changed. Your edits are retained. Refresh and try again.")
		}
		project.ModelOverrides = overrides
		return saveProject(connection, &project, expected)
	})
	return project, err
}

// SaveRun stores task output and context from a credential-free job document.
func (store *Store) SaveRun(id, projectID, document string) error {
	_, err := store.db.Exec("INSERT INTO runs(id,project_id,document) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document", id, projectID, document)
	return err
}

// RunProviderContext reads private context by task ID and project, preserving history ownership boundaries.
func (store *Store) RunProviderContext(projectID, jobID string) (providerContext, string, error) {
	var fingerprint, reasoning, encrypted, text sql.NullString
	err := store.db.QueryRow(`SELECT json_extract(document,'$.provider_context.fingerprint'),
        json_extract(document,'$.provider_context.reasoning'),json_extract(document,'$.provider_context.encrypted'),
        json_extract(document,'$.text') FROM runs WHERE id=? AND project_id=?
        AND json_extract(document,'$.status')='done'
        AND json_extract(document,'$.action') IN ('ask','architecture','outline')`, jobID, projectID).Scan(&fingerprint, &reasoning, &encrypted, &text)
	if errors.Is(err, sql.ErrNoRows) {
		return providerContext{}, "", nil
	}
	if err != nil {
		return providerContext{}, "", err
	}
	return providerContext{Fingerprint: fingerprint.String, Reasoning: reasoning.String, Encrypted: encrypted.String}, text.String, nil
}

// Runs returns the latest hundred public task records with private provider state removed in SQL.
func (store *Store) Runs(projectID string) ([]json.RawMessage, error) {
	rows, err := store.db.Query("SELECT json_remove(document,'$.provider_context') FROM runs WHERE project_id=? ORDER BY rowid DESC LIMIT 100", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []json.RawMessage{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var check any
		if err := decodeJSON([]byte(raw), &check); err != nil {
			return nil, err
		}
		result = append(result, json.RawMessage(raw))
	}
	return result, rows.Err()
}
