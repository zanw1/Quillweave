//go:build quillweave_whitebox

package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSpecialistHandoffCarriesParentEvidence verifies scoped context and actual child costs through normal Agent execution.
func TestSpecialistHandoffCarriesParentEvidence(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	project.Studio.Decisions = []StoryDecision{{ID: newID(), Title: "Keep the cost", Content: "MAGIC_COSTS_MEMORY", Status: "accepted"}}
	insertProject(t, engine, project)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": project.ID, "chapter_id": ""})
	var controlCalls, childCalls atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["tools"] == nil {
			childCalls.Add(1)
			text := jsonText(body)
			for _, required := range []string{"PARENT_AUTHOR_GOAL", "PRIOR_EVIDENCE", "MAGIC_COSTS_MEMORY", "SELECTED_PASSAGE", session.ID, project.ID, "project_revision", "parent_model_calls_remaining", "open questions"} {
				if !strings.Contains(text, required) {
					t.Errorf("Specialist handoff omitted %s", required)
				}
			}
			fmt.Fprint(w, streamPayload("Scoped conclusion with evidence and an open question."))
			return
		}
		text := `{"tool":"finish","text":"Review the specialist conclusion."}`
		switch controlCalls.Add(1) {
		case 1:
			text = `{"tool":"plan","text":"PRIOR_EVIDENCE"}`
		case 2:
			text = `{"tool":"researcher","query":"Investigate the letter"}`
		}
		fmt.Fprint(w, streamPayload(text))
	})
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	request := AIRequest{ProjectID: project.ID, ChapterID: project.Chapters[0].ID, SessionID: session.ID, Action: "agent", Instruction: "PARENT_AUTHOR_GOAL", WorkspaceContext: &WorkspaceContext{SelectedText: "SELECTED_PASSAGE"}, History: []Message{}}
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", request))
	assert(t, job.Status == "done" && job.Calls == 4 && controlCalls.Load() == 3 && childCalls.Load() == 1, "Specialist did not complete within the parent's request budget: "+job.Error)
	assert(t, len(job.Steps) == 3 && job.Steps[1].Tool == "researcher" && job.Steps[1].Status == "done" && strings.Contains(job.Steps[1].Detail, "Scoped conclusion"), "Specialist evidence was not retained in the parent trace")
}

// TestRepairedMemoryMeaningPreservesVersions covers each semantic field instead of only content/state.
func TestRepairedMemoryMeaningPreservesVersions(t *testing.T) {
	cases := []struct {
		name   string
		change func(*StoryMemory)
	}{
		{"content", func(m *StoryMemory) { m.Content = "新内容" }},
		{"state", func(m *StoryMemory) { m.State = "resolved" }},
		{"knowledge", func(m *StoryMemory) { m.KnownBy = []string{"林舟", "岑月"}; m.Chapter = 12 }},
		{"chapter", func(m *StoryMemory) { m.Chapter = 12 }},
		{"through", func(m *StoryMemory) { m.ThroughChapter = 8 }},
		{"deadline", func(m *StoryMemory) { m.DueChapter = 20 }},
		{"story_time", func(m *StoryMemory) { m.StoryTime = "次日" }},
		{"quote", func(m *StoryMemory) { m.Quote = "原文乙" }},
		{"title", func(m *StoryMemory) { m.Title = "修订标题" }},
		{"entity", func(m *StoryMemory) { m.Entity = "岑月" }},
		{"source_id", func(m *StoryMemory) { m.SourceID = "alternate-source"; m.SourceHash = "" }},
		{"source_hash", func(m *StoryMemory) { m.SourceHash = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			engine := testEngine(t)
			p := testProject()
			p.Chapters[0].Body = "原文甲。原文乙。"
			p.Sources = append(p.Sources, Source{ID: "alternate-source", Title: "另一原文", Category: "note", Content: p.Chapters[0].Body})
			insertProject(t, engine, p)
			m := StoryMemory{Kind: "knowledge", Entity: "林舟", Title: "钥匙位置", Content: "钥匙在北塔", State: "active", Status: "confirmed", Chapter: 1, KnownBy: []string{"林舟"}, SourceID: p.Chapters[0].ID, Quote: "原文甲"}
			p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": m})
			old := p.Studio.Memories[0]
			if c.name == "source_hash" {
				p.Chapters[0].Body += "原文新增内容。"
				if err := engine.store.Save(&p, p.Revision); err != nil {
					t.Fatal(err)
				}
			}
			m = old
			c.change(&m)
			p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": m})
			if len(p.Studio.Memories) != 2 || p.Studio.Memories[0].Status != "superseded" || p.Studio.Memories[1].Supersedes != old.ID || p.Studio.Memories[1].ID == old.ID || p.Studio.Memories[0].Content != old.Content || p.Studio.Memories[0].Quote != old.Quote {
				t.Fatal("A semantic edit discarded the old record")
			}
			if c.name == "knowledge" {
				early := jsonText(memoryContext(&p, 6))
				late := jsonText(memoryContext(&p, 20))
				if strings.Contains(early, "岑月") || !strings.Contains(early, "林舟") || !strings.Contains(late, "岑月") {
					t.Fatal("Knowledge did not respect the effective chapter")
				}
			}
		})
	}
}

// TestRepairedSameChapterCorrectionsDoNotDuplicateHistory keeps archived intervals while showing only the replacement.
func TestRepairedSameChapterCorrectionsDoNotDuplicateHistory(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	m := StoryMemory{Kind: "setting", Title: "北塔规则", Content: "旧规则", Status: "confirmed", Chapter: 1, ThroughChapter: 20}
	p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": m})
	m = p.Studio.Memories[0]
	m.Content = "新规则"
	p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": m})
	context := jsonText(memoryContext(&p, 6))
	if strings.Contains(context, "旧规则") || !strings.Contains(context, "新规则") || p.Studio.Memories[0].ThroughChapter != 20 {
		t.Fatal("A same-chapter correction either rewrote old metadata or left both versions active")
	}
}

// TestRepairedStaleMemoryRetiresWithoutRewritingEvidence verifies the user-facing rejection transition.
func TestRepairedStaleMemoryRetiresWithoutRewritingEvidence(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Chapters[0].Body = "钥匙在北塔。"
	insertProject(t, engine, p)
	m := StoryMemory{Kind: "fact", Title: "位置", Content: "钥匙在北塔", Status: "confirmed", SourceID: p.Chapters[0].ID, Quote: p.Chapters[0].Body}
	p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": m})
	old := p.Studio.Memories[0]
	chapter := p.Chapters[0]
	p = invoke[Project](t, engine, "save_chapter", map[string]any{"project_id": p.ID, "revision": p.Revision, "chapter_id": chapter.ID, "title": chapter.Title, "body": "原文已改写，钥匙被移走。", "synopsis": chapter.Synopsis, "status": "draft"})
	bad := old
	bad.Content = "未经重新核对的说法"
	reject(t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": bad})
	bad.Status = "rejected"
	p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": bad})
	retired := p.Studio.Memories[0]
	if retired.Status != "rejected" || retired.SourceHash != old.SourceHash || retired.Quote != old.Quote || retired.Content != old.Content {
		t.Fatal("Retirement rewrote or refused historical evidence")
	}
}

// TestRepairedAIStateAcceptanceReplacesOnlyTheReviewedAttribute exercises two actual extraction and review cycles.
func TestRepairedAIStateAcceptanceReplacesOnlyTheReviewedAttribute(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Chapters[0].Body = "林舟仍然活着。"
	second := NewChapter(12, "守桥")
	second.Body = "林舟死于守桥之战。"
	p.Chapters = append(p.Chapters, second)
	insertProject(t, engine, p)
	responses := []string{}
	for _, item := range []struct {
		chapter Chapter
		state   string
	}{{p.Chapters[0], "alive"}, {second, "dead"}} {
		responses = append(responses, jsonText(map[string]any{"text": "生死状态建议", "proposals": []StudioProposal{{Kind: "memory", Title: "生死", Content: item.state, Memory: &StoryMemory{Kind: "character", Entity: "林舟", Title: "生死", Content: item.state, State: item.state, Chapter: item.chapter.Number, SourceID: item.chapter.ID, Quote: item.chapter.Body}}}}))
	}
	server, _ := sequenceModel(t, responses)
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	for _, chapter := range []Chapter{p.Chapters[0], second} {
		job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": chapter.ID, "action": "memory_update", "instruction": "整理人物变化"}))
		if job.Status != "done" || len(job.Proposals) != 1 {
			t.Fatal(job.Error)
		}
		if chapter.Number == 12 && job.Proposals[0].Memory.Supersedes != p.Studio.Memories[0].ID {
			t.Fatal("The author was not shown an explicit replacement target")
		}
		result := acceptProposalAfterPreview(t, engine, map[string]any{"project_id": p.ID, "revision": p.Revision, "job_id": job.ID, "proposal_id": job.Proposals[0].ID, "decision": "accepted"})
		p = result.Project
	}
	early := jsonText(memoryContext(&p, 6))
	current := jsonText(memoryContext(&p, 20))
	if !strings.Contains(early, "State: alive") || strings.Contains(early, "State: dead") || strings.Contains(current, "State: alive") || !strings.Contains(current, "State: dead") {
		t.Fatal("Accepted state transitions did not preserve one effective value per time")
	}
}

// TestRepairedReviewPreservesPrivateContinuation keeps provider-only data across acceptance and rejection after restart.
func TestRepairedReviewPreservesPrivateContinuation(t *testing.T) {
	for _, decision := range []string{"accepted", "rejected"} {
		t.Run(decision, func(t *testing.T) {
			t.Setenv("QUILLWEAVE_API_KEY", "")
			path := filepath.Join(t.TempDir(), "private.sqlite3")
			engine, err := NewEngine(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { engine.Close() }()
			p := testProject()
			insertProject(t, engine, p)
			var missing atomic.Bool
			response := jsonText(map[string]any{"text": "方向建议", "proposals": []StudioProposal{{Kind: "decision", Title: "选择", Content: "守住北塔"}}})
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []map[string]any `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				for _, message := range body.Messages {
					if message["role"] == "assistant" && message["reasoning_content"] != "PRIVATE_REPAIR" {
						missing.Store(true)
					}
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"PRIVATE_REPAIR\"}}]}\n\n"+streamPayload(response))
			})
			config := nativeConfig("openai")
			config.ProviderID = "moonshot"
			config.Model = "kimi-k3.1-pro"
			config.BaseURL = server.URL
			config.ContextChars = 24000
			configureModel(t, engine, config)
			session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
			first := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "ask", "instruction": "讨论方向"}))
			if first.Status != "done" || len(first.Proposals) != 1 {
				t.Fatal(first.Error)
			}
			before, _, err := engine.store.RunProviderContext(p.ID, first.ID)
			if err != nil || before.Reasoning == "" {
				t.Fatal("Fixture did not persist provider state")
			}
			engine.Close()
			engine, err = NewEngine(path)
			if err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"project_id": p.ID, "revision": p.Revision, "job_id": first.ID, "proposal_id": first.Proposals[0].ID, "decision": decision}
			if decision == "accepted" {
				acceptProposalAfterPreview(t, engine, args)
			} else {
				invoke[map[string]any](t, engine, "review_proposal", args)
			}
			after, _, err := engine.store.RunProviderContext(p.ID, first.ID)
			if err != nil || jsonText(before) != jsonText(after) {
				t.Fatal("Review modified private continuation bytes")
			}
			next := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "ask", "instruction": "继续讨论"}))
			if next.Status != "done" || missing.Load() {
				t.Fatal("Continuation state was missing from the real next request")
			}
		})
	}
}

// TestRepairedIntegrationRevisionAcrossEngines rejects stale settings and refreshes process-local views.
func TestRepairedIntegrationRevisionAcrossEngines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.sqlite3")
	a, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	first := a.integrations.WithoutSecrets()
	stale := b.integrations.WithoutSecrets()
	first.SearchEnabled = true
	invoke[map[string]any](t, a, "save_integrations", map[string]any{"settings": first})
	stale.HindsightURL = "http://127.0.0.1:8888"
	reject(t, b, "save_integrations", map[string]any{"settings": stale})
	latest := invoke[struct {
		Settings IntegrationSettings `json:"settings"`
	}](t, b, "get_integrations", nil).Settings
	if latest.Revision != 1 || !latest.SearchEnabled || latest.HindsightURL != "" {
		t.Fatal("A stale writer erased another engine's settings")
	}
	latest.HindsightURL = stale.HindsightURL
	invoke[map[string]any](t, b, "save_integrations", map[string]any{"settings": latest})
	refreshed := invoke[struct {
		Settings IntegrationSettings `json:"settings"`
	}](t, a, "get_integrations", nil).Settings
	if refreshed.Revision != 2 || !refreshed.SearchEnabled || refreshed.HindsightURL != "" {
		t.Fatal("Shared metadata did not refresh")
	}
}

// TestRepairedAggregateReadUsesOneSnapshot exercises ordinary concurrent writes and multi-statement reads.
func TestRepairedAggregateReadUsesOneSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.sqlite3")
	a, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	p := testProject()
	p.Title = "0"
	p.Chapters[0].Body = "0"
	if err := a.Insert(&p); err != nil {
		t.Fatal(err)
	}
	id := p.ID
	done := make(chan error, 1)
	go func() {
		for i := 1; i <= 100; i++ {
			p.Title = strconv.Itoa(i)
			p.Chapters[0].Body = p.Title
			if err := a.Save(&p, p.Revision); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	mismatch := ""
	for i := 0; i < 200; i++ {
		value, err := b.Get(id)
		if err != nil {
			mismatch = err.Error()
			break
		}
		if value.Title != value.Chapters[0].Body {
			mismatch = "Get returned mixed versions"
			break
		}
		if i%5 == 0 {
			all, err := b.List()
			if err != nil || len(all) != 1 || all[0].Title != all[0].Chapters[0].Body {
				mismatch = "List returned mixed versions during concurrent writes"
				break
			}
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if mismatch != "" {
		t.Fatal(mismatch)
	}
	all, err := b.List()
	if err != nil || len(all) != 1 || all[0].Title != all[0].Chapters[0].Body {
		t.Fatal("List returned an incoherent aggregate")
	}
}

// TestRepairedPreviewAndStartSharePrivateBudget compares the actual two commands on one durable session.
func TestRepairedPreviewAndStartSharePrivateBudget(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	reason := strings.Repeat("PRIVATE_CONTEXT ", 500)
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: "+jsonText(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning_content": reason}}}})+"\n\n"+streamPayload("Visible response"))
	})
	config := nativeConfig("openai")
	config.ProviderID = "moonshot"
	config.Model = "kimi-k3.1-pro"
	config.BaseURL = server.URL
	config.ContextChars = 24000
	configureModel(t, engine, config)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
	first := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "ask", "instruction": "First"}))
	if first.Status != "done" {
		t.Fatal(first.Error)
	}
	config.ContextChars = 4000
	configureModel(t, engine, config)
	args := map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "ask", "instruction": "Continue", "history": []Message{{Role: "assistant", Content: first.Text, SourceJobID: first.ID}}}
	_, previewErr := engine.Dispatch("prepare_ai", rawMessage(args))
	_, startErr := engine.Dispatch("start_ai", rawMessage(args))
	if previewErr == nil || startErr == nil || previewErr.Error() != startErr.Error() {
		t.Fatalf("Preview and start disagree: %v / %v", previewErr, startErr)
	}
}

// TestRepairedDelegateBudgetChecksCompleteInstructions checks all roles at and around their real HTTP budget.
func TestRepairedDelegateBudgetChecksCompleteInstructions(t *testing.T) {
	for _, role := range []string{"writer", "researcher", "reviewer"} {
		for _, boundary := range []string{"below", "exact", "role_overflow"} {
			t.Run(role+"/"+boundary, func(t *testing.T) {
				engine := testEngine(t)
				p := testProject()
				p.Chapters[0].Body = ""
				p.requestLanguage, p.taskLanguage = "en", "en"
				parent := Job{ID: strings.Repeat("j", 36), ProjectID: p.ID, ChapterID: p.Chapters[0].ID, Instruction: "Delegate a scene task", Language: "en"}
				request := scopedAgentRequest(&p, parent, agentStepEnvelope{Tool: role, Query: "Bounded scene task"}, 7)
				task, suffix := request.Action, request.systemAppend
				base, err := prepareAI(&p, request, 24000)
				if err != nil {
					t.Fatal(err)
				}
				budget := max(4000, base.Chars+300)
				expected := budget
				if boundary == "below" {
					expected--
				}
				if boundary == "role_overflow" {
					expected += charCount(suffix)
				}
				p.Chapters[0].Body = strings.Repeat("x", expected-base.Chars)
				// Recount the generated length guidance, whose digit count also depends on the fixture body.
				for attempt := 0; attempt < 4; attempt++ {
					full, err := prepareAI(&p, request, 24000)
					if err != nil {
						t.Fatal(err)
					}
					delta := expected - full.Chars
					if delta == 0 {
						break
					}
					if delta > 0 {
						p.Chapters[0].Body += strings.Repeat("x", delta)
					} else {
						p.Chapters[0].Body = p.Chapters[0].Body[:len(p.Chapters[0].Body)+delta]
					}
				}
				insertProject(t, engine, p)
				var childCalls, actualChars atomic.Int32
				childServer := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
					var body struct {
						Messages []Message `json:"messages"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					childCalls.Add(1)
					actualChars.Store(int32(messageChars(body.Messages)))
					if len(body.Messages) == 0 || !strings.HasSuffix(body.Messages[0].Content, suffix) {
						t.Error("The scoped role instruction was absent from the HTTP request")
					}
					fmt.Fprint(w, streamPayload("Scoped result"))
				})
				child := testProfile("child")
				child.Config = nativeConfig("openai")
				child.Config.BaseURL, child.Config.ContextChars = childServer.URL, uint64(budget)
				var controlCalls atomic.Int32
				controlServer := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
					if controlCalls.Add(1) == 1 {
						if role == "researcher" {
							// Route future ask tools after the coordinator has captured its own immutable model snapshot.
							latest := invoke[settingsResponse](t, engine, "get_model_settings", nil).Settings
							latest.Routes[task] = child.ID
							invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": latest})
						}
						fmt.Fprint(w, streamPayload(jsonText(map[string]string{"tool": role, "query": request.Instruction})))
					} else {
						fmt.Fprint(w, streamPayload(`{"tool":"finish","text":"Review the scoped result."}`))
					}
				})
				settings := invoke[settingsResponse](t, engine, "get_model_settings", nil).Settings
				control := testProfile("control")
				control.Config = nativeConfig("openai")
				control.Config.BaseURL, control.Config.ContextChars = controlServer.URL, 24000
				settings.Profiles, settings.DefaultProfile = []ModelProfile{control, child}, control.ID
				settings.Routes = map[string]string{}
				if role != "researcher" {
					settings.Routes[task] = child.ID
				}
				invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": settings})
				job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "agent", "instruction": "Delegate a scene task"}))
				if job.Status != "done" || len(job.Steps) < 1 {
					t.Fatal(job.Error)
				}
				if boundary == "role_overflow" {
					if childCalls.Load() != 0 || job.Steps[0].Status != "error" || len(job.Proposals) != 0 {
						t.Fatal("The role instruction bypassed the request budget")
					}
				} else if childCalls.Load() != 1 || int(actualChars.Load()) != expected || job.Steps[0].Status != "done" {
					t.Fatalf("The complete role request used %d characters, wanted %d: %s", actualChars.Load(), expected, jsonText(job.Steps))
				}
			})
		}
	}
}

// TestRepairedMemoryProposalTargets separates unrelated attributes, independent additions and explicit replacement.
func TestRepairedMemoryProposalTargets(t *testing.T) {
	for _, mode := range []string{"explicit", "independent", "ambiguous", "foreign", "unrelated"} {
		t.Run(mode, func(t *testing.T) {
			engine := testEngine(t)
			p := testProject()
			p.Studio.Memories = []StoryMemory{
				{ID: "state-a", Kind: "character", Entity: "林舟", Title: "生死", State: "alive", Content: "第一条独立观察", Status: "confirmed", Chapter: 1},
				{ID: "state-b", Kind: "character", Entity: "林舟", Title: "生死", State: "alive", Content: "第二条独立观察", Status: "confirmed", Chapter: 1},
			}
			insertProject(t, engine, p)
			memory := StoryMemory{Kind: "character", Entity: "林舟", Title: "生死", State: "dead", Content: "新的观察", Chapter: 12}
			if mode == "unrelated" {
				memory.Title, memory.State = "携带武器", "active"
			}
			job := Job{ID: newID(), ProjectID: p.ID, Status: "done", Proposals: []StudioProposal{{ID: newID(), Kind: "memory", Title: memory.Title, Content: memory.Content, Status: "proposed", Memory: &memory}}}
			if err := engine.store.SaveRun(job.ID, p.ID, storedJob(&job)); err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"project_id": p.ID, "revision": p.Revision, "job_id": job.ID, "proposal_id": job.Proposals[0].ID, "decision": "accepted"}
			switch mode {
			case "explicit":
				args["supersedes_id"] = "state-a"
			case "independent":
				args["supersedes_id"] = ""
			case "foreign":
				args["supersedes_id"] = "another-book-memory"
			}
			if mode == "foreign" {
				reject(t, engine, "review_proposal", args)
				p = invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
				if len(p.Studio.Memories) != 2 || p.Studio.Memories[0].Status != "confirmed" {
					t.Fatal("An invalid target partially changed canon")
				}
				return
			}
			result := acceptProposalAfterPreview(t, engine, args).Project
			if len(result.Studio.Memories) != 3 || result.Studio.Memories[1].Status != "confirmed" {
				t.Fatal("An unrelated attribute was replaced")
			}
			if mode == "explicit" {
				if result.Studio.Memories[0].Status != "superseded" || result.Studio.Memories[2].Supersedes != "state-a" {
					t.Fatal("The reviewed target was not replaced")
				}
			} else if result.Studio.Memories[0].Status != "confirmed" || result.Studio.Memories[2].Supersedes != "" {
				t.Fatal("An independent or ambiguous proposal silently superseded an existing value")
			}
		})
	}
}

// TestRepairedPendingMemoryAndNoOpEdit protects canon until acceptance and avoids versions for reordered knowledge names.
func TestRepairedPendingMemoryAndNoOpEdit(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	memory := StoryMemory{Kind: "knowledge", Entity: "林舟", Title: "钥匙位置", Content: "北塔", KnownBy: []string{"林舟", "岑月"}, Chapter: 1}
	p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": memory})
	old := p.Studio.Memories[0]
	memory = old
	memory.KnownBy = []string{"岑月", " 林舟 ", "林舟"}
	p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": memory})
	if len(p.Studio.Memories) != 1 || p.Studio.Memories[0].ID != old.ID {
		t.Fatal("Name ordering created a semantic version")
	}
	memory.ID, memory.Supersedes, memory.Status, memory.Chapter, memory.Content = "", old.ID, "proposed", 12, "南塔"
	p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": memory})
	if p.Studio.Memories[0].Status != "confirmed" || strings.Contains(jsonText(memoryContext(&p, 20)), "南塔") {
		t.Fatal("A pending proposal changed current canon")
	}
	memory = p.Studio.Memories[1]
	memory.Status = "confirmed"
	p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": memory})
	if len(p.Studio.Memories) != 2 || p.Studio.Memories[0].Status != "superseded" || !strings.Contains(jsonText(memoryContext(&p, 20)), "南塔") || strings.Contains(jsonText(memoryContext(&p, 6)), "南塔") {
		t.Fatal("Accepting a pending memory lost its version boundary")
	}
	for _, chain := range [][]StoryMemory{{{ID: "a", Supersedes: "missing"}}, {{ID: "a", Supersedes: "b"}, {ID: "b", Supersedes: "a"}}} {
		if validateMemoryLinks(chain) == nil {
			t.Fatal("Malformed memory history was accepted")
		}
	}
}

// TestRepairedSharedSettingsKeepKeysOnlyForMatchingDestinations exercises metadata refresh without persisting secrets.
func TestRepairedSharedSettingsKeepKeysOnlyForMatchingDestinations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key-refresh.sqlite3")
	a, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	settings := defaultIntegrations()
	settings.Providers = []SearchProvider{{ID: "fixture", Kind: "tavily", Endpoint: "http://127.0.0.1:10001"}}
	settings.HindsightURL = "http://127.0.0.1:10002"
	invoke[map[string]any](t, a, "save_integrations", map[string]any{"settings": settings, "key_updates": map[string]SecretUpdate{"fixture": {Action: "replace", Value: "TEST_SEARCH_SECRET"}, "hindsight": {Action: "replace", Value: "TEST_MEMORY_SECRET"}}})
	view := invoke[struct {
		Settings IntegrationSettings `json:"settings"`
		Keys     map[string]bool     `json:"key_status"`
	}](t, b, "get_integrations", nil)
	if view.Keys["fixture"] || view.Keys["hindsight"] {
		t.Fatal("A peer obtained process-local keys")
	}
	view.Settings.MaxSearches = 3
	invoke[map[string]any](t, b, "save_integrations", map[string]any{"settings": view.Settings})
	snapshot, _, err := a.researchSnapshot()
	if err != nil || snapshot.MaxSearches != 3 || snapshot.Providers[0].APIKey != "TEST_SEARCH_SECRET" || snapshot.HindsightKey != "" {
		t.Fatal("Matching destinations lost their process-local credentials")
	}
	view = invoke[struct {
		Settings IntegrationSettings `json:"settings"`
		Keys     map[string]bool     `json:"key_status"`
	}](t, b, "get_integrations", nil)
	view.Settings.Providers[0].Endpoint = "http://127.0.0.1:10003"
	view.Settings.HindsightURL = "http://127.0.0.1:10004"
	invoke[map[string]any](t, b, "save_integrations", map[string]any{"settings": view.Settings})
	snapshot, _, err = a.researchSnapshot()
	if err != nil || snapshot.Providers[0].APIKey != "" || snapshot.HindsightKey != "" || snapshot.Revision != 3 {
		t.Fatal("Credentials followed a changed destination")
	}
	var raw string
	if err := a.store.db.QueryRow("SELECT document FROM integrations WHERE id=1").Scan(&raw); err != nil || strings.Contains(raw, "TEST_") {
		t.Fatal("Integration metadata persisted credentials")
	}
}
