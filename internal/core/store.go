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

type Store struct {
	db   *sql.DB
	path string
}

type projectListCounts struct {
	Chapters int `json:"chapters"`
	Words    int `json:"words"`
}

type projectCard struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Genre        string `json:"genre"`
	Premise      string `json:"premise"`
	UpdatedAt    string `json:"updated_at"`
	ChapterCount int    `json:"chapter_count"`
	WordCount    int    `json:"word_count"`
}

// listProjectCards reads cached metadata and only scans chapter bodies for older writers.
func (store *Store) listProjectCards() ([]projectCard, error) {
	result := []projectCard{}
	err := store.withReadSnapshot(func(db documentDB) error {
		rows, err := db.QueryContext(context.Background(), "SELECT document FROM projects")
		if err != nil {
			return err
		}
		defer rows.Close()
		type cardDocument struct {
			projectCard
			Counts *projectListCounts `json:"_list_counts"`
		}
		documents := []cardDocument{}
		for rows.Next() {
			var raw string
			var document cardDocument
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			if err := decodeJSON([]byte(raw), &document); err != nil {
				return err
			}
			documents = append(documents, document)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		for _, document := range documents {
			counts := document.Counts
			if counts == nil {
				legacy, err := readProjectListCounts(db, document.ID)
				if err != nil {
					return err
				}
				counts = &legacy
			}
			document.ChapterCount, document.WordCount = counts.Chapters, counts.Words
			result = append(result, document.projectCard)
		}
		return nil
	})
	sort.SliceStable(result, func(i, j int) bool { return result[i].UpdatedAt > result[j].UpdatedAt })
	return result, err
}

// readProjectListCounts preserves exact Unicode counting for pre-cache databases.
func readProjectListCounts(db documentDB, projectID string) (projectListCounts, error) {
	counts := projectListCounts{}
	rows, err := db.QueryContext(context.Background(), "SELECT json_extract(document,'$.body') FROM project_parts WHERE project_id=? AND kind='chapter'", projectID)
	if err != nil {
		return counts, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return counts, err
		}
		counts.Chapters++
		counts.Words += wordCount(body)
	}
	return counts, rows.Err()
}

type documentDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// sqliteDSN applies durability settings on every physical connection, before its first statement.
// An Exec after Open would miss connections created when a bad connection is discarded.
func sqliteDSN(path string) string {
	return path + "?_busy_timeout=5000&_journal_mode=WAL&_synchronous=FULL&_foreign_keys=ON"
}

// OpenStore opens the existing SQLite tables and JSON documents with atomic commit settings.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > 6 {
		db.Close()
		return nil, errors.New("This database requires a newer application version")
	}
	_, err = db.Exec(`
    CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,document TEXT NOT NULL);
    CREATE TABLE IF NOT EXISTS settings(id INTEGER PRIMARY KEY CHECK(id=1),document TEXT NOT NULL);
    CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,document TEXT NOT NULL);
    `)
	if err != nil {
		db.Close()
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err == nil {
		canonical, err = filepath.Abs(canonical)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	store := &Store{db: db, path: canonical}
	var count int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version < 2 {
		if err := db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count); err != nil {
			db.Close()
			return nil, err
		}
		if count > 0 {
			backup := path + ".before-studio.sqlite3"
			if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
				if _, err := db.Exec("VACUUM INTO ?", backup); err != nil {
					db.Close()
					return nil, fmt.Errorf("Create migration recovery backup: %w", err)
				}
				if err := os.Chmod(backup, 0600); err != nil {
					db.Close()
					return nil, err
				}
			}
		}
	}
	if err := store.migrateStudioStore(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrateSearchStore(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.ensureLookupIndexes(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrateWorkspaceStore(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrateExternalStore(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.migrateExternalWikiStore(); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(credentialSchema); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

// ensureLookupIndexes matches existing lookup/order clauses and also runs for already-upgraded databases.
func (store *Store) ensureLookupIndexes() error {
	_, err := store.db.Exec(`
CREATE INDEX IF NOT EXISTS project_parts_load_order ON project_parts(project_id,position,id);
CREATE INDEX IF NOT EXISTS runs_project ON runs(project_id);
CREATE INDEX IF NOT EXISTS search_runs_latest_global_test ON search_runs(profile_id,created_at DESC) WHERE purpose='test' AND project_id IS NULL;
CREATE INDEX IF NOT EXISTS project_parts_changeset_status ON project_parts(project_id,kind,json_extract(document,'$.status'),position,id) WHERE kind='changeset';
`)
	return err
}

// Close releases database connections for shutdown and temporary-directory cleanup.
func (store *Store) Close() error { return store.db.Close() }

// List returns independent project documents ordered by their latest update.
func (store *Store) List() ([]Project, error) {
	var projects []Project
	err := store.withReadSnapshot(func(db documentDB) error {
		var err error
		projects, err = listProjects(db)
		return err
	})
	return projects, err
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
	rows.Close()
	for i := range projects {
		if err := hydrateProject(db, &projects[i]); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].UpdatedAt > projects[j].UpdatedAt })
	return projects, nil
}

// Get returns an explicit error for a missing project.
func (store *Store) Get(id string) (Project, error) {
	var project Project
	err := store.withReadSnapshot(func(db documentDB) error {
		var err error
		project, err = readProject(db, id)
		return err
	})
	return project, err
}

// withReadSnapshot gives every statement in an aggregate read the same committed database view.
func (store *Store) withReadSnapshot(read func(documentDB) error) error {
	transaction, err := store.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err := read(transaction); err != nil {
		return err
	}
	return transaction.Commit()
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
	if err == nil {
		err = hydrateProject(db, &project)
	}
	return project, err
}

// Insert writes prose, history, and knowledge as one aggregate document.
func (store *Store) Insert(project *Project) error {
	if project.Revision > math.MaxInt64 {
		return errors.New("Revision is out of range")
	}
	rekeyArchive(project)
	return store.withWriteTransaction(func(conn *sql.Conn) error {
		if err := insertProjectIn(conn, project); err != nil {
			return err
		}
		if err := importConversations(conn, project); err != nil {
			return err
		}
		return importArchiveRuntime(conn, project)
	})
}

// insertProjectIn writes only canonical project records so streamed runtime imports can share its transaction.
func insertProjectIn(db documentDB, project *Project) error {
	metadata, parts := splitProject(project)
	raw, err := projectMetadataJSON(metadata, project)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(context.Background(), "INSERT INTO projects(id,revision,document) VALUES(?,?,?)", project.ID, int64(project.Revision), string(raw)); err != nil {
		return err
	}
	return saveParts(db, project.ID, parts)
}

// DeleteProject removes an unchanged project and all owned task history in one transaction.
func (store *Store) DeleteProject(id string, expected uint64) error {
	return store.withWriteTransaction(func(connection *sql.Conn) error {
		project, err := readProject(connection, id)
		if err != nil {
			return err
		}
		if project.Revision != expected {
			return errors.New("This project changed. Close and reopen the delete confirmation before trying again")
		}
		var active int
		if err := connection.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM runs WHERE project_id=? AND json_extract(document,'$.status')='running'", id).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return errors.New("This project has a running AI task. Stop it and wait for it to finish before deleting")
		}
		if err := connection.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM search_runs WHERE project_id=? AND status IN ('queued','running')", id).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return errors.New("This project has an active search. Cancel it before deleting")
		}
		if _, err := connection.ExecContext(context.Background(), "DELETE FROM runs WHERE project_id=?", id); err != nil {
			return err
		}
		if _, err := connection.ExecContext(context.Background(), "DELETE FROM story_fts WHERE project_id=?", id); err != nil {
			return err
		}
		_, err = connection.ExecContext(context.Background(), "DELETE FROM projects WHERE id=?", id)
		return err
	})
}

// Save uses a SQL revision comparison to reject stale concurrent writes.
func (store *Store) Save(project *Project, expected uint64) error {
	return store.withWriteTransaction(func(conn *sql.Conn) error { return saveProject(conn, project, expected) })
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
	metadata, parts := splitProject(project)
	raw, err := projectMetadataJSON(metadata, project)
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
		var current uint64
		_ = db.QueryRowContext(context.Background(), "SELECT revision FROM projects WHERE id=?", project.ID).Scan(&current)
		return externalFailure("REVISION_CONFLICT", "Another operation changed this project. Keep your draft and refresh before saving.", map[string]any{"expected_revision": expected, "current_revision": current})
	}
	return saveParts(db, project.ID, parts)
}

// ModelSettings reads public model settings without exposing the separate encrypted credentials.
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
		if err := saveModelSettings(connection, settings, expected); err != nil {
			return err
		}
		return store.saveModelCredentials(connection, settings)
	})
}

// saveModelSettings writes a settings document through a locked connection with revision protection.
func saveModelSettings(connection *sql.Conn, settings ModelSettings, expected uint64) error {
	if err := validateSearchModelChanges(connection, settings); err != nil {
		return err
	}
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
	result, err := store.db.Exec("INSERT INTO runs(id,project_id,document) SELECT ?,?,? WHERE EXISTS(SELECT 1 FROM projects WHERE id=?) ON CONFLICT(id) DO UPDATE SET document=excluded.document", id, projectID, document, projectID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err == nil && changed == 0 {
		return errors.New("Project not found")
	}
	return err
}

// RunProviderContext reads private context by task ID and project, preserving history ownership boundaries.
func (store *Store) RunProviderContext(projectID, jobID string) (providerContext, string, error) {
	var private, text, user sql.NullString
	err := store.db.QueryRow(`SELECT json_extract(document,'$.provider_context'),json_extract(document,'$.text'),
		json_extract(document,'$.prompt.messages[#-1].content') FROM runs WHERE id=? AND project_id=?
        AND json_extract(document,'$.status')='done'
		AND json_extract(document,'$.action') IN ('ask','architecture','outline','agent','naming','research','continue','rewrite','review','memory_update')`, jobID, projectID).Scan(&private, &text, &user)
	if errors.Is(err, sql.ErrNoRows) {
		return providerContext{}, "", nil
	}
	if err != nil {
		return providerContext{}, "", err
	}
	result := providerContext{UserContent: user.String}
	if private.Valid {
		if err := decodeJSON([]byte(private.String), &result); err != nil {
			return providerContext{}, "", err
		}
	}
	return result, text.String, nil
}

// Runs returns the latest hundred public task records with private provider state removed in SQL.
func (store *Store) Runs(projectID string) ([]json.RawMessage, error) {
	rows, err := store.db.Query("SELECT json_remove(document,'$.provider_context','$.execution_owner') FROM runs WHERE project_id=? ORDER BY rowid DESC LIMIT 100", projectID)
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
		job, err := decodeStoredJob(raw)
		if err != nil {
			return nil, err
		}
		result = append(result, rawMessage(job))
	}
	return result, rows.Err()
}
