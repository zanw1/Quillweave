//go:build quillweave_whitebox

package core

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRuntimeArchiveRetainsReviewableProposalsAndBranches verifies complete backup round trips with fresh ownership.
func TestRuntimeArchiveRetainsReviewableProposalsAndBranches(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	server := staticModel(t, streamPayload(`{"text":"Proposal prepared","proposals":[{"kind":"decision","title":"A secret","content":"The keeper protects her sister."}]}`), 0)
	config := defaultProvider()
	config.Model = "fixture"
	config.BaseURL = server.URL
	config.APIKey = "fixture-secret-not-for-backup"
	configureModel(t, engine, config)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
	done := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "architecture", "instruction": "Discuss a secret"}))
	assert(t, done.Status == "done", done.Error)
	session = invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	invoke[Conversation](t, engine, "fork_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID, "turn_id": session.Turns[0].ID})
	archive := invoke[map[string]string](t, engine, "export", map[string]any{"project_id": p.ID, "format": "archive"})
	assert(t, !strings.Contains(archive["content"], "fixture-secret-not-for-backup") && !strings.Contains(archive["content"], "provider_context"), "Backup leaked credentials or private provider state")
	restored := invoke[Project](t, engine, "import_archive", map[string]any{"content": archive["content"]})
	assert(t, restored.ID != p.ID, "Backup import reused project ownership")
	sessions := invoke[[]Conversation](t, engine, "list_conversations", map[string]any{"project_id": restored.ID, "chapter_id": ""})
	assert(t, len(sessions) == 2, "Conversation branch was omitted from the backup")
	runs := invoke[[]Job](t, engine, "get_runs", map[string]any{"project_id": restored.ID})
	assert(t, len(runs) == 1 && runs[0].ID != done.ID && runs[0].SessionID != session.ID && len(runs[0].Proposals) == 1, "Pending proposals or session links were lost during rekeying")
	accepted := acceptProposalAfterPreview(t, engine, map[string]any{"project_id": restored.ID, "revision": restored.Revision, "job_id": runs[0].ID, "proposal_id": runs[0].Proposals[0].ID, "decision": "accepted"})
	assert(t, len(accepted.Project.Studio.Decisions) == 1, "Restored pending proposal could not be reviewed")
	original := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, len(original.Studio.Decisions) == 0, "Review of an imported proposal changed the original work")
}

// TestInterruptedProseCanContinueWithoutChangingTheDraft proves recovery is useful and source-version protected.
func TestInterruptedProseCanContinueWithoutChangingTheDraft(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	old := Job{ID: newID(), ProjectID: p.ID, ChapterID: p.Chapters[0].ID, Action: "continue", Instruction: "Continue", Status: "interrupted", Text: "A recovered opening. ", Prompt: Prompt{SourceID: p.Chapters[0].ID, SourceHash: hashText(p.Chapters[0].Body), Messages: []Message{}, Context: []ContextItem{}}, Steps: []AgentStep{}, Proposals: []StudioProposal{}}
	if err := engine.store.SaveRun(old.ID, p.ID, storedJob(&old)); err != nil {
		t.Fatal(err)
	}
	server, _ := sequenceModel(t, []string{writingCompositionFixture(), "The remaining continuation.", `{"summary":"The recovered scene is consistent.","issues":[],"memories":[]}`})
	config := defaultProvider()
	config.Model = "fixture"
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	done := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Continue", "resume_job_id": old.ID}))
	assert(t, done.Status == "done" && done.Text == old.Text+"The remaining continuation.", done.Error)
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, current.Chapters[0].Body == p.Chapters[0].Body, "Recovery applied prose without author review")
	chapter := current.Chapters[0]
	current = invoke[Project](t, engine, "save_chapter", map[string]any{"project_id": p.ID, "revision": current.Revision, "chapter_id": chapter.ID, "title": chapter.Title, "body": chapter.Body + " An author edit.", "synopsis": chapter.Synopsis, "status": "draft"})
	reject(t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": chapter.ID, "action": "continue", "instruction": "Continue", "resume_job_id": old.ID})
}

// TestStreamRecoveryCheckpointPersistsBeforeCompletion protects already delivered text from process interruption.
func TestStreamRecoveryCheckpointPersistsBeforeCompletion(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"Delivered partial text"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	t.Cleanup(engine.cancel)
	config := defaultProvider()
	config.Model = "fixture"
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "action": "ask", "instruction": "Discuss"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current := invoke[Job](t, engine, "get_job", map[string]any{"job_id": job.ID})
		if current.Text != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	persisted, err := engine.loadStoredJob(job.ID)
	assert(t, err == nil && persisted.Text == "Delivered partial text", "Delivered text was not checkpointed before completion")
	invoke[map[string]bool](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
	cancelled := finishJob(t, engine, job)
	assert(t, cancelled.Status == "cancelled", "Cancelling a checkpointed stream failed")
}
