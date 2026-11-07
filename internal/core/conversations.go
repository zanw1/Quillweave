package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type Conversation struct {
	ID                   string                  `json:"id"`
	ProjectID            string                  `json:"project_id"`
	ChapterID            string                  `json:"chapter_id"`
	OriginChapterID      string                  `json:"origin_chapter_id,omitempty"`
	Title                string                  `json:"title"`
	ParentID             string                  `json:"parent_id"`
	ForkTurnID           string                  `json:"fork_turn_id"`
	Draft                string                  `json:"draft"`
	Summary              string                  `json:"summary"`
	CompactedSummary     string                  `json:"compacted_summary,omitempty"`
	CompactedUpToOrdinal int                     `json:"compacted_up_to_ordinal,omitempty"`
	Checkpoint           *ConversationCheckpoint `json:"checkpoint,omitempty"`
	BibleBaseline        string                  `json:"bible_baseline,omitempty"`
	LastBibleHash        string                  `json:"last_bible_hash,omitempty"`
	Revision             uint64                  `json:"revision"`
	CreatedAt            string                  `json:"created_at"`
	UpdatedAt            string                  `json:"updated_at"`
	Turns                []ConversationTurn      `json:"turns"`
}

type ConversationTurn struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	JobID     string `json:"job_id"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	RequestID string `json:"request_id"`
	CreatedAt string `json:"created_at"`
}

// readConversation checks project ownership before returning ordered durable turns.
func readConversation(db documentDB, projectID, id string) (Conversation, error) {
	var raw string
	var session Conversation
	if err := db.QueryRowContext(context.Background(), "SELECT document FROM conversations WHERE id=? AND project_id=?", id, projectID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return session, errors.New("Conversation not found")
		}
		return session, err
	}
	if err := decodeJSON([]byte(raw), &session); err != nil {
		return session, err
	}
	session.Turns = []ConversationTurn{}
	rows, err := db.QueryContext(context.Background(), "SELECT document FROM conversation_turns WHERE session_id=? ORDER BY ordinal", id)
	if err != nil {
		return session, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return session, err
		}
		var turn ConversationTurn
		if err := decodeJSON([]byte(raw), &turn); err != nil {
			return session, err
		}
		session.Turns = append(session.Turns, turn)
	}
	return session, rows.Err()
}

// saveConversation persists complete histories for creation, branching and migration within a write transaction.
func saveConversation(db documentDB, session *Conversation) error {
	if err := saveConversationMetadata(db, session); err != nil {
		return err
	}
	for i := range session.Turns {
		if err := saveConversationTurn(db, session, i); err != nil {
			return err
		}
	}
	return nil
}

// saveConversationMetadata updates the revision, draft and checkpoint without touching message rows.
func saveConversationMetadata(db documentDB, session *Conversation) error {
	if session.Checkpoint != nil {
		session.Summary = session.Checkpoint.Summary
	}
	session.Revision++
	session.UpdatedAt = now()
	metadata := *session
	metadata.Turns = []ConversationTurn{}
	_, err := db.ExecContext(context.Background(), "INSERT INTO conversations(id,project_id,chapter_id,document) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document", session.ID, session.ProjectID, session.ChapterID, jsonText(metadata))
	return err
}

// saveConversationTurn writes one known ordinal and refreshes its index only when its document changes.
func saveConversationTurn(db documentDB, session *Conversation, ordinal int) error {
	turn := session.Turns[ordinal]
	result, err := db.ExecContext(context.Background(), "INSERT INTO conversation_turns(id,session_id,ordinal,document) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document WHERE document!=excluded.document", turn.ID, session.ID, ordinal, jsonText(turn))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed > 0 {
		return indexConversationTurn(db, session, turn)
	}
	return nil
}

// indexConversationTurn retains searchable original discussion without allowing sibling-branch leakage.
func indexConversationTurn(db documentDB, session *Conversation, turn ConversationTurn) error {
	key := "session:" + session.ID + ":" + turn.ID
	if _, err := db.ExecContext(context.Background(), "DELETE FROM story_fts WHERE project_id=? AND part_id=?", session.ProjectID, key); err != nil {
		return err
	}
	if turn.Status != "done" {
		return nil
	}
	for i, text := range chunks(turn.Content, 900, 100) {
		if _, err := db.ExecContext(context.Background(), "INSERT INTO story_fts(project_id,part_id,item_id,title,kind,chapter,content) VALUES(?,?,?,?,?,?,?)", session.ProjectID, key, fmt.Sprintf("T:%s:%s:%d", session.ID, turn.ID, i+1), session.Title+" · "+turn.Role, "discussion", 0, text); err != nil {
			return err
		}
	}
	return nil
}

// Conversations returns recent sessions without sharing any model-private continuation state.
func (store *Store) Conversations(projectID, chapterID string, offsets ...int) ([]Conversation, error) {
	offset := 0
	if len(offsets) > 0 {
		offset = offsets[0]
	}
	rows, err := store.db.Query("SELECT id FROM conversations WHERE project_id=? AND chapter_id=? ORDER BY rowid DESC LIMIT 100 OFFSET ?", projectID, chapterID, offset)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	result := []Conversation{}
	for _, id := range ids {
		session, err := readConversation(store.db, projectID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, nil
}

// createConversation starts a new conversation in the selected story and chapter scope.
func (store *Store) createConversation(projectID, chapterID, title string) (Conversation, error) {
	session := Conversation{ID: newID(), ProjectID: projectID, ChapterID: chapterID, Title: strings.TrimSpace(title), Turns: []ConversationTurn{}, CreatedAt: now()}
	if session.Title == "" {
		session.Title = "New conversation"
	}
	if charCount(session.Title) > 120 {
		return session, errors.New("Conversation title is too long")
	}
	err := store.withWriteTransaction(func(conn *sql.Conn) error {
		p, err := readProject(conn, projectID)
		if err != nil {
			return err
		}
		if chapterID != "" {
			if _, _, ok := p.SourceText(chapterID); !ok {
				return errors.New("Chapter or source not found")
			}
		}
		return saveConversation(conn, &session)
	})
	return session, err
}

// updateConversation rejects stale renames or drafts rather than overwriting another window.
func (store *Store) updateConversation(projectID, id string, revision uint64, title, draft *string) (Conversation, error) {
	var session Conversation
	err := store.withWriteTransaction(func(conn *sql.Conn) error {
		var err error
		session, err = readConversation(conn, projectID, id)
		if err != nil {
			return err
		}
		if session.Revision != revision {
			return errors.New("Conversation changed. Reload before saving its draft")
		}
		if title != nil {
			if strings.TrimSpace(*title) == "" || charCount(*title) > 120 {
				return errors.New("Enter a conversation title within 120 characters")
			}
			session.Title = strings.TrimSpace(*title)
		}
		if draft != nil {
			if charCount(*draft) > 8000 {
				return errors.New("Keep each writing request within 8,000 characters")
			}
			session.Draft = *draft
		}
		return saveConversationMetadata(conn, &session)
	})
	return session, err
}

// forkConversation copies only the prefix before a selected user turn and restores its prompt.
func (store *Store) forkConversation(projectID, id, turnID string) (Conversation, error) {
	var result Conversation
	err := store.withWriteTransaction(func(conn *sql.Conn) error {
		session, err := readConversation(conn, projectID, id)
		if err != nil {
			return err
		}
		position := -1
		for i, turn := range session.Turns {
			if turn.ID == turnID && turn.Role == "user" {
				position = i
				break
			}
		}
		if position < 0 {
			return errors.New("Select a user message to rewind or edit")
		}
		result = Conversation{ID: newID(), ProjectID: projectID, ChapterID: session.ChapterID, OriginChapterID: session.OriginChapterID, Title: session.Title + " · branch", ParentID: session.ID, ForkTurnID: turnID, Draft: session.Turns[position].Content, CreatedAt: now(), Turns: []ConversationTurn{}}
		for _, turn := range session.Turns[:position] {
			turn.ID = newID()
			turn.RequestID = ""
			result.Turns = append(result.Turns, turn)
		}
		return saveConversation(conn, &result)
	})
	return result, err
}

// prepareConversation supplies server-owned history and detects retries of an accepted request.
func (store *Store) prepareConversation(request AIRequest) (AIRequest, string, error) {
	if request.SessionID == "" {
		return request, "", nil
	}
	session, err := readConversation(store.db, request.ProjectID, request.SessionID)
	if err != nil {
		return request, "", err
	}
	scope := request.ChapterID
	if scope == "" {
		scope = request.SourceID
	}
	if session.ChapterID != "" && session.ChapterID != scope {
		return request, "", errors.New("Conversation belongs to another chapter")
	}
	request.History = []Message{}
	request.conversation = &session
	for _, turn := range session.Turns {
		if request.RequestID != "" && turn.Role == "user" && turn.RequestID == request.RequestID {
			return request, turn.JobID, nil
		}
		if turn.Status == "running" {
			return request, "", errors.New("This conversation has an unfinished request")
		}
		if turn.Status != "done" {
			continue
		}
		request.History = append(request.History, Message{Role: turn.Role, Content: turn.Content, SourceJobID: turn.JobID})
	}
	return request, "", nil
}

// beginDurableJob resolves retries or commits one user turn and job under the same write lock.
func (store *Store) beginDurableJob(request AIRequest, job *Job) (string, error) {
	prior := ""
	err := store.withWriteTransaction(func(conn *sql.Conn) error {
		var session Conversation
		if request.SessionID != "" {
			var err error
			session, err = readConversation(conn, request.ProjectID, request.SessionID)
			if err != nil {
				return err
			}
			for _, turn := range session.Turns {
				if request.RequestID != "" && turn.Role == "user" && turn.RequestID == request.RequestID {
					prior = turn.JobID
					return nil
				}
			}
		}
		var revision uint64
		if err := conn.QueryRowContext(context.Background(), "SELECT revision FROM projects WHERE id=?", job.ProjectID).Scan(&revision); err != nil {
			return err
		}
		if revision != job.BaseRevision {
			return errors.New("The project changed. Refresh before starting this task")
		}
		var active int
		if err := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM runs WHERE project_id=? AND json_extract(document,'$.status')='running'", job.ProjectID).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return errors.New("This project already has a running task. Wait or cancel it")
		}
		if request.SessionID != "" {
			for _, turn := range session.Turns {
				if turn.Status == "running" {
					return errors.New("This conversation has an unfinished request")
				}
			}
			session.Turns = append(session.Turns, ConversationTurn{ID: newID(), Role: "user", Content: request.Instruction, JobID: job.ID, Action: request.Action, Status: "running", RequestID: request.RequestID, CreatedAt: now()})
			session.Draft = ""
			if session.BibleBaseline == "" && job.Prompt.BibleCurrent != "" {
				session.BibleBaseline = job.Prompt.BibleCurrent
				session.LastBibleHash = hashText(job.Prompt.BibleCurrent)
			}
			if len(session.Turns) == 1 && session.Title == "New conversation" && strings.TrimSpace(request.Instruction) != "" {
				session.Title = string([]rune(request.Instruction)[:min(35, charCount(request.Instruction))])
			}
			if err := saveConversationMetadata(conn, &session); err != nil {
				return err
			}
			if err := saveConversationTurn(conn, &session, len(session.Turns)-1); err != nil {
				return err
			}
		}
		initial := job.snapshot()
		initial.Status = "running"
		_, err := conn.ExecContext(context.Background(), "INSERT INTO runs(id,project_id,document) VALUES(?,?,?)", job.ID, job.ProjectID, storedJob(&initial))
		return err
	})
	return prior, err
}

// finishConversation publishes the result and marks interruptions without treating partial prose as facts.
func (store *Store) finishConversation(job *Job) error {
	return store.withWriteTransaction(func(conn *sql.Conn) error { return finishConversationIn(conn, job) })
}

// importConversations rekeys archive sessions and turns so importing a backup cannot overwrite another work.
func importConversations(db documentDB, project *Project) error {
	for _, session := range project.Conversations {
		if err := restoreArchivedSession(db, project, session); err != nil {
			return err
		}
	}
	return nil
}

const maxHistoryMessages = 24

// compactHistory retains bounded older context and complete recent turns without unbounded prompt growth.
func compactHistory(history []Message, budget int) ([]Message, string) {
	compacted, summary, _ := compactHistoryBlockwise(history, budget, "", 0)
	return compacted, summary
}
