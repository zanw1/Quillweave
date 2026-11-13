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

// writingCompositionFixture provides an explicit local plan for deterministic writer-stage fixtures.
func writingCompositionFixture() string {
	return jsonText(WritingComposition{Intent: "Continue the keeper's ordinary work", Approach: "Use the known tools and independent reactions; retain the resulting obligation", SceneCards: []WritingScene{writingSceneFixture()}, SceneNotes: []string{"Check the records before acting"}, RevealNotes: []string{"Keep unknown identities private"}})
}

// TestWritingCompositionBudget reserves prose and review calls before retrying an incomplete composition.
func TestWritingCompositionBudget(t *testing.T) {
	for budget := 0; budget <= 5; budget++ {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			insertProject(t, engine, project)
			server, calls := sequenceModel(t, []string{`{"intent":"Plan","approach":"Incomplete"}`, `{"intent":"Plan","approach":"Incomplete"}`, `{"intent":"Plan","approach":"Incomplete"}`})
			config := defaultProvider()
			config.BaseURL, config.Model, config.ContextChars = server.URL, "bounded-composition", 64000
			configureModel(t, engine, config)
			selected, err := engine.models.Select(&project, "continue")
			assert(t, err == nil, fmt.Sprint(err))
			prompt, err := prepareAI(&project, AIRequest{ProjectID: project.ID, ChapterID: project.Chapters[0].ID, Action: "continue", Instruction: "Continue"}, 64000)
			assert(t, err == nil, fmt.Sprint(err))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			job := Job{ID: newID(), ProjectID: project.ID, ChapterID: project.Chapters[0].ID, Action: "continue", Instruction: "Continue", Status: "running", ctx: ctx, cancel: cancel, Prompt: prompt, BaseRevision: project.Revision}
			engine.jobsMu.Lock()
			engine.jobs[job.ID] = &job
			engine.jobsMu.Unlock()
			assert(t, engine.store.SaveRun(job.ID, project.ID, storedJob(&job)) == nil, "Could not save the bounded task fixture")
			_, used, err := engine.runWritingTask(job, &project, selected, prompt, "append", budget)
			assert(t, err != nil && used == max(0, budget-2) && int(calls.Load()) == used && used <= budget, fmt.Sprintf("Composition exceeded its call budget: budget=%d used=%d calls=%d err=%v", budget, used, calls.Load(), err))
		})
	}
}

// TestAutomaticWritingTransaction covers existing prose, scene-card persistence, source-linked deltas and undo.
func TestAutomaticWritingTransaction(t *testing.T) {
	for _, mode := range []string{"append", "replace", "review"} {
		t.Run(mode, func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			project.Studio.Plans = []StoryPlan{{ID: "transaction-plan", Level: "chapter", Title: "A dry ledger", StartChapter: project.Chapters[0].Number, EndChapter: project.Chapters[0].Number, Status: "confirmed", Choice: "Check the records"}}
			insertProject(t, engine, project)
			draft := "Lin Wu wraps the ledger in cloth."
			review := jsonText(WritingReview{Summary: "The action is located.", Memories: []StoryMemory{{Kind: "fact", Title: "Ledger wrapped", Content: draft, Quote: draft}}})
			server, calls := sequenceModel(t, []string{writingCompositionFixture(), draft, review})
			config := defaultProvider()
			config.BaseURL, config.Model, config.ContextChars = server.URL, "writing-transaction", 64000
			configureModel(t, engine, config)
			session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": project.ID})
			action, policy := "continue", "auto"
			if mode == "replace" {
				action = "rewrite"
			}
			if mode == "review" {
				policy = "review"
			}
			args := map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "session_id": session.ID, "request_id": "one-writing-transaction", "action": action, "commit_policy": policy, "instruction": "Write the ledger scene"}
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, job.Status == "done" && len(job.ChangeSets) == 1 && len(job.ChangeSets[0].Operations) == 3, "The compound writing candidate was not produced: "+job.Error)
			saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
			if policy == "review" {
				assert(t, !job.Writing.Saved && saved.Revision == project.Revision && saved.Chapters[0].Body == project.Chapters[0].Body && len(saved.Studio.Memories) == 0 && len(saved.Studio.Plans[0].SceneCards) == 0, "Review mode wrote prose or derived records")
				return
			}
			body := draft
			if mode == "append" {
				body = project.Chapters[0].Body + "\n\n" + draft
			}
			assert(t, job.Writing.Saved && saved.Chapters[0].Body == body && saved.Revision == project.Revision+1 && len(saved.Studio.Plans[0].SceneCards) == 1, "Prose and scene cards were not saved in one revision")
			assert(t, len(saved.Studio.Memories) == 1 && saved.Studio.Memories[0].Status == "proposed" && saved.Studio.Memories[0].Epistemic == "inferred" && saved.Studio.Memories[0].SourceHash == hashText(body), "The derived state lost its candidate identity or source version")
			again := invoke[Job](t, engine, "start_ai", args)
			reloaded := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
			assert(t, again.ID == job.ID && calls.Load() == 3 && reloaded.Revision == saved.Revision && reloaded.Chapters[0].Body == body, "Retry duplicated writing or its save transaction")
			invoke[any](t, engine, "undo_changeset", map[string]any{"project_id": project.ID, "changeset_id": job.ChangeSets[0].ID, "revision": saved.Revision})
			undone := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
			assert(t, undone.Chapters[0].Body == project.Chapters[0].Body && len(undone.Studio.Memories) == 0 && len(undone.Studio.Plans[0].SceneCards) == 0, "Undo failed to restore the complete prose, memory and plan boundary")
		})
	}
}

// TestAutomaticWritingSaveFailureRetainsCandidate verifies a database failure leaves a recoverable whole candidate.
func TestAutomaticWritingSaveFailureRetainsCandidate(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	_, err := engine.store.db.Exec("CREATE TRIGGER fail_writing_save BEFORE UPDATE ON projects BEGIN SELECT RAISE(ABORT,'writer transaction fixture'); END")
	assert(t, err == nil, fmt.Sprint(err))
	draft := "Lin Wu wraps the ledger in cloth."
	server, _ := sequenceModel(t, []string{writingCompositionFixture(), draft, `{"summary":"Ready.","issues":[],"memories":[]}`})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "writing-save-failure", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "continue", "commit_policy": "auto", "instruction": "Write the ledger scene"}))
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, job.Status == "error" && !job.Writing.Saved && job.Writing.Draft == draft && len(job.ChangeSets) == 1 && job.ChangeSets[0].Status != ChangeSetApplied && saved.Revision == project.Revision && saved.Chapters[0].Body == project.Chapters[0].Body, "A rejected save lost its candidate or partially changed the story")
	_, err = engine.store.db.Exec("DROP TRIGGER fail_writing_save")
	assert(t, err == nil, fmt.Sprint(err))
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": job.ChangeSets[0].ID})
	invoke[any](t, engine, "apply_changeset", map[string]any{"project_id": project.ID, "changeset_id": preview.ChangeSet.ID, "revision": preview.ChangeSet.PreviewRevision, "review_token": preview.ChangeSet.ReviewToken})
	recovered := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, recovered.Chapters[0].Body == project.Chapters[0].Body+"\n\n"+draft, "The retained writing candidate could not be recovered")
}

// TestWritingResultToolFallback records one rejected native request and reuses the compatible stage protocol.
func TestWritingResultToolFallback(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	var calls, native atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		calls.Add(1)
		if strings.Contains(body, `"tools":`) {
			native.Add(1)
			http.Error(w, `{"error":{"message":"Tools are not supported"}}`, http.StatusBadRequest)
			return
		}
		switch {
		case strings.Contains(body, "[Writing composition stage]"):
			fmt.Fprint(w, streamPayload(writingCompositionFixture()))
		case strings.Contains(body, "[Writing review stage]"):
			fmt.Fprint(w, streamPayload(`{"summary":"Ready.","issues":[],"memories":[]}`))
		default:
			fmt.Fprint(w, streamPayload("Lin Wu wraps the ledger in cloth."))
		}
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "writing-result-fallback", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "continue", "instruction": "Write the ledger scene"}))
	assert(t, job.Status == "done" && calls.Load() == 4 && native.Load() == 1 && job.Calls == 4 && job.Writing.Calls == 4 && len(job.Writing.Stages) == 4, "Native fallback was unreachable, repeated or undercounted: "+job.Error)
}

// TestWritingNativeStageResults verifies native result transport and rejects a provider-ignored review enum.
func TestWritingNativeStageResults(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			insertProject(t, engine, project)
			draft := "Lin Wu wraps the ledger in cloth."
			var calls atomic.Int32
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				body := string(readBodyForStudioTest(t, r))
				calls.Add(1)
				name, arguments := compositionResultToolName, writingCompositionFixture()
				if strings.Contains(body, "[Writing review stage]") {
					name = reviewResultToolName
					review := map[string]any{"summary": "Review located.", "issues": []WritingIssue{}, "needs_revision": false, "memories": []StoryMemory{}}
					if !valid {
						review["issues"] = []WritingIssue{{Kind: "invented-enum", Scope: "prose", Quote: draft, Explanation: "The provider ignored its declared enum.", Suggestion: "Check the submitted fields."}}
					}
					arguments = jsonText(review)
				} else if !strings.Contains(body, "[Writing composition stage]") {
					fmt.Fprint(w, streamPayload(draft))
					return
				}
				packet := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": newID(), "type": "function", "function": map[string]any{"name": name, "arguments": arguments}}}}, "finish_reason": "tool_calls"}}}
				fmt.Fprint(w, "data: "+jsonText(packet)+"\n\ndata: [DONE]\n\n")
			})
			config := defaultProvider()
			config.BaseURL, config.Model, config.ContextChars = server.URL, "writing-native-stage", 64000
			configureModel(t, engine, config)
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "continue", "commit_policy": "auto", "instruction": "Write the ledger scene"}))
			assert(t, job.Status == "done" && calls.Load() == 3 && job.Writing.Draft == draft, "Native stage results did not reach the writing workflow: "+job.Error)
			assert(t, job.Writing.Saved == valid && (job.Writing.ReviewStatus == "complete") == valid, "A provider-ignored native field was silently accepted or a valid result was lost")
		})
	}
}

// TestWritingLanguageConstraintChecksOnlyNewProse bounds local language repair and preserves older author text.
func TestWritingLanguageConstraintChecksOnlyNewProse(t *testing.T) {
	for _, result := range []string{"repaired", "unresolved", "old_only"} {
		t.Run(result, func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			project.Studio.Language = "zh-CN"
			project.Chapters[0].Body = "旧稿只见林雾站在门边。"
			insertProject(t, engine, project)
			first, clean := "只见林雾把账册裹进布里。", "林雾把账册裹进布里，抹掉桌上的水。"
			if result == "old_only" {
				first = clean
			}
			var drafts atomic.Int32
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				body := string(readBodyForStudioTest(t, r))
				switch {
				case strings.Contains(body, "[Writing composition stage]"):
					fmt.Fprint(w, streamPayload(writingCompositionFixture()))
				case strings.Contains(body, "[Writing review stage]"):
					fmt.Fprint(w, streamPayload(`{"summary":"已按原文核对。","issues":[],"memories":[]}`))
				default:
					text := first
					if drafts.Add(1) > 1 && result == "repaired" {
						text = clean
					}
					fmt.Fprint(w, streamPayload(text))
				}
			})
			config := defaultProvider()
			config.BaseURL, config.Model, config.ContextChars = server.URL, "writing-language-fixture", 64000
			configureModel(t, engine, config)
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "continue", "commit_policy": "auto", "instruction": "写完收好账册的场景"}))
			assert(t, job.Status == "done" && drafts.Load() <= 2, "The language constraint exceeded one repair or lost the candidate: "+job.Error)
			saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
			if result == "unresolved" {
				assert(t, !job.Writing.Saved && job.Writing.ReviewStatus == "needs_revision" && len(job.Writing.Review.Issues) > 0 && saved.Chapters[0].Body == project.Chapters[0].Body, "Unresolved explicit wording was silently saved")
				return
			}
			assert(t, job.Writing.Saved && saved.Chapters[0].Body == project.Chapters[0].Body+"\n\n"+clean, "Old prose blocked the new passage or a repaired passage was lost")
			if result == "old_only" {
				assert(t, drafts.Load() == 1 && job.Writing.Revisions == 0, "An old banned phrase forced an unnecessary revision")
			}
		})
	}
}

// writingSceneFixture supplies an executable participant choice for stage and checkpoint regressions.
func writingSceneFixture() WritingScene {
	return WritingScene{POV: "Lin Wu", Location: "The gate", Goal: "Keep the records dry", Choice: "Check the records before leaving", Action: "Wrap the ledger in cloth", Outcome: "The ledger remains safe and the visit is delayed", Characters: []WritingSceneCharacter{{Name: "Lin Wu", Want: "Keep a promise", Response: "Refuse to leave the ledger behind"}}}
}

// TestWritingRevisionResumeDoesNotReuseOldDraftAsPrefix protects cancellation before a revision's first token.
func TestWritingRevisionResumeDoesNotReuseOldDraftAsPrefix(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Chapters[0].Synopsis = "Check the old records."
	insertProject(t, engine, p)
	first, revised := "She opens the gate at once.", "She checks the records before opening the gate."
	entered := make(chan struct{}, 1)
	var drafts, reviews atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		if strings.Contains(body, "[Writing composition stage]") {
			fmt.Fprint(w, streamPayload(writingCompositionFixture()))
			return
		}
		if strings.Contains(body, "[Writing review stage]") {
			if reviews.Add(1) == 1 {
				fmt.Fprint(w, streamPayload(jsonText(WritingReview{Summary: "Slow the transition.", NeedsRevision: true, Issues: []WritingIssue{{Kind: "reading", Scope: "prose", Quote: first, Explanation: "A brief action would clarify the transition.", Suggestion: "Check the records first."}}})))
				return
			}
			fmt.Fprint(w, streamPayload(`{"summary":"Ready.","issues":[],"memories":[]}`))
			return
		}
		switch drafts.Add(1) {
		case 1:
			fmt.Fprint(w, streamPayload(first))
		case 2:
			entered <- struct{}{}
			<-r.Context().Done()
		default:
			assert(t, !strings.Contains(body, "<interrupted_candidate>"), "An untouched old draft was promoted to a revision prefix")
			fmt.Fprint(w, streamPayload(revised))
		}
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "revision-prefix", 64000
	configureModel(t, engine, config)
	args := map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Open the gate"}
	job := invoke[Job](t, engine, "start_ai", args)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Revision did not start")
	}
	invoke[map[string]bool](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
	stopped := finishJob(t, engine, job)
	assert(t, stopped.Writing.Draft == first && stopped.Writing.Continuation == "" && stopped.Writing.Revisions == 1, "Cancellation lost the original candidate or revision count")
	args["resume_job_id"] = job.ID
	resumed := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
	assert(t, resumed.Status == "done" && resumed.Text == revised && resumed.Writing.Calls == 6 && resumed.Writing.Revisions == 1, "Revision resume duplicated an old draft or restarted the editing cycle: "+resumed.Error)
}

// TestWriterSeparatesCoordinatorProse keeps planning commentary out of the novel and still composes first.
func TestWriterSeparatesCoordinatorProse(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	coordinator := "I will inspect the current scene before drafting."
	server, calls := sequenceModel(t, []string{coordinator + "\n" + `{"tool":"writer","query":"Continue the scene"}`, writingCompositionFixture(), "She checks the old ledger.", `{"summary":"Ready.","issues":[],"memories":[]}`, `{"tool":"finish","text":"Review the proposed scene."}`})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "coordinator-boundary", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "agent", "instruction": "Continue"}))
	assert(t, job.Status == "done" && calls.Load() == 5 && job.Writing.Composition != nil, job.Error)
	assert(t, job.Writing.Draft == "She checks the old ledger." && !strings.Contains(job.Writing.CandidateBody, coordinator), "Coordinator commentary was treated as an interrupted novel prefix")
}

// TestWritingPreviewEditCannotKeepUnsupportedMemory checks candidate edits and evidence commit in the real preview path.
func TestWritingPreviewEditCannotKeepUnsupportedMemory(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Chapters[0].Synopsis = "Repair the window."
	insertProject(t, engine, p)
	draft := "Lin Wu repairs the leaking window."
	review := jsonText(WritingReview{Summary: "Ready.", Memories: []StoryMemory{{Kind: "fact", Title: "Window repaired", Content: draft, Quote: draft}}})
	server, _ := sequenceModel(t, []string{writingCompositionFixture(), draft, review})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "edited-memory", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Repair the window"}))
	assert(t, job.Status == "done" && len(job.ChangeSets) == 1, job.Error)
	cs := job.ChangeSets[0]
	ops := append([]ChangeOperation{}, cs.Operations...)
	for i := range ops {
		if ops[i].TargetKind == TargetChapter {
			ops[i].AfterValue = rawMessage(chapterProsePatch{Body: "Lin Wu leaves the window untouched."})
		}
	}
	reject(t, engine, "preview_changeset", map[string]any{"project_id": p.ID, "changeset_id": cs.ID, "operations": ops})
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, saved.Chapters[0].Body == p.Chapters[0].Body && len(saved.Studio.Memories) == 0, "An unsupported memory or edited candidate entered saved facts")
}

// TestWritingReusesConfirmedComposition keeps confirmed cards for an empty draft or a deliberate chapter rewrite.
func TestWritingReusesConfirmedComposition(t *testing.T) {
	for _, action := range []string{"continue", "rewrite"} {
		t.Run(action, func(t *testing.T) {
			engine := testEngine(t)
			p := testProject()
			if action == "continue" {
				p.Chapters[0].Body = ""
			}
			p.Chapters[0].Synopsis = "Check the tide table before choosing a route."
			p.Studio.Plans = []StoryPlan{{ID: "scene-plan", Level: "chapter", Title: "Check the tide", StartChapter: p.Chapters[0].Number, EndChapter: p.Chapters[0].Number, Status: "confirmed", Goal: "Keep the records dry", SceneCards: []WritingScene{writingSceneFixture()}}}
			insertProject(t, engine, p)
			server, calls := sequenceModel(t, []string{"Lin Wu checks the tide table.", `{"summary":"Ready.","issues":[],"memories":[]}`})
			config := defaultProvider()
			config.BaseURL, config.Model, config.ContextChars = server.URL, "reuse-composition", 64000
			configureModel(t, engine, config)
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": action, "instruction": "Use the confirmed scene"}))
			assert(t, job.Status == "done" && calls.Load() == 2 && job.Writing.Composition.SceneCards[0].Action == writingSceneFixture().Action, job.Error)
			assert(t, job.Writing.Stages[0].Name == "drafting", "Complete confirmed scene cards triggered a redundant composition")
		})
	}
}

// TestWritingRecomposeReplacesConfirmedSceneCards honors a fresh composition request through the normal entry point.
func TestWritingRecomposeReplacesConfirmedSceneCards(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	project.Studio.Plans = []StoryPlan{{ID: "recompose-plan", Level: "chapter", Title: "The old approach", StartChapter: project.Chapters[0].Number, EndChapter: project.Chapters[0].Number, Status: "confirmed", Goal: "Keep the records dry", SceneCards: []WritingScene{writingSceneFixture()}}}
	insertProject(t, engine, project)
	composition := WritingComposition{Intent: "Find another way to keep the promise", Approach: "Ask the keeper to carry the ledger", SceneCards: []WritingScene{writingSceneFixture()}}
	composition.SceneCards[0].Action = "Ask the keeper to carry the ledger"
	draft := "Lin Wu asks the keeper to carry the ledger."
	server, calls := sequenceModel(t, []string{jsonText(composition), draft, `{"summary":"The new approach is located.","issues":[],"memories":[]}`})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "recompose-confirmed-scene", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "rewrite", "intent_hint": "writing_recompose", "commit_policy": "review", "instruction": "Reconsider the scene from each character's wishes"}))
	assert(t, job.Status == "done" && calls.Load() == 3 && job.Writing.Stages[0].Name == "composing" && job.Writing.Composition.SceneCards[0].Action == composition.SceneCards[0].Action && job.Writing.Draft == draft, "A recompose request reused the saved scene cards: "+job.Error)
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, !job.Writing.Saved && saved.Chapters[0].Body == project.Chapters[0].Body && saved.Studio.Plans[0].SceneCards[0].Action == writingSceneFixture().Action, "Recomposition replaced saved prose or its confirmed plan before review")
}

// TestWritingRetrievesCompositionDependencies recovers an old promise beyond the complete previous chapter.
func TestWritingRetrievesCompositionDependencies(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Chapters[0].Number, p.Chapters[0].Body = 3, "She waits by the gate."
	old := NewChapter(1, "The old promise")
	old.Body = "COPPER_LEDGER_PROMISE: Keep the copper ledger dry until Old Chen returns."
	previous := NewChapter(2, "A quiet night")
	previous.Body = "Rain rattles on the roof."
	p.Chapters = append(p.Chapters, old, previous)
	insertProject(t, engine, p)
	composition := jsonText(WritingComposition{Intent: "Follow the old obligation", Approach: "Check the ledger before leaving", SceneCards: []WritingScene{writingSceneFixture()}, EvidenceRequests: []string{"copper ledger"}, RevealNotes: []string{"The promise is private"}})
	server, _ := sequenceModel(t, []string{composition, "She wraps the copper ledger in cloth.", `{"summary":"The old obligation is retained.","issues":[],"memories":[]}`})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "dependency-writing", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Open the gate"}))
	assert(t, job.Status == "done", job.Error)
	found := false
	for _, item := range job.Writing.SourcePrompt.Context {
		if item.SourceID == old.ID && strings.Contains(item.Text, "COPPER_LEDGER_PROMISE") {
			found = item.Required && item.SourceHash == hashText(old.Body) && strings.Contains(item.Reason, "copper ledger")
		}
	}
	assert(t, found && strings.Contains(jsonText(job.Writing.Stages[1].Prompt.Messages), "COPPER_LEDGER_PROMISE"), "The requested old promise was not frozen into the actual writer input")
}

// TestWritingMissingEvidenceStaysUncertain retains a visible gap rather than fabricating a rule.
func TestWritingMissingEvidenceStaysUncertain(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	composition := jsonText(WritingComposition{Intent: "Keep the question open", Approach: "Ask before using the device", SceneCards: []WritingScene{writingSceneFixture()}, EvidenceRequests: []string{"ZXQ_UNDOCUMENTED_DEVICE"}})
	server, _ := sequenceModel(t, []string{composition, "She leaves the device untouched.", `{"summary":"The uncertainty remains.","issues":[],"memories":[]}`})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "missing-evidence", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Inspect the device"}))
	assert(t, job.Status == "done" && len(job.Writing.Composition.OpenQuestions) == 1 && strings.Contains(jsonText(job.Writing.Stages[1].Prompt), "keep uncertain"), job.Error)
}

// TestWritingRejectsInventedCompositionIDs refuses untraceable evidence before generating prose.
func TestWritingRejectsInventedCompositionIDs(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	composition := jsonText(WritingComposition{Intent: "Act", Approach: "Use the supplied source", SceneCards: []WritingScene{writingSceneFixture()}, EvidenceIDs: []string{"C:invented"}})
	server, calls := sequenceModel(t, []string{composition})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "invalid-composition", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Continue"}))
	assert(t, job.Status == "error" && calls.Load() == 1 && len(job.Proposals) == 0 && strings.Contains(job.Error, "unavailable"), "Invented composition evidence reached prose or storage")
}

// TestWritingDropsSelfChapterEvidence lets a composition cite the chapter being written, which is
// its subject rather than evidence, without failing the task as invented evidence.
func TestWritingDropsSelfChapterEvidence(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Chapters[0].Body = ""
	insertProject(t, engine, p)
	self := "C:" + p.Chapters[0].ID
	composition := jsonText(WritingComposition{Intent: "Act", Approach: "Use the supplied source", SceneCards: []WritingScene{writingSceneFixture()}, EvidenceIDs: []string{self}})
	server, calls := sequenceModel(t, []string{composition, "She leaves the device untouched.", `{"summary":"Ready.","issues":[],"memories":[]}`})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "self-evidence", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Continue"}))
	assert(t, job.Status == "done" && calls.Load() == 3 && len(job.Proposals) == 1, "A self-citation of the chapter failed the task: "+job.Error)
	assert(t, !contains(job.Writing.Composition.EvidenceIDs, self), "The uncontextualized self-citation stayed in the composition evidence")
}

// TestRevisionEvidenceFailureSalvagesReviewedCandidate keeps the reviewed draft as a candidate when the
// revision round cites invented evidence, instead of failing the task and losing that draft.
func TestRevisionEvidenceFailureSalvagesReviewedCandidate(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	review := jsonText(WritingReview{Summary: "Slow the reveal.", NeedsRevision: true, Issues: []WritingIssue{{Kind: "reading", Scope: "composition", Quote: "Lin Wu found a letter.", Explanation: "The action needs grounding.", Suggestion: "Check the records first."}}})
	invented := jsonText(WritingComposition{Intent: "Act", Approach: "Use the supplied source", SceneCards: []WritingScene{writingSceneFixture()}, EvidenceIDs: []string{"C:invented"}})
	server, calls := sequenceModel(t, []string{writingCompositionFixture(), "She decided to visit the lighthouse again.", review, invented})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "revision-evidence", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Continue"}))
	assert(t, job.Status == "done" && calls.Load() == 4 && len(job.Proposals) == 1, "Invented revision evidence discarded the reviewed draft: "+job.Error)
	assert(t, job.Writing.Review != nil && strings.Contains(job.Writing.Review.Warning, "unavailable") && strings.Contains(job.Proposals[0].Content, "lighthouse again"), "The preserved candidate lost its draft or the reason it was kept")
}

// TestWritingRevisionScopes routes source and motivation problems back to composition once.
func TestWritingRevisionScopes(t *testing.T) {
	for _, scope := range []string{"composition", "context", "plan"} {
		t.Run(scope, func(t *testing.T) {
			engine := testEngine(t)
			p := testProject()
			insertProject(t, engine, p)
			first, revised := "She leaves without a reason.", "She stays to honor the promise."
			review := jsonText(WritingReview{Summary: "Check the choice.", NeedsRevision: true, Issues: []WritingIssue{{Kind: "reading", Scope: scope, Quote: first, Explanation: "The obligation has not been considered.", Suggestion: "Reconsider her reason before polishing."}}})
			responses := []string{writingCompositionFixture(), first, review}
			if scope != "plan" {
				responses = append(responses, writingCompositionFixture(), revised, `{"summary":"Ready.","issues":[],"memories":[]}`)
			}
			server, calls := sequenceModel(t, responses)
			config := defaultProvider()
			config.BaseURL, config.Model, config.ContextChars = server.URL, "scoped-revision", 64000
			configureModel(t, engine, config)
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Continue"}))
			assert(t, job.Status == "done", job.Error)
			if scope == "plan" {
				assert(t, calls.Load() == 3 && job.Writing.Review.NeedsRevision && job.Writing.Draft == first, "A global plan objection silently rewrote the story")
			} else {
				assert(t, calls.Load() == 6 && job.Writing.Revisions == 1 && job.Writing.Draft == revised && job.Writing.Stages[3].Name == "composing", "A composition issue was treated as a local wording edit")
			}
		})
	}
}

// TestWritingCompositionCheckpointResumesWithoutReplanning protects a completed plan when drafting is interrupted.
func TestWritingCompositionCheckpointResumesWithoutReplanning(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Studio.Plans = []StoryPlan{{ID: "resume-scene-plan", Level: "chapter", Title: "Saved scene", StartChapter: p.Chapters[0].Number, EndChapter: p.Chapters[0].Number, Status: "confirmed", SceneCards: []WritingScene{writingSceneFixture()}}}
	insertProject(t, engine, p)
	entered := make(chan struct{}, 1)
	var plans, drafts atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		switch {
		case strings.Contains(body, "[Writing composition stage]"):
			plans.Add(1)
			fmt.Fprint(w, streamPayload(writingCompositionFixture()))
		case strings.Contains(body, "[Writing review stage]"):
			fmt.Fprint(w, streamPayload(`{"summary":"Ready.","issues":[],"memories":[]}`))
		default:
			if drafts.Add(1) == 1 {
				entered <- struct{}{}
				<-r.Context().Done()
				return
			}
			fmt.Fprint(w, streamPayload("She checks the dry ledger."))
		}
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "resume-composition", 64000
	configureModel(t, engine, config)
	args := map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "intent_hint": "writing_recompose", "instruction": "Continue"}
	job := invoke[Job](t, engine, "start_ai", args)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Drafting did not start")
	}
	invoke[map[string]bool](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
	stopped := finishJob(t, engine, job)
	assert(t, stopped.Writing.Composition != nil && stopped.Writing.Draft == "" && stopped.Status == "cancelled", "The completed composition was lost")
	args["resume_job_id"] = job.ID
	resumed := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
	assert(t, resumed.Status == "done" && plans.Load() == 1 && resumed.Writing.Calls == 4 && resumed.Calls == 2, resumed.Error)
}

// TestWritingCompositionCopiesAndValidation keeps snapshots isolated and rejects malformed stage responses.
func TestWritingCompositionCopiesAndValidation(t *testing.T) {
	original := &WritingRun{Composition: &WritingComposition{Intent: "A quiet visit", Approach: "Let both people choose", RevealNotes: []string{"private"}}, Review: &WritingReview{Issues: []WritingIssue{{EvidenceIDs: []string{"C:one"}}}}}
	copy := copyWritingRun(original)
	copy.Composition.RevealNotes[0] = "public"
	copy.Review.Issues[0].EvidenceIDs[0] = "C:two"
	assert(t, original.Composition.RevealNotes[0] == "private" && original.Review.Issues[0].EvidenceIDs[0] == "C:one", "A task snapshot shares mutable composition state")
	for _, text := range []string{"ordinary prose", `{"intent":"Only a label"}`, `{"intent":"A","approach":"B","evidence_requests":["1","2","3","4","5"]}`} {
		_, err := parseWritingComposition(text)
		assert(t, err != nil, "An invalid composition was accepted")
	}
}

// TestPlanFlexibilityRoundTrips keeps old confirmation semantics and exposes revisable questions to writing.
func TestPlanFlexibilityRoundTrips(t *testing.T) {
	p := testProject()
	p.Studio.Plans = []StoryPlan{{ID: "book", Level: "book", Title: "A long life", Status: "confirmed", StartChapter: 1, Flexibility: "flexible", OpenQuestions: []string{"Will she keep the old promise?"}}}
	assert(t, validateStudio(&p.Studio) == nil, "Valid plan flexibility failed")
	prompt, err := prepareAI(&p, AIRequest{ProjectID: p.ID, ChapterID: p.Chapters[0].ID, Action: "continue", Instruction: "Continue"}, 24000)
	assert(t, err == nil && strings.Contains(jsonText(prompt), "Will she keep the old promise?"), "The open planning question disappeared")
	p.Studio.Plans[0].Flexibility = ""
	assert(t, validateStudio(&p.Studio) == nil, "An older confirmed plan became invalid")
	p.Studio.Plans[0].Flexibility = "always-ignore"
	assert(t, validateStudio(&p.Studio) != nil, "An unknown planning contract was accepted")
}

// TestWritingContextPinsChoicesAndPredecessor protects long actions and chapter adjacency from summary limits.
func TestWritingContextPinsChoicesAndPredecessor(t *testing.T) {
	p := testProject()
	p.Chapters[0].Number = 2
	previous := NewChapter(1, "The previous scene")
	previous.Body = strings.Repeat("A quiet exchange. ", 180) + "PREVIOUS_RELATIONSHIP_CHANGE"
	p.Chapters = append(p.Chapters, previous)
	p.Studio.Plans = []StoryPlan{
		{ID: "book", Level: "book", Title: "Book", StartChapter: 1, Status: "confirmed", Goal: "A long life"},
		{ID: "late-id", ParentID: "book", Level: "chapter", Title: "Chapter two", StartChapter: 2, EndChapter: 2, Status: "confirmed", Choice: strings.Repeat("Choose carefully. ", 160) + "COMPLETE_CHARACTER_CHOICE"},
	}
	prompt, err := prepareAI(&p, AIRequest{ChapterID: p.Chapters[0].ID, Action: "continue", Instruction: "Continue"}, 24000)
	assert(t, err == nil, fmt.Sprint(err))
	text := jsonText(prompt.Messages)
	assert(t, strings.Contains(text, "COMPLETE_CHARACTER_CHOICE") && strings.Contains(text, previous.Body), "A necessary action or preceding scene was truncated")
	for _, item := range prompt.Context {
		if item.SourceID == previous.ID {
			assert(t, item.Required && item.SourceHash == hashText(previous.Body), "The predecessor lost its required identity or revision")
			return
		}
	}
	t.Fatal("The predecessor is absent from the inspectable context")
}

// TestWritingHandoffRetainsSelectedFullEvidence checks an early source survives newer unrelated steps.
func TestWritingHandoffRetainsSelectedFullEvidence(t *testing.T) {
	p := testProject()
	parent := Job{ID: "parent", ProjectID: p.ID, ChapterID: p.Chapters[0].ID, Instruction: "Write the scene", Steps: []AgentStep{{ID: "source", Tool: "get_source_detail", Status: "done", Detail: "short trace", FullDetail: strings.Repeat("证据", 1500) + "DECISIVE_SOURCE_CONDITION"}}}
	for i := 0; i < 8; i++ {
		parent.Steps = append(parent.Steps, AgentStep{ID: fmt.Sprint(i), Tool: "plan", Status: "done", Detail: "Unrelated plan"})
	}
	request := scopedAgentRequest(&p, parent, agentStepEnvelope{Tool: "writer", Query: "Continue", EvidenceIDs: []string{"source"}})
	assert(t, len(request.Retrieved) == 1 && request.Retrieved[0].Required && strings.Contains(request.Retrieved[0].Text, "DECISIVE_SOURCE_CONDITION") && !strings.Contains(request.Retrieved[0].Text, "Unrelated plan"), "The handoff dropped or arbitrarily replaced selected source evidence")
	assert(t, validateHandoffEvidence(parent, []string{"missing"}) != nil && validateHandoffEvidence(parent, []string{"source"}) == nil, "An unavailable source selection was silently accepted")
}

// TestWritingStagesAndMemoryCommit exercises prose generation, a real review request and atomic author acceptance.
func TestWritingStagesAndMemoryCommit(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	draft := "Lin Wu repairs the leaking window before touching the old grave."
	requests := make(chan string, 4)
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		requests <- body
		if strings.Contains(body, "[Writing composition stage]") {
			fmt.Fprint(w, streamPayload(writingCompositionFixture()))
			return
		}
		if strings.Contains(body, "[Writing review stage]") {
			fmt.Fprint(w, streamPayload(jsonText(map[string]any{"summary": "The action has a concrete purpose.", "issues": []WritingIssue{}, "memories": []StoryMemory{{Kind: "fact", Entity: "Lin Wu", Title: "Window", Content: "Lin Wu repaired the window.", Quote: "Lin Wu repairs the leaking window"}}})))
			return
		}
		fmt.Fprint(w, streamPayload(draft))
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "writing-fixture", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Repair the window"}))
	assert(t, job.Status == "done" && job.Calls == 3 && job.Text == draft && job.Writing != nil && job.Writing.Review.Structured, job.Error)
	assert(t, len(job.Writing.Stages) == 3 && len(job.ChangeSets) == 1 && len(job.ChangeSets[0].Operations) == 2, "The writing stages and memory candidates were not linked")
	assert(t, strings.Contains(<-requests, "chapter_brief") && strings.Contains(<-requests, "selected_composition") && strings.Contains(<-requests, draft), "The actual provider requests lost the brief, composition or candidate")
	assert(t, job.Writing.CandidateBody == p.Chapters[0].Body+"\n\n"+draft, "The complete evaluation candidate was not frozen")
	before := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, before.Chapters[0].Body == p.Chapters[0].Body && len(before.Studio.Memories) == 0, "Unaccepted writing changed the story")
	preview := invoke[struct {
		Project   Project   `json:"project"`
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": p.ID, "revision": before.Revision, "changeset_id": job.ChangeSets[0].ID})
	accepted := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "apply_changeset", map[string]any{"project_id": p.ID, "revision": before.Revision, "changeset_id": preview.ChangeSet.ID, "review_token": preview.ChangeSet.ReviewToken})
	assert(t, accepted.Project.Chapters[0].Body == p.Chapters[0].Body+"\n\n"+draft && len(accepted.Project.Studio.Memories) == 1, "Prose and memory were not committed together")
	memory := accepted.Project.Studio.Memories[0]
	assert(t, memory.SourceHash == hashText(accepted.Project.Chapters[0].Body) && memoryCurrent(&accepted.Project, memory), "The accepted memory points to the old chapter revision")
}

// TestWritingRevisionRunsAtMostOnce keeps a repeated editorial objection from causing an unbounded loop.
func TestWritingRevisionRunsAtMostOnce(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	first, revised := "Lin Wu moved the stone with unexplained power.", "Lin Wu fetched a lever and moved the stone."
	review := func(quote string) string {
		return jsonText(map[string]any{"summary": "Consider the action's limits.", "issues": []WritingIssue{{Kind: "continuity", Quote: quote, Explanation: "Check the physical execution.", Suggestion: "Show the available preparation."}}, "needs_revision": true})
	}
	server, calls := sequenceModel(t, []string{writingCompositionFixture(), first, review(first), revised, review(revised)})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "revision-fixture", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Move the stone"}))
	assert(t, job.Status == "done" && calls.Load() == 5 && job.Calls == 5 && job.Text == revised, "Revision duplicated the original draft or exceeded one cycle: "+job.Error)
	assert(t, len(job.Proposals) == 1 && job.Proposals[0].Content == revised && len(job.Writing.Stages) == 5, "An obsolete draft was staged alongside its revision")
}

// TestWritingReviewCancellationResumesTheCandidate avoids generating the prose a second time after review interruption.
func TestWritingReviewCancellationResumesTheCandidate(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	draft := "Lin Wu keeps the grave records in a dry drawer."
	entered := make(chan struct{}, 1)
	var calls atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		index := calls.Add(1)
		if strings.Contains(body, "[Writing composition stage]") {
			fmt.Fprint(w, streamPayload(writingCompositionFixture()))
			return
		}
		if strings.Contains(body, "[Writing review stage]") {
			if index == 3 {
				entered <- struct{}{}
				<-r.Context().Done()
				return
			}
			fmt.Fprint(w, streamPayload(`{"summary":"The resumed review is complete.","issues":[],"memories":[]}`))
			return
		}
		fmt.Fprint(w, streamPayload(draft))
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "resume-writing-fixture", 64000
	configureModel(t, engine, config)
	args := map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Keep the records safe"}
	job := invoke[Job](t, engine, "start_ai", args)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Review did not start")
	}
	invoke[map[string]bool](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
	stopped := finishJob(t, engine, job)
	assert(t, stopped.Status == "cancelled" && stopped.Text == draft && stopped.Writing.Draft == draft && len(stopped.Proposals) == 0, "Review interruption lost the candidate")
	args["resume_job_id"] = stopped.ID
	resumed := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
	assert(t, resumed.Status == "done" && resumed.Text == draft && calls.Load() == 4 && resumed.Calls == 1 && resumed.Writing.Calls == 4 && len(resumed.Proposals) == 1, "Resuming review regenerated or duplicated the candidate: "+resumed.Error)
}

// TestWritingRejectsChangedSources retains the draft while refusing stale evidence at the end of review.
func TestWritingRejectsChangedSources(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		if strings.Contains(body, "[Writing composition stage]") {
			fmt.Fprint(w, streamPayload(writingCompositionFixture()))
			return
		}
		if strings.Contains(body, "[Writing review stage]") {
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, streamPayload(`{"summary":"Reviewed.","issues":[],"memories":[]}`))
			return
		}
		fmt.Fprint(w, streamPayload("Lin Wu closes the gate."))
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "stale-writing-fixture", 64000
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Close the gate"})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Review did not start")
	}
	chapter := p.Chapters[0]
	invoke[Project](t, engine, "save_chapter", map[string]any{"project_id": p.ID, "revision": p.Revision, "chapter_id": chapter.ID, "title": chapter.Title, "body": chapter.Body + " Author correction.", "synopsis": chapter.Synopsis, "status": chapter.Status})
	close(release)
	done := finishJob(t, engine, job)
	assert(t, done.Status == "error" && strings.Contains(done.Error, "changed") && done.Writing.Draft != "" && len(done.Proposals) == 0, "Stale source evidence produced a ready candidate")
}

// TestWritingReviewRequiresLocatedFeedback separates ungrounded criticism from a usable editing report.
func TestWritingReviewRequiresLocatedFeedback(t *testing.T) {
	p := testProject()
	review := parseWritingReview(&p, "Lin Wu fetched a lever.", `{"summary":"Check this.","needs_revision":true,"issues":[{"kind":"continuity","quote":"An invented flying scene","explanation":"Unsupported","suggestion":"Delete it"}]}`)
	assert(t, review.Structured && len(review.Issues) == 0 && !review.NeedsRevision && review.Warning != "", "Unlocated criticism triggered an automatic rewrite")
	plain := parseWritingReview(&p, "A scene", "Informal feedback")
	assert(t, !plain.Structured && plain.Warning != "", "Unstructured feedback was reported as a validated review")
}

// TestWritingReferencesKeepTheirIdentity prevents a craft passage from becoming an implicit story fact.
func TestWritingReferencesKeepTheirIdentity(t *testing.T) {
	p := testProject()
	source := Source{ID: "reference", Title: "Another book", Category: "craft", Content: "OtherHero hides a sword in another world."}
	p.Sources = append(p.Sources, source)
	_, compilationErr := wikiSourcePrompt(&p, AIRequest{SourceID: source.ID})
	assert(t, compilationErr != nil && strings.Contains(compilationErr.Error(), "/reference"), "A craft source was eligible to become this work's Wiki")
	request := AIRequest{ChapterID: p.Chapters[0].ID, Action: "continue", Instruction: "Continue", Retrieved: []ContextItem{{ID: "S:reference:1", Kind: "craft", Title: source.Title, Text: source.Content}}}
	prompt, err := prepareAI(&p, request, 24000)
	assert(t, err == nil, fmt.Sprint(err))
	for _, item := range prompt.Context {
		if item.ID == "S:reference:1" {
			assert(t, strings.Contains(item.Text, "belong to that reference") && item.SourceHash == hashText(source.Content), "The craft reference lost its source identity")
			return
		}
	}
	t.Fatal("Reference missing from the actual prompt")
}

// TestWritingMaterializationPreservesChoice protects the normal plan-to-editor entry point.
func TestWritingMaterializationPreservesChoice(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Studio.Plans = []StoryPlan{{ID: "chapter-plan", Level: "chapter", Title: "A measured decision", StartChapter: 2, EndChapter: 2, Goal: "Keep the grave intact", Conflict: "The elder wants a grave", Choice: "Offer a different site after checking the burial records", Consequence: "The elder asks about the old inscription", Status: "confirmed"}}
	insertProject(t, engine, p)
	saved := invoke[Project](t, engine, "create_planned_chapters", map[string]any{"project_id": p.ID, "revision": p.Revision})
	for _, chapter := range saved.Chapters {
		if chapter.Number == 2 {
			assert(t, strings.Contains(chapter.Synopsis, p.Studio.Plans[0].Choice), "Materialization dropped the character's decision")
			return
		}
	}
	t.Fatal("Chapter draft was not created")
}

// TestWritingStagesLargeCandidates protects chapter lengths supported by the existing editor.
func TestWritingStagesLargeCandidates(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	draft := strings.Repeat("The keeper checks the old burial records. ", 700)
	server, _ := sequenceModel(t, []string{writingCompositionFixture(), draft, `{"summary":"Review the complete scene.","issues":[],"memories":[]}`})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "long-writing-fixture", 160000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Write the scene"}))
	assert(t, job.Status == "done" && len(job.Proposals) == 1 && job.Proposals[0].Content == draft, "A supported long chapter was silently dropped: "+job.Error)
}

// TestWritingDraftCancellationKeepsVisibleText protects both fresh streams and resumed prefixes when providers return an empty error output.
func TestWritingDraftCancellationKeepsVisibleText(t *testing.T) {
	for _, prefix := range []string{"", "Previously generated opening. "} {
		t.Run(fmt.Sprint(len(prefix)), func(t *testing.T) {
			engine := testEngine(t)
			p := testProject()
			insertProject(t, engine, p)
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				body := string(readBodyForStudioTest(t, r))
				if strings.Contains(body, "[Writing composition stage]") {
					fmt.Fprint(w, streamPayload(writingCompositionFixture()))
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"A visible partial passage.\"}}]}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			})
			config := defaultProvider()
			config.BaseURL, config.Model, config.ContextChars = server.URL, "partial-writing-fixture", 64000
			configureModel(t, engine, config)
			args := map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "instruction": "Continue the scene"}
			if prefix != "" {
				old := Job{ID: newID(), ProjectID: p.ID, ChapterID: p.Chapters[0].ID, Action: "continue", Status: "interrupted", Text: prefix, Prompt: Prompt{SourceID: p.Chapters[0].ID, SourceHash: hashText(p.Chapters[0].Body)}}
				assert(t, engine.store.SaveRun(old.ID, p.ID, storedJob(&old)) == nil, "Could not seed the interrupted prefix")
				args["resume_job_id"] = old.ID
			}
			job := invoke[Job](t, engine, "start_ai", args)
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				job = invoke[Job](t, engine, "get_job", map[string]any{"job_id": job.ID})
				if strings.Contains(job.Text, "A visible partial passage.") {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			invoke[map[string]bool](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
			done := finishJob(t, engine, job)
			assert(t, done.Status == "cancelled" && done.Text == prefix+"A visible partial passage." && len(done.Proposals) == 0, "A cancelled writer erased or duplicated its visible text")
			stored, err := engine.loadStoredJob(job.ID)
			assert(t, err == nil && stored.Writing.Continuation == done.Text, "The durable stage lost its streamed prose prefix")
		})
	}
}

// TestWritingChapterChangeSetsAreReviewed covers the batch-operation path for existing and newly proposed chapters.
func TestWritingChapterChangeSetsAreReviewed(t *testing.T) {
	for _, create := range []bool{false, true} {
		t.Run(fmt.Sprint(create), func(t *testing.T) {
			engine := testEngine(t)
			p := testProject()
			insertProject(t, engine, p)
			draft := "Lin Wu checks the burial records before offering another site."
			chapterID := p.Chapters[0].ID
			ops := []ChangeOperation{{TargetKind: TargetChapter, TargetID: chapterID, OpType: OpAppendProse, Title: "Continue the scene", AfterValue: rawMessage(chapterProsePatch{Body: draft})}}
			if create {
				chapterID = ""
				title, number := "The next morning", uint32(2)
				ops = []ChangeOperation{{TargetKind: TargetChapter, TargetID: "new-chapter", OpType: OpUpsert, Title: title, AfterValue: rawMessage(chapterProsePatch{Title: &title, Number: &number, Body: draft})}}
			}
			response := jsonText(map[string]any{"tool": "propose_changes", "operations": ops})
			server, calls := sequenceModel(t, []string{response, `{"summary":"Reviewed against the saved story.","issues":[],"memories":[]}`, `{"tool":"finish","text":"Review the chapter changes."}`})
			config := defaultProvider()
			config.BaseURL, config.Model, config.ContextChars = server.URL, "batch-writing-fixture", 64000
			configureModel(t, engine, config)
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": chapterID, "action": "agent", "instruction": "Prepare the chapter prose"}))
			assert(t, job.Status == "done" && calls.Load() == 3 && job.Writing != nil && job.Writing.Mode == "changeset" && job.Writing.Review.Structured && len(job.ChangeSets) == 1, "The typed chapter path bypassed review: "+job.Error)
			before := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
			assert(t, len(before.Chapters) == len(p.Chapters) && before.Chapters[0].Body == p.Chapters[0].Body, "A reviewed batch was applied before acceptance")
			preview := invoke[struct {
				ChangeSet ChangeSet `json:"changeset"`
			}](t, engine, "preview_changeset", map[string]any{"project_id": p.ID, "revision": p.Revision, "changeset_id": job.ChangeSets[0].ID})
			accepted := invoke[struct {
				Project Project `json:"project"`
			}](t, engine, "apply_changeset", map[string]any{"project_id": p.ID, "revision": p.Revision, "changeset_id": preview.ChangeSet.ID, "review_token": preview.ChangeSet.ReviewToken})
			if create {
				assert(t, len(accepted.Project.Chapters) == len(p.Chapters)+1 && accepted.Project.Chapters[len(p.Chapters)].Body == draft, "The reviewed new chapter lost its content or identifier")
			} else {
				assert(t, accepted.Project.Chapters[0].Body == p.Chapters[0].Body+"\n\n"+draft, "The reviewed append changed the saved boundary")
			}
		})
	}
}

// TestAutomaticWritingReviewOutcome separates retained candidates from completed, automatically accepted reviews.
func TestAutomaticWritingReviewOutcome(t *testing.T) {
	for _, outcome := range []string{"http", "unstructured", "plan", "prose", "complete"} {
		t.Run(outcome, func(t *testing.T) {
			engine := testEngine(t)
			p := testProject()
			p.Chapters[0].Body, p.Chapters[0].Synopsis = "", "Check the ledger before opening the gate."
			insertProject(t, engine, p)
			draft := "Lin Wu checks the ledger before opening the gate."
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				body := string(readBodyForStudioTest(t, r))
				if strings.Contains(body, "[Writing composition stage]") {
					fmt.Fprint(w, streamPayload(writingCompositionFixture()))
					return
				}
				if !strings.Contains(body, "[Writing review stage]") {
					fmt.Fprint(w, streamPayload(draft))
					return
				}
				if outcome == "http" {
					http.Error(w, "Review unavailable", http.StatusBadRequest)
					return
				}
				if outcome == "unstructured" {
					fmt.Fprint(w, streamPayload("The scene needs another look."))
					return
				}
				review := WritingReview{Summary: "The ledger action is located.", Memories: []StoryMemory{{Kind: "fact", Title: "Ledger checked", Content: draft, Quote: draft}}}
				if outcome != "complete" {
					review.NeedsRevision = true
					review.Issues = []WritingIssue{{Kind: "continuity", Scope: outcome, Quote: draft, Explanation: "The selected choice needs author attention.", Suggestion: "Review the saved outline."}}
				}
				fmt.Fprint(w, streamPayload(jsonText(review)))
			})
			config := defaultProvider()
			config.BaseURL, config.Model, config.ContextChars = server.URL, "review-outcome", 64000
			configureModel(t, engine, config)
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "continue", "intent_hint": "write_from_outline", "commit_policy": "auto", "instruction": "Write this chapter"}))
			assert(t, job.Status == "done" && job.Writing != nil && job.Writing.Draft == draft, "The candidate was not retained: "+job.Error)
			saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
			if outcome == "complete" {
				assert(t, job.Writing.Saved && job.Writing.ReviewStatus == "complete" && saved.Chapters[0].Body == draft && len(saved.Studio.Memories) == 1, "A completed review failed to use automatic adoption")
				return
			}
			assert(t, !job.Writing.Saved && job.Writing.Phase == "needs_review" && job.Writing.ReviewStatus != "complete", "An incomplete review was reported as completed")
			if outcome == "http" || outcome == "unstructured" {
				expected := map[string]string{"http": "failed", "unstructured": "unstructured"}[outcome]
				assert(t, job.Writing.ReviewStatus == expected, "The review failure type was lost")
			}
			assert(t, saved.Chapters[0].Body == "" && len(saved.Studio.Memories) == 0 && len(job.ChangeSets) == 1, "Incomplete-review prose or memory was automatically adopted")
			assert(t, !strings.Contains(job.Progress, "Writing and review complete"), "The final receipt claimed a completed review")
			for _, operation := range job.ChangeSets[0].Operations {
				assert(t, operation.TargetKind != TargetMemory, "An incomplete review staged confirmable memory")
			}
		})
	}
}
