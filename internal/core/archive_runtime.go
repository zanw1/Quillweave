package core

import (
	"context"
	"errors"
)

// rekeyArchive isolates imported sessions, jobs and research records while retaining internal provenance links.
func rekeyArchive(project *Project) {
	sessions, turns, jobs := map[string]string{}, map[string]string{}, map[string]string{}
	for _, s := range project.Conversations {
		sessions[s.ID] = newID()
		for _, t := range s.Turns {
			turns[t.ID] = newID()
		}
	}
	for _, job := range project.ArchiveJobs {
		jobs[job.ID] = newID()
	}
	for i := range project.Conversations {
		s := &project.Conversations[i]
		s.ID = sessions[s.ID]
		s.ProjectID = project.ID
		s.ParentID = sessions[s.ParentID]
		s.ForkTurnID = turns[s.ForkTurnID]
		s.Revision = 0
		for j := range s.Turns {
			t := &s.Turns[j]
			t.ID = turns[t.ID]
			t.JobID = jobs[t.JobID]
			t.RequestID = ""
			if t.Status == "running" {
				t.Status = "interrupted"
			}
		}
	}
	for i := range project.ArchiveJobs {
		job := &project.ArchiveJobs[i]
		job.ID = jobs[job.ID]
		job.ProjectID = project.ID
		job.SessionID = sessions[job.SessionID]
		if job.Status == "running" {
			job.Status = "interrupted"
		}
		for j := range job.Prompt.Messages {
			job.Prompt.Messages[j].SourceJobID = jobs[job.Prompt.Messages[j].SourceJobID]
		}
	}
	for i := range project.Studio.Memories {
		project.Studio.Memories[i].SessionID = sessions[project.Studio.Memories[i].SessionID]
	}
	for i := range project.Studio.Decisions {
		project.Studio.Decisions[i].SessionID = sessions[project.Studio.Decisions[i].SessionID]
	}
	for i := range project.Experiments {
		project.Experiments[i].ID = newID()
	}
}

// importArchiveRuntime restores public task and experiment records without importing provider-private state.
func importArchiveRuntime(db documentDB, project *Project) error {
	if len(project.ArchiveJobs) > 20000 || len(project.Experiments) > 200 {
		return errors.New("Too many runtime records in the backup")
	}
	for _, job := range project.ArchiveJobs {
		if job.ID == "" || job.ProjectID != project.ID || !contains([]string{"done", "error", "cancelled", "interrupted"}, job.Status) {
			return errors.New("Invalid archived task")
		}
		if _, err := db.ExecContext(context.Background(), "INSERT INTO runs(id,project_id,document) VALUES(?,?,?)", job.ID, project.ID, jsonText(job)); err != nil {
			return err
		}
	}
	for _, experiment := range project.Experiments {
		if err := validateExperiment(experiment); err != nil {
			return err
		}
		if _, err := db.ExecContext(context.Background(), "INSERT INTO experiments(id,project_id,document) VALUES(?,?,?)", experiment.ID, project.ID, jsonText(experiment)); err != nil {
			return err
		}
	}
	return nil
}

// restoreArchivedSession preserves validated rekeyed identities inside the import transaction.
func restoreArchivedSession(db documentDB, project *Project, session Conversation) error {
	if session.ID == "" || session.ProjectID != project.ID || len(session.Turns) > 20000 || charCount(jsonText(session)) > 10_000_000 {
		return errors.New("Invalid or oversized archived conversation")
	}
	for _, turn := range session.Turns {
		if turn.ID == "" || !contains([]string{"user", "assistant"}, turn.Role) {
			return errors.New("Invalid conversation role")
		}
	}
	return saveConversation(db, &session)
}
