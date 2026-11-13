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
	if err == nil {
		normalizeLegacyProposalStates(&saved.Job)
	}
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
	current.Text = desired.Text
	current.ChangeSets = append([]ChangeSet{}, desired.ChangeSets...)
	current.Proposals = append([]StudioProposal{}, desired.Proposals...)
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
			live, liveErr := engine.ownerHasLiveWork(id)
			if liveErr != nil {
				return liveErr
			}
			// Windows can return access denied while a peer's lock is still DELETE_PENDING.
			// An owner with no queued or running work has nothing to publish.
			if !live && platform.RetryableFileError(err) {
				continue
			}
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

// ownerHasLiveWork reports unfinished jobs or searches still charged to one runtime owner.
func (engine *Engine) ownerHasLiveWork(ownerID string) (bool, error) {
	var searches, jobs int
	if err := engine.store.db.QueryRow("SELECT COUNT(*) FROM search_runs WHERE execution_owner=? AND status IN ('queued','running')", ownerID).Scan(&searches); err != nil {
		return false, err
	}
	if err := engine.store.db.QueryRow("SELECT COUNT(*) FROM runs WHERE json_extract(document,'$.status')='running' AND COALESCE(json_extract(document,'$.execution_owner'),'')=?", ownerID).Scan(&jobs); err != nil {
		return false, err
	}
	return searches+jobs > 0, nil
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
		if job.Status == "done" {
			job.Text = stripModelToolJSON(job.Text)
		}
		if len(job.ChangeSets) > 0 {
			staged := append([]ChangeSet{}, job.ChangeSets...)
			pending := []ChangeSet{}
			for i := range staged {
				var existing string
				err := connection.QueryRowContext(context.Background(), "SELECT document FROM project_parts WHERE project_id=? AND kind='changeset' AND id=?", job.ProjectID, staged[i].ID).Scan(&existing)
				if err == nil {
					var published ChangeSet
					if err := decodeJSON([]byte(existing), &published); err != nil {
						return err
					}
					if published.JobID != job.ID {
						return errors.New("This changeset already exists. Reload its saved state")
					}
					staged[i] = published
					continue
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				staged[i].CommitPolicy, staged[i].Status = "review", ChangeSetStaged
				pending = append(pending, staged[i])
			}
			if err := persistStagedChangeSetsInDB(connection, job.ProjectID, pending); err != nil {
				return err
			}
			job.ChangeSets = staged
		}
		if err := commitAutomaticOutputIn(connection, job); err != nil {
			return err
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
	if job.Status == "done" && job.Prompt.BibleCurrent != "" {
		session.LastBibleHash = hashText(job.Prompt.BibleCurrent)
	}
	answered := false
	changedTurns := []int{}
	for i := range session.Turns {
		if session.Turns[i].JobID != job.ID {
			continue
		}
		if session.Turns[i].Role == "assistant" {
			answered = true
		}
		if session.Turns[i].Status != job.Status {
			session.Turns[i].Status = job.Status
			changedTurns = append(changedTurns, i)
		}
	}
	if !answered {
		session.Turns = append(session.Turns, ConversationTurn{ID: newID(), Role: "assistant", Content: job.Text, JobID: job.ID, Action: job.Action, Status: job.Status, CreatedAt: now()})
		changedTurns = append(changedTurns, len(session.Turns)-1)
	}
	if len(changedTurns) == 0 {
		return nil
	}
	if err := saveConversationMetadata(connection, &session); err != nil {
		return err
	}
	for _, ordinal := range changedTurns {
		if err := saveConversationTurn(connection, &session, ordinal); err != nil {
			return err
		}
	}
	return nil
}

// automaticRecordChange selects records that can use the ordinary automatic-save path.
// Prose stays with saveAutomaticChapter, which checks the exact reviewed candidate.
func automaticRecordChange(cs ChangeSet) bool {
	if cs.Status != ChangeSetStaged {
		return false
	}
	for _, op := range cs.Operations {
		if op.TargetKind == TargetChapter {
			return false
		}
	}
	return true
}

// commitAutomaticOutputIn publishes authorized records and their receipts together.
// Conflicting batches and separately reviewed prose remain inspectable candidates.
func commitAutomaticOutputIn(db documentDB, job *Job) error {
	if !contains([]string{"running", "done"}, job.Status) || job.CommitPolicy != "auto" || len(job.ChangeSets) == 0 {
		return nil
	}
	project, err := readProject(db, job.ProjectID)
	if err != nil {
		return err
	}
	project.requestLanguage = job.Language
	staged := []int{}
	for i, cs := range job.ChangeSets {
		if automaticRecordChange(cs) {
			staged = append(staged, i)
		}
	}
	if len(staged) == 0 {
		if job.Writing != nil && !job.Writing.Saved {
			job.Progress = writingResultSummary(&project, job.Writing)
			return nil
		}
		for _, cs := range job.ChangeSets {
			if cs.Status == ChangeSetApplied {
				job.Progress = localText(&project, "Output saved with an undoable receipt.", "创作结果已保存，可在回执中撤销。")
				break
			}
		}
		return nil
	}
	// All candidates from the same task share one revision boundary. Build them on
	// a copy before any project write, so one invalid operation cannot partly save.
	updated := cloneProject(project)
	changes := append([]ChangeSet{}, job.ChangeSets...)
	var conflict error
	for _, i := range staged {
		cs := &changes[i]
		cs.Operations = append([]ChangeOperation{}, cs.Operations...)
		if cs.BaseRevision != project.Revision {
			conflict = errors.New("The story changed after generation")
			break
		}
		if hasChapterDeletion(cs.Operations) {
			conflict = errors.New("Chapter deletion requires its dedicated review")
			break
		}
		cs.CommitPolicy = "auto"
		cs.PreviewRevision = project.Revision
		if err := applyChangeSetToProject(&updated, cs); err != nil {
			conflict = err
			break
		}
		cs.AppliedRevision = project.Revision + 1
		cs.ReviewToken = changeSetReviewToken(*cs)
		upsertChangeSetInProject(&updated, *cs)
	}
	if conflict != nil {
		job.Progress = localText(&project, "Output retained for review; automatic save was not applied: ", "结果已保留供检查，自动保存未执行：") + conflict.Error()
		return nil
	}
	if err := saveProject(db, &updated, project.Revision); err != nil {
		return err
	}
	for _, i := range staged {
		cs := changes[i]
		if err := writeChangeSetRecord(db, project.ID, cs); err != nil {
			return err
		}
		for n := range job.Proposals {
			for _, op := range cs.Operations {
				if op.ProposalID == job.Proposals[n].ID {
					job.Proposals[n].Status = "accepted"
					if op.TargetKind == TargetMemory {
						if index := memoryIndex(&updated, op.TargetID); index >= 0 {
							memory := updated.Studio.Memories[index]
							job.Proposals[n].Memory = &memory
						}
					}
				}
			}
		}
	}
	job.ChangeSets = changes
	job.Progress = localText(&project, "Output saved with an undoable receipt.", "创作结果已保存，可在回执中撤销。")
	return nil
}

// commitAutomaticStage makes explicitly authorized records available to later tools
// in this task, committing the records, task snapshot and undoable receipts together.
func (engine *Engine) commitAutomaticStage(jobID string, project *Project) (string, error) {
	engine.jobsMu.Lock()
	automatic := engine.jobs[jobID] != nil && engine.jobs[jobID].CommitPolicy == "auto"
	engine.jobsMu.Unlock()
	if !automatic {
		return "", nil
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	active := engine.jobs[jobID]
	if active == nil || active.CommitPolicy != "auto" {
		return "", nil
	}
	if project == nil || project.ID != active.ProjectID {
		return "", errors.New("The automatic stage belongs to another project")
	}
	if active.Status != "running" || active.ctx.Err() != nil {
		return "", errors.New("Task cancelled")
	}
	copy := active.snapshot()
	var updated Project
	applied := false
	var stageFailure error
	err := engine.store.withWriteTransaction(func(db *sql.Conn) error {
		var raw string
		if err := db.QueryRowContext(context.Background(), "SELECT document FROM runs WHERE id=? AND project_id=?", copy.ID, copy.ProjectID).Scan(&raw); err != nil {
			return err
		}
		stored, err := decodeStoredJob(raw)
		if err != nil || stored.Status != "running" || stored.ownerID != copy.ownerID {
			return errors.New("Task ownership changed before the automatic stage could save")
		}
		pending := []ChangeSet{}
		for i, cs := range copy.ChangeSets {
			if cs.Status != ChangeSetStaged {
				continue
			}
			var existing string
			err := db.QueryRowContext(context.Background(), "SELECT document FROM project_parts WHERE project_id=? AND kind='changeset' AND id=?", copy.ProjectID, cs.ID).Scan(&existing)
			if errors.Is(err, sql.ErrNoRows) {
				pending = append(pending, cs)
				continue
			}
			if err != nil {
				return err
			}
			var published ChangeSet
			if decodeJSON([]byte(existing), &published) != nil || published.JobID != copy.ID {
				return errors.New("The automatic stage receipt belongs to another task")
			}
			if published.Status == ChangeSetApplied {
				copy.ChangeSets[i] = published
			} else if jsonText(published) != jsonText(cs) {
				return errors.New("The candidate was edited separately. Review the current version before saving")
			}
		}
		if err := persistStagedChangeSetsInDB(db, copy.ProjectID, pending); err != nil {
			return err
		}
		if err := commitAutomaticOutputIn(db, &copy); err != nil {
			return err
		}
		updated, err = readProject(db, copy.ProjectID)
		if err != nil {
			return err
		}
		applied = true
		for _, cs := range copy.ChangeSets {
			if automaticRecordChange(cs) {
				applied = false
			}
		}
		if applied {
			copy.BaseRevision = updated.Revision
		} else {
			stageFailure = errors.New(copy.Progress)
			for i := range copy.ChangeSets {
				cs := &copy.ChangeSets[i]
				if !automaticRecordChange(*cs) {
					continue
				}
				// Keep the unsuccessful attempt inspectable, but do not reapply it
				// together with the model's corrected operation on its next call.
				cs.Status, cs.UpdatedAt = ChangeSetConflicted, now()
				cs.Summary += "\n" + copy.Progress
				if err := writeChangeSetRecord(db, copy.ProjectID, *cs); err != nil {
					return err
				}
			}
		}
		_, err = db.ExecContext(context.Background(), "UPDATE runs SET document=? WHERE id=? AND project_id=?", storedJob(&copy), copy.ID, copy.ProjectID)
		return err
	})
	if err != nil {
		return "", err
	}
	active.Proposals, active.ChangeSets, active.BaseRevision, active.Progress = copy.Proposals, copy.ChangeSets, copy.BaseRevision, copy.Progress
	if !applied {
		return "", stageFailure
	}
	updated.requestLanguage, updated.taskLanguage = project.requestLanguage, project.taskLanguage
	*project = updated
	receipts := []map[string]any{}
	for _, cs := range copy.ChangeSets {
		targets := []map[string]any{}
		for _, op := range cs.Operations {
			targets = append(targets, map[string]any{"kind": op.TargetKind, "id": op.TargetID, "title": op.Title})
		}
		receipts = append(receipts, map[string]any{"id": cs.ID, "status": cs.Status, "targets": targets})
	}
	status, next := "saved", "Later tools may now read these saved records. Receipts support undo."
	if len(copy.ChangeSets) > 0 && copy.ChangeSets[len(copy.ChangeSets)-1].Status != ChangeSetApplied {
		status, next = "candidate", "The prose candidate is retained for review and has not changed the chapter. Use writer for source-checked automatic chapter production."
	}
	return jsonText(map[string]any{"status": status, "revision": updated.Revision, "receipts": receipts, "next": next}), nil
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
