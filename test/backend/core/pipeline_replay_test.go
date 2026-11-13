//go:build quillweave_whitebox

package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The 2026-11-10 production failures replayed with non-cooperative models: the
// composition stage that answered with full chapter prose, JSON buried behind
// prose, revision failures and the new batch/read/watchdog machinery.

// compositionProseReplay is a chapter-length answer to the composition stage, like the
// real 21:40 / 21:47 captures where the model resolved conflicting instructions by writing.
func compositionProseReplay() string {
	return strings.Repeat("潮水漫过防波堤的时候，林雾把最后一册档案搬进了干燥的抽屉。", 16)
}

// TestCompositionProseFallbackKeepsTheDraft replays a model that keeps answering the composition
// stage with finished chapter prose: after one corrective retry the prose must become the draft
// and reach review, never "did not return an intent and approach".
func TestCompositionProseFallbackKeepsTheDraft(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	prose := compositionProseReplay()
	var compositions atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		switch {
		case strings.Contains(body, "[Writing composition stage]"):
			compositions.Add(1)
			fmt.Fprint(w, streamPayload(prose))
		case strings.Contains(body, "[Writing review stage]"):
			fmt.Fprint(w, streamPayload(`{"summary":"The candidate reads cleanly.","issues":[],"memories":[]}`))
		default:
			t.Errorf("Unexpected request after the prose fallback: %.80s", body)
			fmt.Fprint(w, streamPayload(prose))
		}
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "prose-replay-fixture", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "rewrite", "instruction": "重构第一章"}))
	assert(t, job.Status == "done" && job.Error == "", "The prose fallback still failed: "+job.Error)
	assert(t, compositions.Load() == 2, fmt.Sprintf("The composition stage was asked %d times instead of one corrective retry", compositions.Load()))
	assert(t, job.Writing != nil && job.Writing.Draft == prose, "The composition prose was not kept as the draft")
	assert(t, job.Writing.Review != nil && job.Writing.Review.Summary != "", "The salvaged draft was never reviewed")
	assert(t, len(job.Proposals) == 1 && job.Proposals[0].Content == prose, "The salvaged draft did not reach the candidates")
}

// TestCompositionProseRetriesBeforeFallback replays a model that first answers the composition
// stage with prose and then submits scene cards: the prose is dropped and the prose stage writes.
func TestCompositionProseRetriesBeforeFallback(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	var compositions, corrections, drafts atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		switch {
		case strings.Contains(body, "[Writing composition stage]"):
			if strings.Contains(body, "was chapter prose") {
				corrections.Add(1)
			}
			if compositions.Add(1) == 1 {
				fmt.Fprint(w, streamPayload(compositionProseReplay()))
				return
			}
			fmt.Fprint(w, streamPayload(writingCompositionFixture()))
		case strings.Contains(body, "[Writing prose stage]"):
			drafts.Add(1)
			fmt.Fprint(w, streamPayload("She checks the records before opening the gate."))
		case strings.Contains(body, "[Writing review stage]"):
			fmt.Fprint(w, streamPayload(`{"summary":"Ready.","issues":[],"memories":[]}`))
		default:
			t.Errorf("Unexpected request after the corrective retry: %.80s", body)
		}
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "prose-retry-fixture", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "继续"}))
	assert(t, job.Status == "done" && job.Error == "", "The corrective retry failed the job: "+job.Error)
	assert(t, compositions.Load() == 2 && corrections.Load() == 1 && drafts.Load() == 1, fmt.Sprintf("compositions=%d corrections=%d drafts=%d", compositions.Load(), corrections.Load(), drafts.Load()))
	assert(t, job.Writing.Composition != nil && job.Writing.Composition.Intent == "Continue the keeper's ordinary work", "The retried composition was not adopted")
	assert(t, strings.Contains(job.Writing.Draft, "She checks the records") && !strings.Contains(job.Writing.Draft, "潮水漫过防波堤"), "The dropped composition prose reached the draft")
}

// TestCompositionJSONWithTrailingProseIsAdopted accepts a JSON composition followed by
// commentary without wasting a retry.
func TestCompositionJSONWithTrailingProseIsAdopted(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	var compositions atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		switch {
		case strings.Contains(body, "[Writing composition stage]"):
			compositions.Add(1)
			fmt.Fprint(w, streamPayload(writingCompositionFixture()+"\n\n以上是本阶段的构思，下面不再展开正文。"))
		case strings.Contains(body, "[Writing review stage]"):
			fmt.Fprint(w, streamPayload(`{"summary":"Ready.","issues":[],"memories":[]}`))
		default:
			fmt.Fprint(w, streamPayload("She checks the records before opening the gate."))
		}
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "json-tail-fixture", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "继续"}))
	assert(t, job.Status == "done" && compositions.Load() == 1, "Embedded JSON wasted retries or failed: "+job.Error)
	assert(t, job.Writing.Composition != nil && job.Writing.Composition.Intent == "Continue the keeper's ordinary work", "The embedded composition was not adopted")
}

// TestRevisionFailureSalvagesReviewedCandidate keeps a reviewed draft reviewable when the
// revision round cannot produce a valid composition.
func TestRevisionFailureSalvagesReviewedCandidate(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	var compositions atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		switch {
		case strings.Contains(body, "[Writing composition stage]"):
			if compositions.Add(1) == 1 {
				fmt.Fprint(w, streamPayload(writingCompositionFixture()))
				return
			}
			fmt.Fprint(w, streamPayload("构思失败。"))
		case strings.Contains(body, "[Writing review stage]"):
			fmt.Fprint(w, streamPayload(jsonText(WritingReview{Summary: "Slow the reveal.", NeedsRevision: true, Issues: []WritingIssue{{Kind: "reading", Scope: "composition", Quote: "Lin Wu found a letter.", Explanation: "The action needs grounding.", Suggestion: "Check the records first."}}})))
		default:
			fmt.Fprint(w, streamPayload("Lin Wu found a letter. She decided to visit the lighthouse again."))
		}
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "salvage-fixture", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "继续"}))
	assert(t, job.Status == "done" && len(job.Proposals) == 1, "A failed revision discarded the reviewed draft: "+job.Error)
	assert(t, job.Writing.Review != nil && strings.Contains(job.Writing.Review.Warning, "preserved"), "The salvage note is missing")
}

// TestChapterReadsAreWholeAndBatched checks the loosened read contract: whole chapters by
// default and multi-chapter / multi-outline batch reads.
func TestChapterReadsAreWholeAndBatched(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	long := NewChapter(2, "The Long Ledger")
	long.Body = strings.Repeat("账", 4500)
	p.Chapters = append(p.Chapters, long)
	p.Studio.Plans = []StoryPlan{
		{ID: "plan-a", Level: "chapter", Title: "First outline", StartChapter: 1, EndChapter: 1, Status: "confirmed", Choice: "Open the letter"},
		{ID: "plan-b", Level: "chapter", Title: "Second outline", StartChapter: 2, EndChapter: 2, Status: "confirmed", Choice: "Read the ledger"},
	}
	insertProject(t, engine, p)
	job := Job{ID: "read-check", ProjectID: p.ID}
	whole, err := engine.executeScopedAgentTool(&p, job, agentStepEnvelope{Tool: "get_chapter_window", Chapter: 2})
	assert(t, err == nil && strings.Contains(whole, `"total_chars":4500`) && !strings.Contains(whole, `"truncated":true`), "A whole chapter still needs paging: "+whole)
	batch, err := engine.executeScopedAgentTool(&p, job, agentStepEnvelope{Tool: "read_chapters", Chapters: []uint32{1, 2}})
	assert(t, err == nil && strings.Contains(batch, "The Letter") && strings.Contains(batch, "The Long Ledger"), "The batch read missed a chapter: "+errText(err))
	plans, err := engine.executeScopedAgentTool(&p, job, agentStepEnvelope{Tool: "read_plan_nodes", PlanIDs: []string{"plan-a", "plan-b"}})
	assert(t, err == nil && strings.Contains(plans, "plan-a") && strings.Contains(plans, "plan-b"), "The outline batch read missed a node: "+errText(err))
}

// TestNativePerActionFunctionsDispatch verifies the split native declarations dispatch
// through the same validated executor as the legacy envelope.
func TestNativePerActionFunctionsDispatch(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	var calls atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			arguments := `{"query":"lighthouse rules"}`
			first, second := arguments[:len(arguments)/2], arguments[len(arguments)/2:]
			fmt.Fprint(w, "data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-split","type":"function","function":{"name":"quillweave_recall","arguments":`+jsonText(first)+`}}]}}]}`+"\n\n")
			fmt.Fprint(w, "data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":`+jsonText(second)+`}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, streamPayload("The keeper keeps the light burning."))
	})
	config := nativeConfig("openai")
	config.BaseURL, config.ContextChars, config.nativeTools = server.URL, 64000, true
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "agent", "instruction": "查询规则"}))
	assert(t, job.Status == "done", "The split-function call failed: "+job.Error)
	recallDone := false
	for _, step := range job.Steps {
		recallDone = recallDone || step.Tool == "recall" && step.Status == "done"
	}
	assert(t, recallDone, "quillweave_recall never executed")
}

// TestMultiChapterProductionWritesEachChapter drives one writer call with count=3 across
// confirmed outlines: every chapter is produced and saved on its own boundary.
func TestMultiChapterProductionWritesEachChapter(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	for number := uint32(2); number <= 4; number++ {
		p.Studio.Plans = append(p.Studio.Plans, StoryPlan{ID: fmt.Sprintf("outline-%d", number), Level: "chapter", Title: fmt.Sprintf("Outline %d", number), StartChapter: number, EndChapter: number, Status: "confirmed", Choice: "Advance the tide"})
	}
	insertProject(t, engine, p)
	var coordinator atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		switch {
		case strings.Contains(body, "[Writing composition stage]"):
			fmt.Fprint(w, streamPayload(writingCompositionFixture()))
		case strings.Contains(body, "[Writing review stage]"):
			fmt.Fprint(w, streamPayload(`{"summary":"Ready.","issues":[],"memories":[]}`))
		case strings.Contains(body, "[Writing prose stage]"):
			fmt.Fprint(w, streamPayload("潮水上涨，守灯人核对了一遍记录，把新的约定写进账册。"))
		default:
			if coordinator.Add(1) == 1 {
				fmt.Fprint(w, streamPayload(`{"tool":"writer","query":"完成后面三章","count":3}`))
				return
			}
			fmt.Fprint(w, streamPayload(`{"tool":"finish","text":"三章已完成。"}`))
		}
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "batch-writer-fixture", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "action": "agent", "commit_policy": "auto", "instruction": "完成后面三章"}))
	assert(t, job.Status == "done", "Multi-chapter production failed: "+job.Error)
	latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	written := 0
	for _, chapter := range latest.Chapters {
		if chapter.Number >= 2 && chapter.Number <= 4 && len(chapter.Body) > 0 {
			written++
		}
	}
	assert(t, written == 3, fmt.Sprintf("Expected three saved chapters, found %d; steps: %s; error: %s", written, jsonText(job.Steps), job.Error))
}

// TestIdleWatchdogTimesOutQuietStreams replaces the total-duration cap: an active stream
// may run long, but a silent one fails with a named idle timeout.
func TestIdleWatchdogTimesOutQuietStreams(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keepalive\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars, config.TimeoutSeconds = server.URL, "silent-fixture", 64000, 10
	configureModel(t, engine, config)
	started := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "ask", "instruction": "讨论"})
	deadline := time.Now().Add(15 * time.Second)
	job := started
	for time.Now().Before(deadline) {
		job = invoke[Job](t, engine, "get_job", map[string]any{"job_id": started.ID})
		if job.Status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert(t, job.Status == "error" && strings.Contains(job.Error, "went idle"), "A silent stream did not fail with the idle timeout: "+job.Error)
}

// TestNativeDeclarationBudgetGuard verifies the complete strict contracts leave room
// for real story context in the default window and the normal entry reaches the model.
func TestNativeDeclarationBudgetGuard(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	server, calls := sequenceModel(t, []string{"The keeper can decline the request."})
	config := nativeConfig("openai")
	config.BaseURL, config.ContextChars = server.URL, 24000
	configureModel(t, engine, config)
	args := map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "agent", "history": []Message{}, "instruction": "Discuss Lin Wu's reason to leave without changing the saved story"}
	prompt := invoke[Prompt](t, engine, "prepare_ai", args)
	assert(t, prompt.ToolChars < 24000*3/5 && prompt.Chars < 24000 && strings.Contains(jsonText(prompt.Messages), project.Chapters[0].Body), "Strict tools crowded the actual chapter out of the default window")
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
	assert(t, job.Status == "done" && calls.Load() == 1 && job.Text != "", "The default native request never reached the model: "+job.Error)
}

// TestTruncateConversationRetriesInPlace keeps regeneration on the same conversation
// instead of forking a child session.
func TestTruncateConversationRetriesInPlace(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, streamPayload(`{"tool":"finish","text":"第一轮回答。"}`))
	})
	config := defaultProvider()
	config.BaseURL, config.Model = server.URL, "truncate-fixture"
	configureModel(t, engine, config)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
	first := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "action": "agent", "instruction": "第一问", "session_id": session.ID}))
	saved := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	assert(t, len(saved.Turns) == 2 && first.Status == "done", "The fixture exchange did not persist")
	var userTurn string
	for _, turn := range saved.Turns {
		if turn.Role == "user" {
			userTurn = turn.ID
		}
	}
	truncated := invoke[Conversation](t, engine, "truncate_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID, "turn_id": userTurn})
	assert(t, truncated.ID == session.ID && len(truncated.Turns) == 0 && !strings.Contains(truncated.Title, "branch"), "Truncation forked or kept turns")
	list := invoke[[]Conversation](t, engine, "list_conversations", map[string]any{"project_id": p.ID, "chapter_id": ""})
	assert(t, len(list) == 1, "Truncation created an extra conversation")
}

// TestSupersedesRetiresConfirmedMemory guarantees two confirmed versions of the same fact
// can never be retrieved together.
func TestSupersedesRetiresConfirmedMemory(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Studio.Memories = []StoryMemory{{ID: "mem-a", Kind: "setting", Title: "旧版灯塔规则", Content: "灯每天熄灭一次", Status: "confirmed"}}
	insertProject(t, engine, p)
	job := structuredProposalJob(t, engine, p, []ChangeOperation{{TargetKind: TargetMemory, OpType: OpUpsert, Title: "新版灯塔规则", AfterValue: rawMessage(StoryMemory{ID: "mem-b", Kind: "setting", Title: "新版灯塔规则", Content: "灯常年亮着", Status: "confirmed", Supersedes: "mem-a"})}})
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": p.ID, "changeset_id": job.ChangeSets[0].ID})
	invoke[any](t, engine, "apply_changeset", map[string]any{"project_id": p.ID, "changeset_id": preview.ChangeSet.ID, "revision": preview.ChangeSet.PreviewRevision, "review_token": preview.ChangeSet.ReviewToken})
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	retired, confirmed := false, false
	for _, memory := range current.Studio.Memories {
		if memory.ID == "mem-a" {
			retired = memory.Status == "superseded"
		}
		if memory.ID == "mem-b" {
			confirmed = memory.Status == "confirmed"
		}
	}
	assert(t, retired && confirmed, "The superseded memory stayed confirmed")
	for _, item := range memoryContext(&current, ^uint32(0)) {
		assert(t, !strings.Contains(item.Title, "旧版灯塔规则"), "The retired memory is still retrieved")
	}
}

// errText renders nil errors as empty strings for assertion messages.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestMultiChapterProductionRespectsPlanningScope freezes volume, unit and chapter scopes across all iterations.
func TestMultiChapterProductionRespectsPlanningScope(t *testing.T) {
	for _, scope := range []string{"volume-a", "unit-a", "outline-1", "workspace"} {
		t.Run(scope, func(t *testing.T) {
			engine := testEngine(t)
			p := testProject()
			p.Chapters[0].Body = ""
			p.Studio.Plans = []StoryPlan{
				{ID: "book", Level: "book", Title: "Book", StartChapter: 1, EndChapter: 3, Status: "confirmed"},
				{ID: "volume-a", ParentID: "book", Level: "volume", Title: "First volume", StartChapter: 1, EndChapter: 2, Status: "confirmed"},
				{ID: "unit-a", ParentID: "volume-a", Level: "unit", Title: "First unit", StartChapter: 1, EndChapter: 2, Status: "confirmed"},
				{ID: "volume-b", ParentID: "book", Level: "volume", Title: "Second volume", StartChapter: 3, EndChapter: 3, Status: "confirmed"},
			}
			for number := uint32(1); number <= 3; number++ {
				parent := "unit-a"
				if number == 3 {
					parent = "volume-b"
				}
				p.Studio.Plans = append(p.Studio.Plans, StoryPlan{ID: fmt.Sprintf("outline-%d", number), ParentID: parent, Level: "chapter", Title: fmt.Sprintf("Outline %d", number), StartChapter: number, EndChapter: number, Status: "confirmed", Choice: "Check the ledger"})
			}
			insertProject(t, engine, p)
			var coordinator atomic.Int32
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				body := string(readBodyForStudioTest(t, r))
				switch {
				case strings.Contains(body, "[Writing composition stage]"):
					fmt.Fprint(w, streamPayload(writingCompositionFixture()))
				case strings.Contains(body, "[Writing review stage]"):
					fmt.Fprint(w, streamPayload(`{"summary":"Reviewed.","issues":[],"memories":[]}`))
				case strings.Contains(body, "[Writing prose stage]"):
					fmt.Fprint(w, streamPayload("The keeper checks the ledger and returns to the gate."))
				default:
					if coordinator.Add(1) == 1 {
						step := agentStepEnvelope{Tool: "writer", PlanID: scope, Count: 3, Query: "Write only the selected scope"}
						if scope == "workspace" {
							step.PlanID = ""
						}
						fmt.Fprint(w, streamPayload(jsonText(step)))
						return
					}
					fmt.Fprint(w, streamPayload(`{"tool":"finish","text":"Selected scope processed."}`))
				}
			})
			config := defaultProvider()
			config.BaseURL, config.Model, config.ContextChars = server.URL, "scope-fixture", 64000
			configureModel(t, engine, config)
			args := map[string]any{"project_id": p.ID, "chapter_id": "", "action": "agent", "commit_policy": "auto", "instruction": "Write only the selected scope"}
			if scope == "workspace" {
				args["workspace_context"] = WorkspaceContext{ActivePlanID: "unit-a"}
			}
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, job.Status == "done", job.Error)
			latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
			expected := 2
			if scope == "outline-1" {
				expected = 1
			}
			assert(t, len(latest.Chapters) == expected, fmt.Sprintf("Scope %s produced %d chapters", scope, len(latest.Chapters)))
			for _, chapter := range latest.Chapters {
				assert(t, chapter.Number <= uint32(expected) && chapter.Body != "", "A chapter outside the selected scope was written")
			}
			assert(t, strings.Contains(jsonText(job.Steps), "requested chapters remain"), "The scope boundary did not report the remaining count")
		})
	}
}

// TestMultiChapterProductionPausesForIncompleteReview avoids deriving later prose from an unaccepted chapter.
func TestMultiChapterProductionPausesForIncompleteReview(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Chapters[0].Body = ""
	for number := uint32(1); number <= 2; number++ {
		p.Studio.Plans = append(p.Studio.Plans, StoryPlan{ID: fmt.Sprintf("outline-%d", number), Level: "chapter", Title: fmt.Sprintf("Outline %d", number), StartChapter: number, EndChapter: number, Status: "confirmed", Choice: "Check the ledger"})
	}
	insertProject(t, engine, p)
	var coordinator, drafts atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		switch {
		case strings.Contains(body, "[Writing composition stage]"):
			fmt.Fprint(w, streamPayload(writingCompositionFixture()))
		case strings.Contains(body, "[Writing review stage]"):
			fmt.Fprint(w, streamPayload("Review could not return the requested structured result."))
		case strings.Contains(body, "[Writing prose stage]"):
			drafts.Add(1)
			fmt.Fprint(w, streamPayload("The keeper checks the ledger before opening the gate."))
		default:
			if coordinator.Add(1) == 1 {
				fmt.Fprint(w, streamPayload(`{"tool":"writer","count":2,"query":"Write both chapters"}`))
				return
			}
			fmt.Fprint(w, streamPayload(`{"tool":"finish","text":"The first candidate needs review."}`))
		}
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "batch-review-pause", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "action": "agent", "commit_policy": "auto", "instruction": "Write both chapters"}))
	latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, job.Status == "done" && drafts.Load() == 1 && len(latest.Chapters) == 1 && latest.Chapters[0].Body == "", "The batch continued beyond an unreviewed candidate: "+job.Error)
	assert(t, job.Writing != nil && job.Writing.Phase == "needs_review" && strings.Contains(jsonText(job.Steps), "batch paused"), "The paused batch did not expose the author-review boundary")
}
