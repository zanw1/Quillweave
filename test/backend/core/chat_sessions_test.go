//go:build quillweave_whitebox

package core

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// TestChatSessionSearchAndArchive finds older message content and preserves archived history and drafts.
func TestChatSessionSearchAndArchive(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	old := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": project.ID, "title": "Old conversation"})
	old.Draft = "Keep this unsent draft"
	old.Turns = []ConversationTurn{{ID: newID(), Role: "user", Content: "Distinctive lantern reference 100%", Status: "done", CreatedAt: now()}}
	err := engine.store.withWriteTransaction(func(conn *sql.Conn) error { return saveConversation(conn, &old) })
	assert(t, err == nil, fmt.Sprint(err))
	for i := 0; i < 101; i++ {
		invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": project.ID, "title": fmt.Sprintf("Recent %d", i)})
	}
	first := invoke[[]Conversation](t, engine, "list_conversations", map[string]any{"project_id": project.ID})
	assert(t, len(first) == 100 && first[0].ID != old.ID, "The first bounded page did not contain recent sessions")
	found := invoke[[]Conversation](t, engine, "list_conversations", map[string]any{"project_id": project.ID, "query": "LANTERN REFERENCE 100%"})
	assert(t, len(found) == 1 && found[0].ID == old.ID, "Server search did not find literal message content beyond the first page")
	archived := invoke[Conversation](t, engine, "archive_conversation", map[string]any{"project_id": project.ID, "session_id": old.ID, "revision": old.Revision, "archived": true})
	assert(t, archived.Archived && archived.Draft == old.Draft && len(archived.Turns) == 1, "Archiving lost the conversation draft or history")
	active := invoke[[]Conversation](t, engine, "list_conversations", map[string]any{"project_id": project.ID, "query": "lantern"})
	assert(t, len(active) == 0, "An archived conversation leaked into the active search")
	archive := invoke[[]Conversation](t, engine, "list_conversations", map[string]any{"project_id": project.ID, "query": "lantern", "archived": true})
	assert(t, len(archive) == 1 && archive[0].ID == old.ID, "An archived conversation was not searchable")
	reject(t, engine, "archive_conversation", map[string]any{"project_id": project.ID, "session_id": old.ID, "revision": old.Revision, "archived": false})
	reject(t, engine, "start_ai", AIRequest{ProjectID: project.ID, SessionID: old.ID, Action: "ask", Instruction: "A new request"})
	restored := invoke[Conversation](t, engine, "archive_conversation", map[string]any{"project_id": project.ID, "session_id": old.ID, "revision": archived.Revision, "archived": false})
	assert(t, !restored.Archived && restored.Draft == old.Draft && restored.Turns[0].ID == old.Turns[0].ID, "Restore changed source history identities")
}

// TestChatSessionModelDefaultsRemainScoped validates defaults and copies them into branches without aliasing.
func TestChatSessionModelDefaultsRemainScoped(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	config := defaultProvider()
	config.Model = "session-fixture"
	config.BaseURL = "http://127.0.0.1:19876/v1"
	configureModel(t, engine, config)
	models, err := readModelSettings(engine.store.db)
	assert(t, err == nil, fmt.Sprint(err))
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": project.ID})
	selection := ChatModelSelection{ProfileID: models.DefaultProfile, OutputMode: "auto"}
	session = invoke[Conversation](t, engine, "update_conversation", map[string]any{"project_id": project.ID, "session_id": session.ID, "revision": session.Revision, "model_selection": selection})
	assert(t, session.ModelSelection != nil && session.ModelSelection.ProfileID == selection.ProfileID, "A session model selection was not saved")
	session.Turns = []ConversationTurn{{ID: newID(), Role: "user", Content: "Branch here", Status: "done", CreatedAt: now()}}
	err = engine.store.withWriteTransaction(func(conn *sql.Conn) error { return saveConversation(conn, &session) })
	assert(t, err == nil, fmt.Sprint(err))
	branch, err := engine.store.forkConversation(project.ID, session.ID, session.Turns[0].ID)
	assert(t, err == nil && branch.ModelSelection != nil && *branch.ModelSelection == selection, "The branch lost its model default")
	branch.ModelSelection.ProfileID = "changed-only-in-memory"
	assert(t, session.ModelSelection.ProfileID == selection.ProfileID, "A branch shared a mutable model selection")
	cleared := invoke[Conversation](t, engine, "update_conversation", map[string]any{"project_id": project.ID, "session_id": session.ID, "revision": session.Revision, "model_selection": nil})
	assert(t, cleared.ModelSelection == nil, "Explicit inheritance did not clear the session override")
	reject(t, engine, "update_conversation", map[string]any{"project_id": project.ID, "session_id": session.ID, "revision": cleared.Revision, "model_selection": ChatModelSelection{ProfileID: "missing", OutputMode: "auto"}})
}

// TestChatSessionArchiveRejectsRunningAndForeignSessions keeps archive transitions inside their owning story.
func TestChatSessionArchiveRejectsRunningAndForeignSessions(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": project.ID})
	session.Turns = []ConversationTurn{{ID: newID(), Role: "user", Content: "Active request", Status: "running", CreatedAt: now()}}
	err := engine.store.withWriteTransaction(func(conn *sql.Conn) error { return saveConversation(conn, &session) })
	assert(t, err == nil, fmt.Sprint(err))
	_, err = engine.store.archiveConversation(project.ID, session.ID, session.Revision, true)
	assert(t, err != nil, "An unfinished conversation was archived")
	_, err = engine.store.archiveConversation("another-project", session.ID, session.Revision, true)
	assert(t, err != nil, "Another story could archive this conversation")
	var archived bool
	err = engine.store.db.QueryRowContext(context.Background(), "SELECT COALESCE(json_extract(document,'$.archived'),0) FROM conversations WHERE id=?", session.ID).Scan(&archived)
	assert(t, err == nil && !archived, "A rejected archive changed durable state")
	reject(t, engine, "list_conversations", map[string]any{"project_id": project.ID, "archived": "yes"})
}
