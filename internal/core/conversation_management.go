package core

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// queryConversations searches all durable turns before applying the bounded result page.
func (store *Store) queryConversations(projectID, chapterID, query string, archived bool, offset int) ([]Conversation, error) {
	result := []Conversation{}
	query = strings.TrimSpace(query)
	err := store.withReadSnapshot(func(db documentDB) error {
		statement := `SELECT c.id FROM conversations c
			WHERE c.project_id=? AND c.chapter_id=?
			AND COALESCE(json_extract(c.document,'$.archived'),0)=?`
		arguments := []any{projectID, chapterID, archived}
		if query != "" {
			statement += ` AND (instr(lower(json_extract(c.document,'$.title')),lower(?))>0
				OR EXISTS (SELECT 1 FROM conversation_turns t WHERE t.session_id=c.id
				AND instr(lower(json_extract(t.document,'$.content')),lower(?))>0))`
			arguments = append(arguments, query, query)
		}
		statement += " ORDER BY c.rowid DESC LIMIT 100 OFFSET ?"
		arguments = append(arguments, offset)
		rows, err := db.QueryContext(context.Background(), statement, arguments...)
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
		rowError := rows.Err()
		rows.Close()
		if rowError != nil {
			return rowError
		}
		for _, id := range ids {
			session, err := readConversation(db, projectID, id)
			if err != nil {
				return err
			}
			result = append(result, session)
		}
		return nil
	})
	return result, err
}

// archiveConversation moves an idle conversation between active and archived lists atomically.
func (store *Store) archiveConversation(projectID, id string, revision uint64, archived bool) (Conversation, error) {
	var session Conversation
	err := store.withWriteTransaction(func(conn *sql.Conn) error {
		var err error
		session, err = readConversation(conn, projectID, id)
		if err != nil {
			return err
		}
		if session.Revision != revision {
			return errors.New("Conversation changed. Reload before archiving")
		}
		for _, turn := range session.Turns {
			if turn.Status == "running" {
				return errors.New("Wait for this conversation's running task before archiving")
			}
		}
		if session.Archived == archived {
			return nil
		}
		session.Archived = archived
		return saveConversationMetadata(conn, &session)
	})
	return session, err
}
