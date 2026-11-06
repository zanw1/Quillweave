package core

import (
	"database/sql"
	"errors"
	websearch "quillweave/internal/search"
)

// readSearchRun restores public data with authoritative scope, ownership and CAS columns.
func readSearchRun(db documentDB, id string) (searchRun, error) {
	var raw, status, owner, hash string
	var revision uint64
	var project sql.NullString
	err := db.QueryRowContext(background(), "SELECT document,revision,project_id,status,execution_owner,payload_hash FROM search_runs WHERE id=?", id).Scan(&raw, &revision, &project, &status, &owner, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return searchRun{}, websearch.Fail("SEARCH_RUN_NOT_FOUND", "Search run was not found", 404)
	}
	if err != nil {
		return searchRun{}, err
	}
	var run searchRun
	if err := decodeJSON([]byte(raw), &run); err != nil {
		return run, err
	}
	if run.ID != id || run.ProjectID != project.String {
		return run, errors.New("Search run metadata is inconsistent")
	}
	run.Revision = revision
	run.Status = status
	run.Owner = owner
	run.Hash = hash
	return run, nil
}

// updateSearchRun serializes budgets, checkpoints and terminal states with the current revision.
func updateSearchRun(db documentDB, run *searchRun, expected uint64) error {
	run.Revision = expected + 1
	var finished any
	if run.FinishedAt != "" {
		finished = run.FinishedAt
	}
	result, err := db.ExecContext(background(), "UPDATE search_runs SET revision=?,status=?,document=?,finished_at=? WHERE id=? AND revision=? AND execution_owner=? AND status IN ('queued','running')", run.Revision, run.Status, jsonText(run), finished, run.ID, expected, run.Owner)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return websearch.Fail("SEARCH_REVISION_CONFLICT", "The search state changed before this update", 409)
	}
	return nil
}

// changeSearchRun operates on a fresh row and never lets a delayed worker replace a terminal outcome.
func (store *Store) changeSearchRun(id string, change func(*searchRun) error) (searchRun, error) {
	var run searchRun
	err := store.withWriteTransaction(func(db *sql.Conn) error {
		var err error
		run, err = readSearchRun(db, id)
		if err != nil {
			return err
		}
		if terminalSearch(run.Status) {
			return nil
		}
		revision := run.Revision
		if err := change(&run); err != nil {
			return err
		}
		return updateSearchRun(db, &run, revision)
	})
	return run, err
}

// searchScope distinguishes identical request IDs in separate works and the global test area.
func searchScope(projectID string) string {
	if projectID == "" {
		return "global"
	}
	return "project:" + projectID
}

// findSearchRequest checks an accepted request before resolving settings that may have changed since its first attempt.
func findSearchRequest(db documentDB, scope, requestID string) (searchRun, bool, error) {
	var id string
	err := db.QueryRowContext(background(), "SELECT id FROM search_runs WHERE request_scope=? AND request_id=?", scope, requestID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return searchRun{}, false, nil
	}
	if err != nil {
		return searchRun{}, false, err
	}
	run, err := readSearchRun(db, id)
	return run, true, err
}

// getSearchRun validates the UI's work scope rather than accepting a run ID from another open project.
func (engine *Engine) getSearchRun(id, projectID string) (searchRun, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := engine.reconcileJobsLocked(); err != nil {
		return searchRun{}, err
	}
	run, err := readSearchRun(engine.store.db, id)
	if err != nil {
		return run, err
	}
	if run.ProjectID != projectID {
		return searchRun{}, websearch.Fail("SEARCH_SCOPE_MISMATCH", "This search belongs to another project or the global test area", 403)
	}
	return run, nil
}

// cancelSearchRun is idempotent and publishes cancellation before any later response can complete the run.
func (engine *Engine) cancelSearchRun(id, projectID string) (searchRun, error) {
	run, err := readSearchRun(engine.store.db, id)
	if err != nil {
		return run, err
	}
	if run.ProjectID != projectID {
		return searchRun{}, websearch.Fail("SEARCH_SCOPE_MISMATCH", "This search belongs to another project", 403)
	}
	engine.searchRuntime.mu.Lock()
	task := engine.searchRuntime.tasks[id]
	if task != nil {
		task.cancel()
	}
	engine.searchRuntime.mu.Unlock()
	return engine.store.changeSearchRun(id, func(run *searchRun) error {
		run.Status = "cancelled"
		run.FinishedAt = now()
		run.Error = websearch.Fail("SEARCH_CANCELLED", "Search was cancelled; already accepted supplier requests may still be billed", 409)
		return nil
	})
}
