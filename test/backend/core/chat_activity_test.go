//go:build quillweave_whitebox

package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestProviderThoughtStreams separates visible provider thought text from signatures and answer prose for every adapter.
func TestProviderThoughtStreams(t *testing.T) {
	for _, protocol := range []string{"openai", "responses", "anthropic", "gemini", "gemini_generate", "ollama"} {
		t.Run(protocol, func(t *testing.T) {
			thought := "PUBLIC_THOUGHTPRIVATE_THOUGHT"
			frame := ""
			switch protocol {
			case "openai":
				frame = `{"choices":[{"delta":{"reasoning_content":"PUBLIC_THOUGHT","encrypted_content":"OPAQUE_SECRET"}}]}`
			case "responses":
				frame = `{"type":"response.reasoning_summary_text.delta","delta":"PUBLIC_THOUGHT","summary_index":0}`
			case "anthropic":
				frame = `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"PUBLIC_THOUGHT"}}`
			case "gemini":
				frame = `{"event_type":"step.delta","index":0,"delta":{"type":"thought_summary","text":"PUBLIC_THOUGHT"}}`
			case "gemini_generate":
				frame = `{"candidates":[{"content":{"parts":[{"thought":true,"text":"PUBLIC_THOUGHT","thoughtSignature":"OPAQUE_SECRET"}]}}]}`
			case "ollama":
				frame = `{"message":{"thinking":"PUBLIC_THOUGHT"},"done":false}`
			}
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				if protocol == "ollama" {
					fmt.Fprint(w, frame+"\n")
				} else {
					fmt.Fprint(w, "data: "+frame+"\n\n")
				}
				fmt.Fprint(w, nativeStreamFixture(protocol, "ANSWER_ONLY"))
			})
			config := nativeConfig(protocol)
			config.BaseURL = server.URL
			visible := ""
			config.onReasoning = func(delta string) { visible += delta }
			answer, err := streamModel(context.Background(), config, []Message{{Role: "user", Content: "Explain"}}, func(string) {})
			assert(t, err == nil && answer == "ANSWER_ONLY" && visible == thought, fmt.Sprintf("Thought adapter mismatch: %s %q %v", protocol, visible, err))
		})
	}
}

// TestReasoningSummaryCompletion handles done-only summaries, missing tail deltas and opaque provider data.
func TestReasoningSummaryCompletion(t *testing.T) {
	visible := ""
	config := nativeConfig("responses")
	config.onReasoning = func(delta string) { visible += delta }
	stream := modelStream{config: config}
	for _, event := range []string{
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"First"}`,
		`{"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0,"text":"First summary."}`,
		`{"type":"response.reasoning_summary_text.done","output_index":0,"summary_index":0,"text":"First summary."}`,
		`{"type":"response.reasoning_summary_text.done","output_index":1,"summary_index":0,"text":"Second summary."}`,
		`{"type":"response.reasoning_text.delta","delta":"OPAQUE_RAW_REASONING"}`,
	} {
		assert(t, stream.payload("", event) == nil, "A valid summary event failed")
	}
	assert(t, visible == "First summary.Second summary.", "A final summary was duplicated, truncated or mixed with raw provider reasoning")
	stream.publicReasoningBytes = maxStreamBytes
	assert(t, stream.payload("", `{"type":"response.reasoning_summary_text.delta","delta":"overflow"}`) != nil, "Thought output bypassed the shared stream limit")
	body, _, err := modelRequest(config, []Message{{Role: "user", Content: "Discuss"}})
	assert(t, err == nil && body["reasoning"] == nil, "An unknown non-reasoning model received an unsupported summary parameter")
	config.ReasoningEffort = "low"
	body, _, err = modelRequest(config, []Message{{Role: "user", Content: "Discuss"}})
	assert(t, err == nil && strings.Contains(jsonText(body), `"summary":"auto"`), "An explicitly enabled reasoning model did not request summaries")
}

// TestLiveChatActivitySurvivesInterruption observes thought and answer before completion, then checks durable recovery.
func TestLiveChatActivitySurvivesInterruption(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	flushed := make(chan struct{})
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Visible thought\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Visible opening\"}}]}\n\n")
		w.(http.Flusher).Flush()
		close(flushed)
		<-r.Context().Done()
	})
	config := defaultProvider()
	config.Model, config.BaseURL, config.ContextChars = "live-activity", server.URL, 64000
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": "", "action": "agent", "instruction": "Discuss"})
	<-flushed
	deadline := time.Now().Add(3 * time.Second)
	for {
		current := invoke[Job](t, engine, "get_job", map[string]any{"job_id": job.ID})
		if len(current.Activity) >= 2 {
			assert(t, current.Status == "running" && current.Activity[0].Kind == "reasoning" && current.Activity[1].Kind == "text" && current.Text == "Visible opening", "Live event order or prose streaming was lost")
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Thought and prose did not stream before completion")
		}
		time.Sleep(10 * time.Millisecond)
	}
	invoke[map[string]bool](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
	done := finishJob(t, engine, job)
	assert(t, done.Status == "cancelled" && len(done.Activity) == 2 && done.Activity[0].Text == "Visible thought", "Cancellation lost public thought or partial prose")
	assert(t, engine.Close() == nil, "Close failed")
	peer, err := NewEngine(engine.store.path)
	assert(t, err == nil, fmt.Sprint(err))
	defer peer.Close()
	restored := invoke[Job](t, peer, "get_job", map[string]any{"job_id": job.ID})
	assert(t, len(restored.Activity) == 2 && restored.Activity[1].Text == "Visible opening", "Restart lost the partial activity history")
}

// TestWriterCreatesOutlineChapterAndSaves uses the normal coordinator, writer, review and database entry points.
func TestWriterCreatesOutlineChapterAndSaves(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	project.Chapters = []Chapter{}
	project.Studio.Plans = []StoryPlan{{ID: "chapter-plan", Level: "chapter", Title: "The sealed letter", StartChapter: 1, EndChapter: 1, Status: "confirmed", Goal: "Learn who sent the letter", Choice: "Open it before the tide returns"}}
	insertProject(t, engine, project)
	body := "She opened the sealed letter beside the window. The tide had not yet returned."
	server, _ := sequenceModel(t, []string{
		`{"tool":"writer","plan_id":"chapter-plan","query":"Write the confirmed first chapter"}`,
		writingCompositionFixture(),
		body,
		`{"summary":"The scene follows its outline.","issues":[],"needs_revision":false,"memories":[]}`,
		`{"tool":"finish","text":"The first chapter has been saved."}`,
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "outline-writer", 64000
	configureModel(t, engine, config)
	done := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": "", "action": "agent", "commit_policy": "auto", "instruction": "Write chapter one using its outline"}))
	assert(t, done.Status == "done", done.Error)
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(saved.Chapters) == 1 && saved.Chapters[0].Number == 1 && saved.Chapters[0].Title == "The sealed letter" && saved.Chapters[0].Body == body, "The writer did not create and save the outlined chapter")
	assert(t, done.Writing != nil && done.Writing.Saved && done.Writing.TargetChapterID == saved.Chapters[0].ID && len(done.ChangeSets) == 1 && done.ChangeSets[0].Status == ChangeSetApplied, "Automatic writing lost its durable target or undo receipt")
	assert(t, len(done.Activity) >= 3 && strings.Contains(done.Text, "saved"), "The writer tool was absent from the ordered conversation")
	invoke[map[string]any](t, engine, "undo_changeset", map[string]any{"project_id": project.ID, "changeset_id": done.ChangeSets[0].ID, "revision": saved.Revision})
	undone := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, undone.Chapters[0].Body == "", "The automatic prose could not be undone")
}

// TestWritingOutlineSelection checks exact targets, completed chapters, missing plans and proposal boundaries.
func TestWritingOutlineSelection(t *testing.T) {
	project := testProject()
	project.Studio.Plans = []StoryPlan{
		{ID: "volume", Level: "volume", StartChapter: 1, EndChapter: 3, Status: "confirmed"},
		{ID: "second", ParentID: "volume", Level: "chapter", StartChapter: 2, EndChapter: 2, Status: "confirmed"},
		{ID: "first", ParentID: "volume", Level: "chapter", StartChapter: 1, EndChapter: 1, Status: "confirmed"},
		{ID: "third", Level: "chapter", StartChapter: 3, EndChapter: 3, Status: "proposed"},
	}
	for _, item := range []struct {
		name string
		step agentStepEnvelope
		want string
	}{
		{"next unwritten", agentStepEnvelope{}, "second"},
		{"explicit plan", agentStepEnvelope{PlanID: "first"}, "first"},
		{"explicit number", agentStepEnvelope{Chapter: 2}, "second"},
		{"focused volume", agentStepEnvelope{PlanID: "volume"}, "second"},
		{"missing plan", agentStepEnvelope{PlanID: "missing"}, ""},
		{"unconfirmed", agentStepEnvelope{PlanID: "third"}, ""},
		{"conflicting target", agentStepEnvelope{PlanID: "first", Chapter: 2}, ""},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan, err := resolveWritingPlan(&project, Job{}, item.step)
			if item.want == "" {
				assert(t, err != nil, "An unsupported target was silently selected")
				return
			}
			assert(t, err == nil && plan.ID == item.want, "Wrong outline selected")
		})
	}
}

// TestAutomaticWriterReceiptReportsCandidate keeps unresolved plan feedback out of saved-output receipts.
func TestAutomaticWriterReceiptReportsCandidate(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	project.Chapters = nil
	project.Studio.Plans = []StoryPlan{{ID: "candidate-plan", Level: "chapter", Title: "The letter", StartChapter: 1, EndChapter: 1, Status: "confirmed", Choice: "Open the letter"}}
	insertProject(t, engine, project)
	server, _ := sequenceModel(t, []string{
		`{"tool":"writer","plan_id":"candidate-plan","query":"Write the letter scene"}`,
		writingCompositionFixture(),
		"She opened the letter.",
		`{"summary":"The goal needs a new plan.","issues":[{"kind":"continuity","scope":"plan","quote":"opened the letter","explanation":"The current plan requires a different choice","suggestion":"Repair the outline"}],"memories":[],"needs_revision":true}`,
		`{"tool":"propose_changes","operations":[{"target_kind":"entity","op_type":"upsert","title":"The courier","after_value":{"name":"The courier","kind":"character","description":"Delivers the letter"}}]}`,
		`{"tool":"finish","text":"The candidate is retained."}`,
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "candidate-receipt", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", AIRequest{ProjectID: project.ID, Action: "agent", Instruction: "Write from this outline", CommitPolicy: "auto", History: []Message{}}))
	assert(t, job.Status == "done" && job.Writing != nil && !job.Writing.Saved && job.Writing.ReviewStatus == "needs_plan", "The unresolved writing candidate changed its review outcome")
	for _, step := range job.Steps {
		if step.Tool == "writer" {
			assert(t, !strings.Contains(step.Detail, "created and saved") && strings.Contains(step.Detail, "Candidate preserved"), "The writer falsely reported a saved chapter: "+step.Detail)
		}
	}
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(saved.Chapters) == 1 && saved.Chapters[0].Body == "", "Unresolved prose entered the saved chapter")
	assert(t, len(saved.Entities) == len(project.Entities)+1 && job.ChangeSets[0].Status == ChangeSetStaged, "A retained prose candidate blocked a separate automatic record or lost its review state")
}

// TestAutomaticWriterResumeReusesChapter retains a partial draft and its newly created chapter across coordinator failure.
func TestAutomaticWriterResumeReusesChapter(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	project.Chapters = []Chapter{}
	project.Studio.Plans = []StoryPlan{{ID: "resume-plan", Level: "chapter", Title: "The letter", StartChapter: 1, EndChapter: 1, Status: "confirmed", Choice: "Open the letter"}}
	insertProject(t, engine, project)
	prefix, tail := "She opened the letter. ", "The window rattled in the wind."
	var calls atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1, 5:
			fmt.Fprint(w, streamPayload(`{"tool":"writer","plan_id":"resume-plan","query":"Write the first chapter"}`))
		case 2:
			fmt.Fprint(w, streamPayload(writingCompositionFixture()))
		case 3:
			fmt.Fprint(w, "data: "+jsonText(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": prefix}}}})+"\n\n")
		case 4:
			w.WriteHeader(503)
		case 6:
			fmt.Fprint(w, streamPayload(tail))
		case 7:
			fmt.Fprint(w, streamPayload(`{"summary":"Consistent.","issues":[],"memories":[],"needs_revision":false}`))
		default:
			fmt.Fprint(w, streamPayload(`{"tool":"finish","text":"The chapter is saved."}`))
		}
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "automatic-resume", 64000
	configureModel(t, engine, config)
	args := map[string]any{"project_id": project.ID, "chapter_id": "", "action": "agent", "commit_policy": "auto", "instruction": "Write from the chapter outline"}
	failed := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
	assert(t, failed.Status == "error" && failed.Writing != nil && failed.Writing.Continuation == prefix, "An interrupted automatic writer lost its partial draft")
	created := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(created.Chapters) == 1 && created.Chapters[0].Body == "", "The interrupted task published incomplete prose")
	args["resume_job_id"], args["session_id"] = failed.ID, failed.SessionID
	resumed := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
	assert(t, resumed.Status == "done", resumed.Error)
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(saved.Chapters) == 1 && saved.Chapters[0].ID == created.Chapters[0].ID && saved.Chapters[0].Body == prefix+tail, "Resume duplicated the target chapter, lost its prefix or failed to save")
}

// TestSelectedEmptyChapterWriteSaves follows the direct slash-write entry point for an already-created empty chapter.
func TestSelectedEmptyChapterWriteSaves(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	project.Chapters[0].Body = ""
	project.Chapters[0].Synopsis = "Open the sealed letter before the tide returns."
	insertProject(t, engine, project)
	server, _ := sequenceModel(t, []string{writingCompositionFixture(), "She opened the sealed letter.", `{"summary":"Consistent.","issues":[],"memories":[],"needs_revision":false}`})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "selected-outline-writer", 64000
	configureModel(t, engine, config)
	done := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "continue", "intent_hint": "write_from_outline", "commit_policy": "auto", "instruction": "Write this chapter"}))
	assert(t, done.Status == "done" && done.Writing.Saved, done.Error)
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(saved.Chapters) == 1 && saved.Chapters[0].Body == "She opened the sealed letter.", "Slash-write did not save its selected empty chapter")
}

// TestAutomaticWriterProtectsConcurrentAuthorEdit preserves author prose written while the model reviews its candidate.
func TestAutomaticWriterProtectsConcurrentAuthorEdit(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	project.Chapters = []Chapter{}
	project.Studio.Plans = []StoryPlan{{ID: "concurrent-plan", Level: "chapter", Title: "The letter", StartChapter: 1, EndChapter: 1, Status: "confirmed", Choice: "Open the letter"}}
	insertProject(t, engine, project)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			fmt.Fprint(w, streamPayload(`{"tool":"writer","plan_id":"concurrent-plan","query":"Write chapter one"}`))
		case 2:
			fmt.Fprint(w, streamPayload(writingCompositionFixture()))
		case 3:
			fmt.Fprint(w, streamPayload("The candidate opening."))
		case 4:
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, streamPayload(`{"summary":"Consistent.","issues":[],"memories":[],"needs_revision":false}`))
		default:
			fmt.Fprint(w, streamPayload(`{"tool":"finish","text":"The story changed. The candidate is retained."}`))
		}
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "concurrent-author-writer", 64000
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": "", "action": "agent", "commit_policy": "auto", "instruction": "Write from the outline"})
	<-entered
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	chapter := current.Chapters[0]
	invoke[Project](t, engine, "save_chapter", map[string]any{"project_id": project.ID, "revision": current.Revision, "chapter_id": chapter.ID, "title": chapter.Title, "body": "The author's own opening.", "synopsis": chapter.Synopsis, "status": "draft", "target_words": nil})
	close(release)
	done := finishJob(t, engine, job)
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, saved.Chapters[0].Body == "The author's own opening." && done.Writing != nil && !done.Writing.Saved && done.Writing.Draft == "The candidate opening.", "The automatic writer overwrote a concurrent author edit or discarded its candidate")
}
