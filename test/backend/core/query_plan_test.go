//go:build quillweave_whitebox

package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type queryPlanCase struct {
	name, source, statement, countSQL, index string
	args                                     []any
	wantRows, wantScope                      int
}

// queryFixtureRows compares ordered SQL output without duplicating business sorting or hydration logic.
func queryFixtureRows(t *testing.T, db *sql.DB, statement string, args ...any) (int, string) {
	t.Helper()
	rows, err := db.Query(statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	hash, count := sha256.New(), 0
	for rows.Next() {
		values, targets := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		hash.Write(data)
		hash.Write([]byte{'\n'})
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return count, fmt.Sprintf("%x", hash.Sum(nil))
}

// explainFixtureQuery reads SQLite's four-column diagnostic plan; this parsing stays entirely in tests.
func explainFixtureQuery(t *testing.T, db *sql.DB, statement string, args ...any) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	parts := []string{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(parts, "; ")
}

// seedQueryPlanFixture uses only permanent production tables inside t.TempDir, with intentionally uneven scopes.
func seedQueryPlanFixture(t *testing.T, store *Store) {
	t.Helper()
	err := store.withWriteTransaction(func(db *sql.Conn) error {
		ctx := context.Background()
		if _, err := db.ExecContext(ctx, `INSERT INTO projects VALUES('p1',0,'{}'),('p2',0,'{}')`); err != nil {
			return err
		}
		parts, err := db.PrepareContext(ctx, "INSERT INTO project_parts VALUES(?,?,?,?,?)")
		if err != nil {
			return err
		}
		defer parts.Close()
		for i := 0; i < 2000; i++ {
			project := "p1"
			if i%2 != 0 {
				project = "p2"
			}
			kind := []string{"chapter", "source", "memory"}[i%3]
			if _, err := parts.ExecContext(ctx, project, kind, fmt.Sprintf("part%05d", i), i%100, "{}"); err != nil {
				return err
			}
		}
		runs, err := db.PrepareContext(ctx, "INSERT INTO runs VALUES(?,?,?)")
		if err != nil {
			return err
		}
		defer runs.Close()
		search, err := db.PrepareContext(ctx, `INSERT INTO search_runs(id,project_id,request_scope,request_id,payload_hash,profile_id,purpose,operation,status,execution_owner,document,created_at) VALUES(?,NULL,'global',?,'fixture',?,'test','search',?,'fixture','{}',?)`)
		if err != nil {
			return err
		}
		defer search.Close()
		start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := 0; i < 10000; i++ {
			project := "p2"
			if i%10 == 0 {
				project = "p1"
			}
			if _, err := runs.ExecContext(ctx, fmt.Sprintf("run%05d", i), project, `{"status":"done","execution_owner":"fixture"}`); err != nil {
				return err
			}
			profile, status := "other", "succeeded"
			if i%500 == 0 {
				profile = "target"
			}
			if i%2500 == 0 {
				status = "running"
			}
			if _, err := search.ExecContext(ctx, fmt.Sprintf("search%05d", i), fmt.Sprintf("req%05d", i), profile, status, start.Add(time.Duration(i)*time.Second).Format(time.RFC3339)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestDatabaseQueryPlans checks ten real SQL shapes, an existing-v3 upgrade, and unchanged ordered results.
func TestDatabaseQueryPlans(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.sqlite3")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			store.Close()
		}
	})
	seedQueryPlanFixture(t, store)
	cases := []queryPlanCase{
		{"parts", "store_parts.go", "SELECT kind,document FROM project_parts WHERE project_id=? ORDER BY position,id", "SELECT COUNT(*) FROM project_parts WHERE project_id=?", "project_parts_load_order", []any{"p1"}, 1000, 1000},
		{"missing_parts", "store_parts.go", "SELECT kind,document FROM project_parts WHERE project_id=? ORDER BY position,id", "SELECT COUNT(*) FROM project_parts WHERE project_id=?", "project_parts_load_order", []any{"absent"}, 0, 0},
		{"history", "store.go", "SELECT json_remove(document,'$.provider_context','$.execution_owner') FROM runs WHERE project_id=? ORDER BY rowid DESC LIMIT 100", "SELECT COUNT(*) FROM runs WHERE project_id=?", "runs_project", []any{"p1"}, 100, 1000},
		{"missing_history", "store.go", "SELECT json_remove(document,'$.provider_context','$.execution_owner') FROM runs WHERE project_id=? ORDER BY rowid DESC LIMIT 100", "SELECT COUNT(*) FROM runs WHERE project_id=?", "runs_project", []any{"absent"}, 0, 0},
		{"latest_test", "search_commands.go", "SELECT id FROM search_runs WHERE profile_id=? AND purpose='test' AND project_id IS NULL ORDER BY created_at DESC LIMIT 1", "SELECT COUNT(*) FROM search_runs WHERE profile_id=? AND purpose='test' AND project_id IS NULL", "search_runs_latest_global_test", []any{"target"}, 1, 20},
		{"missing_test", "search_commands.go", "SELECT id FROM search_runs WHERE profile_id=? AND purpose='test' AND project_id IS NULL ORDER BY created_at DESC LIMIT 1", "SELECT COUNT(*) FROM search_runs WHERE profile_id=? AND purpose='test' AND project_id IS NULL", "search_runs_latest_global_test", []any{"absent"}, 0, 0},
		{"run_id", "search_run_store.go", "SELECT document,revision,project_id,status,execution_owner,payload_hash FROM search_runs WHERE id=?", "SELECT COUNT(*) FROM search_runs WHERE id=?", "sqlite_autoindex_search_runs_1", []any{"search00000"}, 1, 1},
		{"request_id", "search_run_store.go", "SELECT id FROM search_runs WHERE request_scope=? AND request_id=?", "SELECT COUNT(*) FROM search_runs WHERE request_scope=? AND request_id=?", "sqlite_autoindex_search_runs_2", []any{"global", "req00001"}, 1, 1},
		{"active", "search_runs.go", "SELECT COUNT(*) FROM search_runs WHERE status IN ('queued','running')", "SELECT COUNT(*) FROM search_runs WHERE status IN ('queued','running')", "search_runs_active", nil, 1, 4},
		{"changeset_status", "changeset_commands.go", "SELECT document FROM project_parts WHERE project_id=? AND kind='changeset' AND json_extract(document,'$.status')=? ORDER BY position,id", "SELECT COUNT(*) FROM project_parts WHERE project_id=? AND kind='changeset' AND json_extract(document,'$.status')=?", "project_parts_changeset_status", []any{"p1", "staged"}, 0, 0},
		{"all_projects", "store.go", "SELECT document FROM projects", "SELECT COUNT(*) FROM projects", "", nil, 2, 2},
	}
	// Reproduce the existing v3 schema, where the three new lookup indexes are absent.
	for _, name := range []string{"project_parts_load_order", "runs_project", "search_runs_latest_global_test"} {
		if _, err := store.db.Exec("DROP INDEX IF EXISTS " + name); err != nil {
			t.Fatal(err)
		}
	}
	beforePlans, beforeResults := map[string]string{}, map[string]string{}
	for _, c := range cases {
		source, err := os.ReadFile(c.source)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(source), c.statement) {
			t.Fatalf("Update this fixture to the actual production SQL: %s", c.name)
		}
		beforePlans[c.name] = explainFixtureQuery(t, store.db, c.statement, c.args...)
		_, beforeResults[c.name] = queryFixtureRows(t, store.db, c.statement, c.args...)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	var sqliteVersion string
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT sqlite_version()").Scan(&sqliteVersion); err != nil {
		t.Fatal(err)
	}
	if version != 4 {
		t.Fatal("An index-only update changed the document version")
	}
	t.Logf("SQLite %s; fixture tables: projects=2 project_parts=2000 runs=10000 search_runs=10000", sqliteVersion)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan := explainFixtureQuery(t, store.db, c.statement, c.args...)
			rows, digest := queryFixtureRows(t, store.db, c.statement, c.args...)
			var scope int
			if err := store.db.QueryRow(c.countSQL, c.args...).Scan(&scope); err != nil {
				t.Fatal(err)
			}
			if rows != c.wantRows || scope != c.wantScope || digest != beforeResults[c.name] {
				t.Fatalf("Changed results: rows=%d scope=%d", rows, scope)
			}
			t.Logf("PLAN %s | before=%s | after=%s | matching_rows=%d returned_rows=%d", c.name, beforePlans[c.name], plan, scope, rows)
			if c.index != "" && (!strings.Contains(plan, "SEARCH") || !strings.Contains(plan, c.index) || strings.Contains(plan, "TEMP B-TREE")) {
				t.Fatalf("Expected indexed lookup without extra sorting: %s", plan)
			}
			if c.name == "all_projects" && !strings.Contains(plan, "SCAN projects") {
				t.Fatal("Review the intentional complete project listing plan")
			}
		})
	}
}
