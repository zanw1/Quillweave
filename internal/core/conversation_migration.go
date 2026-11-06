package core

import (
	"context"
)

// migrateLegacyConversations retains old task discussions as explicitly labelled imported histories.
func migrateLegacyConversations(db documentDB, projectID string) error {
	rows, err := db.QueryContext(context.Background(), "SELECT json_remove(document,'$.provider_context') FROM runs WHERE project_id=? ORDER BY rowid", projectID)
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
		var job Job
		if err := decodeJSON([]byte(raw), &job); err != nil {
			rows.Close()
			return err
		}
		if job.SessionID == "" && conversationAction(job.Action) {
			jobs = append(jobs, job)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	groups := map[string]*Conversation{}
	for _, job := range jobs {
		session := groups[job.ChapterID]
		if session == nil {
			session = &Conversation{ID: newID(), ProjectID: projectID, ChapterID: job.ChapterID, Title: "Imported task history", Turns: []ConversationTurn{}, CreatedAt: job.CreatedAt}
			groups[job.ChapterID] = session
		}
		status := job.Status
		if status == "running" {
			status = "interrupted"
		}
		session.Turns = append(session.Turns, ConversationTurn{ID: newID(), Role: "user", Content: job.Instruction, JobID: job.ID, Action: job.Action, Status: status, CreatedAt: job.CreatedAt}, ConversationTurn{ID: newID(), Role: "assistant", Content: job.Text, JobID: job.ID, Action: job.Action, Status: status, CreatedAt: job.CreatedAt})
		if _, err := db.ExecContext(context.Background(), "UPDATE runs SET document=json_set(document,'$.session_id',?) WHERE id=? AND project_id=?", session.ID, job.ID, projectID); err != nil {
			return err
		}
	}
	for _, session := range groups {
		if err := saveConversation(db, session); err != nil {
			return err
		}
	}
	return nil
}
