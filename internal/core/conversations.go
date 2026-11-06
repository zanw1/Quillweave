package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type Conversation struct {
	ID         string             `json:"id"`
	ProjectID  string             `json:"project_id"`
	ChapterID  string             `json:"chapter_id"`
	Title      string             `json:"title"`
	ParentID   string             `json:"parent_id"`
	ForkTurnID string             `json:"fork_turn_id"`
	Draft      string             `json:"draft"`
	Summary    string             `json:"summary"`
	Revision   uint64             `json:"revision"`
	CreatedAt  string             `json:"created_at"`
	UpdatedAt  string             `json:"updated_at"`
	Turns      []ConversationTurn `json:"turns"`
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

// saveConversation persists metadata and only changed turns in an existing write transaction.
func saveConversation(db documentDB, session *Conversation) error {
	history := []Message{}
	for _, turn := range session.Turns {
		if turn.Status == "done" {
			history = append(history, Message{Role: turn.Role, Content: turn.Content})
		}
	}
	_, session.Summary = compactHistory(history, 4000)
	session.Revision++
	session.UpdatedAt = now()
	metadata := *session
	metadata.Turns = []ConversationTurn{}
	if _, err := db.ExecContext(context.Background(), "INSERT INTO conversations(id,project_id,chapter_id,document) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document", session.ID, session.ProjectID, session.ChapterID, jsonText(metadata)); err != nil {
		return err
	}
	for i, turn := range session.Turns {
		result, err := db.ExecContext(context.Background(), "INSERT INTO conversation_turns(id,session_id,ordinal,document) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document WHERE document!=excluded.document", turn.ID, session.ID, i, jsonText(turn))
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed > 0 {
			if err := indexConversationTurn(db, session, turn); err != nil {
				return err
			}
		}
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
func (store *Store) Conversations(projectID, chapterID string) ([]Conversation, error) {
	rows, err := store.db.Query("SELECT id FROM conversations WHERE project_id=? AND chapter_id=? ORDER BY rowid DESC LIMIT 100", projectID, chapterID)
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
		return saveConversation(conn, &session)
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
		result = Conversation{ID: newID(), ProjectID: projectID, ChapterID: session.ChapterID, Title: session.Title + " · branch", ParentID: session.ID, ForkTurnID: turnID, Draft: session.Turns[position].Content, CreatedAt: now(), Turns: []ConversationTurn{}}
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
	if session.ChapterID != scope {
		return request, "", errors.New("Conversation belongs to another chapter")
	}
	request.History = []Message{}
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

// beginDurableJob commits a user turn and its resumable job in one write transaction.
func (store *Store) beginDurableJob(request AIRequest, job *Job) error {
	return store.withWriteTransaction(func(conn *sql.Conn) error {
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
			session, err := readConversation(conn, request.ProjectID, request.SessionID)
			if err != nil {
				return err
			}
			for _, turn := range session.Turns {
				if turn.Status == "running" {
					return errors.New("This conversation has an unfinished request")
				}
			}
			session.Turns = append(session.Turns, ConversationTurn{ID: newID(), Role: "user", Content: request.Instruction, JobID: job.ID, Action: request.Action, Status: "running", RequestID: request.RequestID, CreatedAt: now()})
			session.Draft = ""
			if len(session.Turns) == 1 && session.Title == "New conversation" && strings.TrimSpace(request.Instruction) != "" {
				session.Title = string([]rune(request.Instruction)[:min(35, charCount(request.Instruction))])
			}
			if err := saveConversation(conn, &session); err != nil {
				return err
			}
		}
		initial := job.snapshot()
		initial.Status = "running"
		_, err := conn.ExecContext(context.Background(), "INSERT INTO runs(id,project_id,document) VALUES(?,?,?)", job.ID, job.ProjectID, storedJob(&initial))
		return err
	})
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
	if len(history) <= maxHistoryMessages && messageChars(history) <= budget {
		return history, ""
	}
	tail := []Message{}
	used := 0
	cut := len(history)
	for cut > 0 && len(tail) < maxHistoryMessages-1 {
		item := history[cut-1]
		if used+charCount(item.Content) > budget*2/3 {
			break
		}
		tail = append([]Message{item}, tail...)
		used += charCount(item.Content)
		cut--
	}
	snippets := []string{}
	remaining := max(0, budget-used-100)
	for i := 0; i < cut && remaining > 0; i++ {
		text := []rune(history[i].Content)
		size := min(len(text), min(180, remaining))
		snippets = append(snippets, fmt.Sprintf("%s: %s", history[i].Role, string(text[:size])))
		remaining -= size + 12
	}
	summary := "Earlier discussion excerpts (incomplete; accepted decisions are recorded separately):\n" + strings.Join(snippets, "\n")
	if charCount(summary) > budget-used {
		summary = string([]rune(summary)[:max(0, budget-used)])
	}
	if summary != "" {
		tail = append([]Message{{Role: "user", Content: summary}}, tail...)
	}
	return tail, summary
}
