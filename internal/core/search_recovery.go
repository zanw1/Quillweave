package core

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"quillweave/internal/platform"
	websearch "quillweave/internal/search"
	"regexp"
)

var searchRunIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}$`)

type searchIntent struct {
	Run   searchRun `json:"run"`
	Owner string    `json:"owner"`
	Hash  string    `json:"hash"`
}

// searchIntentPath confines recovery files to a server-generated run ID inside an owner directory.
func searchIntentPath(dir, id string) (string, error) {
	if !searchRunIDPattern.MatchString(id) {
		return "", errors.New("Invalid search recovery identity")
	}
	return filepath.Join(dir, "search-"+id+".final.json"), nil
}

// saveSearchIntent syncs public result data before its terminal database commit, never credentials.
func (engine *Engine) saveSearchIntent(run searchRun) error {
	path, err := searchIntentPath(engine.owner.dir, run.ID)
	if err != nil {
		return err
	}
	engine.searchRuntime.intentMu.Lock()
	defer engine.searchRuntime.intentMu.Unlock()
	data := []byte(jsonText(searchIntent{Run: run, Owner: run.Owner, Hash: run.Hash}))
	file, err := os.CreateTemp(engine.owner.dir, ".search-final-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// removeSearchIntent removes only a result whose terminal state reached the database or was already cancelled.
func (engine *Engine) removeSearchIntent(id string) {
	if path, err := searchIntentPath(engine.owner.dir, id); err == nil {
		engine.removeIntentPath(path)
	}
}

// removeIntentPath serializes deletion with the rename and read of the same sidecar.
func (engine *Engine) removeIntentPath(path string) {
	engine.searchRuntime.intentMu.Lock()
	defer engine.searchRuntime.intentMu.Unlock()
	_ = os.Remove(path)
}

// hasSearchIntents preserves the process directory when an unavailable database left terminal results pending.
func (engine *Engine) hasSearchIntents() bool {
	entries, err := os.ReadDir(engine.owner.dir)
	if err != nil {
		return !errors.Is(err, os.ErrNotExist)
	}
	for _, entry := range entries {
		if !entry.IsDir() && regexp.MustCompile(`^search-[0-9a-f-]{36}\.final\.json$`).MatchString(entry.Name()) {
			return true
		}
	}
	return false
}

// recoverSearchOwner finishes saved results or marks unsaved work interrupted, without replaying a supplier call.
func (engine *Engine) recoverSearchOwner(ownerID, dir string) error {
	return engine.restoreSearchOwner(ownerID, dir, false)
}

// recoverLocalSearchIntents retries current-process terminal data after temporary storage errors.
func (engine *Engine) recoverLocalSearchIntents() error {
	return engine.restoreSearchOwner(engine.owner.id, engine.owner.dir, true)
}

// searchRecoveryDeferred keeps a live worker the sole user of its sidecar until a commit retry is waiting.
func (engine *Engine) searchRecoveryDeferred(id string) bool {
	engine.searchRuntime.mu.Lock()
	task := engine.searchRuntime.tasks[id]
	engine.searchRuntime.mu.Unlock()
	if task == nil {
		return false
	}
	phase := task.phase.Load()
	return phase == searchPhaseRunning || phase == searchPhasePublishing
}

// readSearchIntent loads one sidecar while its writer cannot rename or delete it.
func (engine *Engine) readSearchIntent(dir, id string) (*searchIntent, string, error) {
	path, err := searchIntentPath(dir, id)
	if err != nil {
		return nil, "", err
	}
	engine.searchRuntime.intentMu.Lock()
	defer engine.searchRuntime.intentMu.Unlock()
	file, err := os.Open(path)
	if err != nil {
		return nil, path, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4_000_001))
	if err != nil {
		return nil, path, err
	}
	if len(data) > 4_000_000 {
		return nil, path, errors.New("Search recovery result is too large")
	}
	var value searchIntent
	if err := decodeJSON(data, &value); err != nil {
		return nil, path, err
	}
	return &value, path, nil
}

// searchRevisionConflict reports that another commit already moved the row.
func searchRevisionConflict(err error) bool {
	var fault *websearch.Fault
	return errors.As(err, &fault) && fault.Code == "SEARCH_REVISION_CONFLICT"
}

// restoreSearchOwner validates a terminal sidecar against its authoritative database row before publishing it.
func (engine *Engine) restoreSearchOwner(ownerID, dir string, terminalOnly bool) error {
	rows, err := engine.store.db.Query("SELECT id FROM search_runs WHERE execution_owner=? AND status IN ('queued','running')", ownerID)
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
	for _, id := range ids {
		if terminalOnly && engine.searchRecoveryDeferred(id) {
			continue
		}
		var intent *searchIntent
		path := ""
		if dir != "" {
			var readErr error
			intent, path, readErr = engine.readSearchIntent(dir, id)
			if errors.Is(readErr, os.ErrNotExist) {
				intent = nil
			} else if readErr != nil {
				if terminalOnly && platform.RetryableFileError(readErr) {
					continue
				}
				return readErr
			}
		}
		if terminalOnly && intent == nil {
			continue
		}
		_, err := engine.store.changeSearchRun(id, func(run *searchRun) error {
			if run.Owner != ownerID {
				return errors.New("Search ownership changed")
			}
			if intent != nil {
				if intent.Owner != ownerID || intent.Hash != run.Hash || intent.Run.ID != id || intent.Run.ProjectID != run.ProjectID || !contains([]string{"succeeded", "empty", "failed", "cancelled"}, intent.Run.Status) {
					return errors.New("Invalid search recovery intent")
				}
				revision, owner, hash := run.Revision, run.Owner, run.Hash
				*run = intent.Run
				run.Revision = revision
				run.Owner = owner
				run.Hash = hash
			} else {
				run.Status = "interrupted"
				run.FinishedAt = now()
				run.Error = websearch.Fail("SEARCH_INTERRUPTED", "Search was interrupted. It was not retried automatically; the supplier may already have charged it", 409)
			}
			return nil
		})
		if searchRevisionConflict(err) {
			current, readErr := readSearchRun(engine.store.db, id)
			if readErr != nil {
				return readErr
			}
			if !terminalSearch(current.Status) {
				continue
			}
		} else if err != nil {
			return err
		}
		if path != "" {
			engine.removeIntentPath(path)
		}
	}
	return nil
}
