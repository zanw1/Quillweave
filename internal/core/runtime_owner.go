package core

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"quillweave/internal/platform"
	"regexp"
)

type runtimeOwner struct {
	id, root, dir string
	lock          *platform.FileLock
}

var ownerPattern = regexp.MustCompile(`^owner-[0-9a-f-]{36}$`)

// openRuntimeOwner creates a unique process-lifetime claim beside the canonical database path.
func openRuntimeOwner(database string) (*runtimeOwner, error) {
	root := database + ".runtime"
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	id := "owner-" + newID()
	dir := filepath.Join(root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	lock, acquired, err := platform.TryFileLock(filepath.Join(dir, "owner.lock"))
	if err != nil || !acquired {
		os.RemoveAll(dir)
		return nil, errors.Join(err, errors.New("Unable to claim the application runtime"))
	}
	return &runtimeOwner{id: id, root: root, dir: dir, lock: lock}, nil
}

// terminalPath keeps a task's recovery intent within this engine's private runtime directory.
func (owner *runtimeOwner) terminalPath(id string) string {
	return filepath.Join(owner.dir, id+".terminal.json")
}

// saveTerminalIntent syncs a credential-free terminal snapshot before attempting the database transaction.
func (owner *runtimeOwner) saveTerminalIntent(job *Job) error {
	path := owner.terminalPath(job.ID)
	data := []byte(storedJob(job))
	if previous, err := os.ReadFile(path); err == nil && string(previous) == string(data) {
		return nil
	}
	file, err := os.CreateTemp(owner.dir, "terminal-*")
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

// decodeStoredJob restores internal-only ownership and continuation fields without exporting them in Job JSON.
func decodeStoredJob(raw string) (Job, error) {
	var saved struct {
		Job
		ProviderContext *providerContext `json:"provider_context"`
		Owner           string           `json:"execution_owner"`
	}
	err := decodeJSON([]byte(raw), &saved)
	saved.Job.providerContext, saved.Job.ownerID = saved.ProviderContext, saved.Owner
	return saved.Job, err
}

// commitTerminalLocked retries a completed result; the caller holds engine.mu and engine.jobsMu.
func (engine *Engine) commitTerminalLocked(current *Job) error {
	desired := current.pendingFinal
	if desired == nil {
		copy := current.snapshot()
		copy.pendingFinal = nil
		desired = &copy
		current.pendingFinal = desired
	}
	intentErr := engine.owner.saveTerminalIntent(desired)
	if err := engine.store.finishConversation(desired); err != nil {
		current.Status = "error"
		current.Error = "Unable to save task and conversation: " + errors.Join(err, intentErr).Error()
		return err
	}
	current.Status, current.Error, current.Progress = desired.Status, desired.Error, desired.Progress
	current.pendingFinal = nil
	_ = os.Remove(engine.owner.terminalPath(current.ID))
	return nil
}

// reconcileJobsLocked finishes local pending commits and recovers only owners whose OS locks are gone.
func (engine *Engine) reconcileJobsLocked() error {
	if err := engine.recoverLocalSearchIntents(); err != nil {
		return err
	}
	engine.jobsMu.Lock()
	for _, job := range engine.jobs {
		if job.pendingFinal != nil {
			if err := engine.commitTerminalLocked(job); err != nil {
				engine.jobsMu.Unlock()
				return err
			}
		}
	}
	engine.jobsMu.Unlock()
	entries, err := os.ReadDir(engine.owner.root)
	if err != nil {
		return err
	}
	owners := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() && ownerPattern.MatchString(entry.Name()) {
			owners[entry.Name()] = true
		}
	}
	rows, err := engine.store.db.Query("SELECT DISTINCT COALESCE(json_extract(document,'$.execution_owner'),'') FROM runs WHERE json_extract(document,'$.status')='running'")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if id != "" {
			owners[id] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for id := range owners {
		if id == engine.owner.id {
			continue
		}
		if !ownerPattern.MatchString(id) {
			return errors.New("Invalid task owner identity")
		}
		dir := filepath.Join(engine.owner.root, id)
		lock, acquired, err := platform.TryFileLock(filepath.Join(dir, "owner.lock"))
		if errors.Is(err, os.ErrNotExist) {
			if err := engine.recoverOwner(id, ""); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !acquired {
			continue
		}
		recoverErr := engine.recoverOwner(id, dir)
		closeErr := lock.Close()
		if recoverErr == nil && closeErr == nil {
			_ = os.RemoveAll(dir)
		}
		if recoverErr != nil || closeErr != nil {
			return errors.Join(recoverErr, closeErr)
		}
	}
	if err := engine.recoverOwner("", ""); err != nil {
		return err
	}
	return engine.recoverTerminalTurns()
}

// recoverTerminalTurns repairs older cached-completion rows whose user turn was left running.
func (engine *Engine) recoverTerminalTurns() error {
	rows, err := engine.store.db.Query("SELECT DISTINCT r.document FROM conversation_turns t JOIN runs r ON r.id=json_extract(t.document,'$.job_id') WHERE json_extract(t.document,'$.status')='running' AND json_extract(r.document,'$.status') IN ('done','error','cancelled','interrupted')")
	if err != nil {
		return err
	}
	jobs := []Job{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		job, err := decodeStoredJob(raw)
		if err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, job)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := engine.store.finishConversation(&job); err != nil {
			return err
		}
	}
	return nil
}

// recoverOwner reads one abandoned owner's rows and commits each result together with its conversation turn.
func (engine *Engine) recoverOwner(ownerID, dir string) error {
	if err := engine.recoverSearchOwner(ownerID, dir); err != nil {
		return err
	}
	rows, err := engine.store.db.Query("SELECT document FROM runs WHERE json_extract(document,'$.status')='running' AND COALESCE(json_extract(document,'$.execution_owner'),'')=?", ownerID)
	if err != nil {
		return err
	}
	jobs := []Job{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		job, err := decodeStoredJob(raw)
		if err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, job)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if ownerID == "" {
			engine.jobsMu.Lock()
			active := engine.jobs[job.ID]
			live := active != nil && active.Status == "running"
			engine.jobsMu.Unlock()
			if live {
				continue
			}
		}
		job.Status = "interrupted"
		job.Error = "The app restarted. Continue from the saved conversation."
		if dir != "" {
			data, readErr := os.ReadFile(filepath.Join(dir, job.ID+".terminal.json"))
			if readErr == nil {
				intent, decodeErr := decodeStoredJob(string(data))
				if decodeErr != nil || intent.ID != job.ID || intent.ProjectID != job.ProjectID || intent.SessionID != job.SessionID || intent.ownerID != ownerID || !contains([]string{"done", "error", "cancelled"}, intent.Status) {
					return errors.New("Invalid task recovery intent")
				}
				job = intent
			} else if !errors.Is(readErr, os.ErrNotExist) {
				return readErr
			}
		}
		if err := engine.store.finishConversation(&job); err != nil {
			return err
		}
	}
	return nil
}

// finishConversationIn writes a terminal result once while preserving any already-published task document.
func finishConversationIn(connection *sql.Conn, job *Job) error {
	var raw string
	if err := connection.QueryRowContext(context.Background(), "SELECT document FROM runs WHERE id=? AND project_id=?", job.ID, job.ProjectID).Scan(&raw); err != nil {
		return err
	}
	stored, err := decodeStoredJob(raw)
	if err != nil {
		return err
	}
	if stored.Status == "running" {
		if stored.ownerID != job.ownerID {
			return errors.New("Task ownership changed; the result was not published")
		}
		if _, err := connection.ExecContext(context.Background(), "UPDATE runs SET document=? WHERE id=? AND project_id=?", storedJob(job), job.ID, job.ProjectID); err != nil {
			return err
		}
	} else {
		job = &stored
	}
	if job.SessionID == "" {
		return nil
	}
	session, err := readConversation(connection, job.ProjectID, job.SessionID)
	if err != nil {
		return err
	}
	answered, changed := false, false
	for i := range session.Turns {
		if session.Turns[i].JobID != job.ID {
			continue
		}
		if session.Turns[i].Role == "assistant" {
			answered = true
		}
		if session.Turns[i].Status != job.Status {
			session.Turns[i].Status = job.Status
			changed = true
		}
	}
	if !answered {
		session.Turns = append(session.Turns, ConversationTurn{ID: newID(), Role: "assistant", Content: job.Text, JobID: job.ID, Action: job.Action, Status: job.Status, CreatedAt: now()})
		changed = true
	}
	if !changed {
		return nil
	}
	return saveConversation(connection, &session)
}

// closeRuntime releases the process claim but preserves recovery intents that could not reach SQLite.
func (engine *Engine) closeRuntime() error {
	pending := false
	if engine.hasSearchIntents() {
		pending = true
	}
	for _, job := range engine.jobs {
		if job.pendingFinal != nil {
			pending = true
			break
		}
	}
	err := engine.owner.lock.Close()
	if !pending && err == nil {
		_ = os.RemoveAll(engine.owner.dir)
	}
	return err
}
