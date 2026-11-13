//go:build quillweave_whitebox

package core

import (
	"fmt"
	"net/http"
	websearch "quillweave/internal/search"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestAgentStreamsBeforeCompletion keeps model I/O blocked while observing real get_job snapshots.
func TestAgentStreamsBeforeCompletion(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			insertProject(t, engine, project)
			release := make(chan struct{})
			var released sync.Once
			defer released.Do(func() { close(release) })
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				_ = readBodyForStudioTest(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"第一段已经生成。"}}]}`+"\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
					fmt.Fprint(w, streamPayload("第二段接着出现。"))
				case <-r.Context().Done():
				}
			})
			config := defaultProvider()
			config.Model, config.BaseURL = "streaming-fixture", server.URL
			configureModel(t, engine, config)
			job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": "", "action": "agent", "instruction": "写一小段"})
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				job = invoke[Job](t, engine, "get_job", map[string]any{"job_id": job.ID})
				if job.Text != "" {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			assert(t, job.Status == "running" && job.Text == "第一段已经生成。", "Agent buffered prose until model completion")
			if cancel {
				invoke[map[string]bool](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
			} else {
				released.Do(func() { close(release) })
			}
			done := finishJob(t, engine, job)
			if cancel {
				assert(t, done.Status == "cancelled" && done.Text == "第一段已经生成。", "Cancellation lost visible Agent prose")
			} else {
				assert(t, done.Status == "done" && done.Text == "第一段已经生成。第二段接着出现。", "Agent final output duplicated its streamed prefix")
			}
			stored, err := engine.loadStoredJob(job.ID)
			assert(t, err == nil && stored.Text == done.Text && stored.Status == done.Status, "Terminal persistence disagreed with visible partial output")
		})
	}
}

// TestAgentProtocolOutputPreservesSurroundingProse separates complete and incomplete protocol material.
func TestAgentProtocolOutputPreservesSurroundingProse(t *testing.T) {
	cases := []struct{ input, expected string }{
		{"普通正文。", "普通正文。"},
		{"  保留正文的缩进与末尾换行。\n", "  保留正文的缩进与末尾换行。\n"},
		{`正文里的普通对象 {"name":"角色"} 保留。`, `正文里的普通对象 {"name":"角色"} 保留。`},
		{`{"tool":"finish","text":"完整回复。"}`, "完整回复。"},
		{"开头正文。\n{\"tool\":\"plan\",\"text\":\"内部计划\"}\n结尾正文。", "开头正文。\n\n结尾正文。"},
		{"开头正文。\n```json\n{\"tool\":\"ask_author\",\"text\":\"你想选哪个？\"}\n```\n结尾正文。", "开头正文。\n\n你想选哪个？\n\n结尾正文。"},
		{`{"tool":"propose_changes","operations":[]}`, ""},
	}
	for _, item := range cases {
		assert(t, visibleAgentOutput(item.input) == item.expected, "Visible output disagreed: "+visibleAgentOutput(item.input))
	}
	assert(t, agentProsePrefix("正文使用 `词语` 后继续。") == "正文使用 `词语` 后继续。", "Inline Markdown stopped streaming")
	assert(t, agentProsePrefix("```markdown\n## 标题\n继续正文。") == "```markdown\n## 标题\n继续正文。", "Non-JSON Markdown fences stopped streaming")
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	engine.jobsMu.Lock()
	engine.jobs["stream-envelope"] = &Job{ID: "stream-envelope", ProjectID: project.ID, Status: "running", Text: "已有回答。"}
	engine.jobsMu.Unlock()
	stream := engine.newAgentTextStream("stream-envelope")
	for _, part := range []string{"```", "json\n{\"tool\":", "\"finish\",\"text\":\"安全正文。\"}", "\n```"} {
		stream.append(part)
		engine.jobsMu.Lock()
		text := engine.jobs["stream-envelope"].Text
		engine.jobsMu.Unlock()
		assert(t, text == "已有回答。", "A partial protocol envelope leaked into the conversation")
	}
	stream.finish(stream.raw.String())
	engine.jobsMu.Lock()
	text := engine.jobs["stream-envelope"].Text
	engine.jobsMu.Unlock()
	assert(t, text == "已有回答。\n\n安全正文。", "A protocol response replaced prior commentary or duplicated itself")
}

// TestNativeArgumentFailureCanBeCorrected exercises malformed native arguments through the normal Agent entry point.
func TestNativeArgumentFailureCanBeCorrected(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	var calls atomic.Int32
	var corrected atomic.Bool
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		if calls.Add(1) == 1 {
			fmt.Fprint(w, nativeFunctionFixture("openai", `{"tool":"plan","text":"broken"quotes"}`))
			return
		}
		corrected.Store(strings.Contains(body, "Function arguments must be one complete JSON object") && strings.Contains(body, "call-native"))
		fmt.Fprint(w, streamPayload("参数错误已纠正，继续讨论。"))
	})
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": "", "action": "agent", "instruction": "讨论"}))
	assert(t, job.Status == "done" && calls.Load() == 2 && corrected.Load(), "Malformed native arguments ended the entire Agent task: "+job.Error)
	assert(t, len(job.Steps) == 1 && job.Steps[0].Status == "error" && job.Text == "参数错误已纠正，继续讨论。", "An invalid native step executed or its protocol leaked")
}

// TestAgentLoopHasNoFixedCallCap keeps the coordinator bounded by context and completion, not a call count.
func TestAgentLoopHasNoFixedCallCap(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	var calls atomic.Int32
	var finalWithoutTools atomic.Bool
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		index := calls.Add(1)
		if index >= 10 {
			finalWithoutTools.Store(!strings.Contains(body, "final model call"))
			fmt.Fprint(w, streamPayload("第十步之后的总结。"))
			return
		}
		arguments := fmt.Sprintf(`{"tool":"plan","text":"step %d"}`, index)
		fmt.Fprint(w, nativeFunctionFixture("openai", arguments))
	})
	config := nativeConfig("openai")
	config.BaseURL, config.ContextChars = server.URL, 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "agent", "instruction": "整理"}))
	assert(t, job.Status == "done" && calls.Load() == 10 && finalWithoutTools.Load(), "The loop stopped before ten coordinator calls or forced an early final note: "+job.Error)
	assert(t, job.Text == "第十步之后的总结。", "Final prose was not retained")
	assert(t, job.Error == "", "A healthy loop still reported an error")
}

// TestAgentSignatureIncludesReadParameters keeps distinct paginated and task reads out of the repeated-failure bucket.
func TestAgentSignatureIncludesReadParameters(t *testing.T) {
	base := agentStepEnvelope{Tool: "get_task_step", JobID: "job-a", StepID: "step-a", Offset: 0}
	next := base
	next.JobID = "job-b"
	assert(t, agentStepSignature(base) != agentStepSignature(next), "Different jobs shared a failure signature")
	next = base
	next.StepID = "step-b"
	assert(t, agentStepSignature(base) != agentStepSignature(next), "Different steps shared a failure signature")
	next = base
	next.Offset = 2000
	assert(t, agentStepSignature(base) != agentStepSignature(next), "Different page offsets shared a failure signature")
}

// TestWikiInvalidCandidatesNeverCacheSuccess covers invalid extraction, legitimate no-facts output and legacy empty caches.
func TestWikiInvalidCandidatesNeverCacheSuccess(t *testing.T) {
	for _, invalid := range []bool{true, false} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			insertProject(t, engine, project)
			response := `{"facts":[]}`
			if invalid {
				response = jsonText(map[string]any{"facts": []Candidate{{Entity: "Lin Wu", Kind: "event", Claim: "Unsupported", Quote: "Absent quote", Epistemic: "explicit"}}})
			}
			server, calls := sequenceModel(t, []string{response, response})
			config := defaultProvider()
			config.Model, config.BaseURL = "wiki-empty-fixture", server.URL
			configureModel(t, engine, config)
			args := map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "wiki", "instruction": ""}
			first := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			if invalid {
				assert(t, first.Status == "error" && strings.Contains(first.Error, "Every extracted candidate"), "An entirely invalid batch was reported as successful")
			} else {
				assert(t, first.Status == "done" && strings.Contains(first.Progress, "no supported evidence"), "A valid zero-facts extraction became a misleading error")
			}
			latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
			assert(t, len(latest.Compilations) == 0 && len(latest.Facts) == 0, "A zero-facts run created a reusable empty compilation")
			finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, calls.Load() == 2, "A repeat reused an empty compilation")
		})
	}
	project, config := memoryProject(t)
	project.Compilations[0].Sections = nil
	fingerprint, err := compilationFingerprint(&project, project.Chapters[0].ID, config)
	assert(t, err == nil && cachedCompilation(&project, project.Chapters[0].ID, fingerprint) == nil, "A pre-existing empty compilation remained a valid cache")
}

// TestExplicitSearchRunsBeforeAgentModel makes slash-search execution independent of the model's tool choice.
func TestExplicitSearchRunsBeforeAgentModel(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(fmt.Sprint(fails), func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			insertProject(t, engine, project)
			var modelCalls, searchCalls atomic.Int32
			var evidenceReceived atomic.Bool
			search := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				searchCalls.Add(1)
				body := string(readBodyForStudioTest(t, r))
				if !strings.Contains(body, "明代官制") {
					t.Error("Explicit search query changed before reaching the service")
				}
				if fails {
					w.WriteHeader(http.StatusUnauthorized)
					fmt.Fprint(w, `{"error":"fixture authentication failure"}`)
					return
				}
				fmt.Fprint(w, `{"results":[{"title":"史料","url":"https://example.org/history","snippet":"制度证据"}]}`)
			})
			model := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				modelCalls.Add(1)
				body := string(readBodyForStudioTest(t, r))
				evidenceReceived.Store(strings.Contains(body, "web_search_results") && strings.Contains(body, "https://example.org/history") && strings.Contains(body, "制度证据"))
				fmt.Fprint(w, streamPayload("依据已检索资料给出结论。"))
			})
			config := defaultProvider()
			config.Model, config.BaseURL = "explicit-search-fixture", model.URL
			configureModel(t, engine, config)
			profile := configureSearchFixture(t, engine, "deepseek", search.URL)
			args := AIRequest{History: []Message{}, ProjectID: project.ID, ChapterID: "", Action: "agent", Instruction: "明代官制", IntentHint: "web_search", SearchSelection: &websearch.Selection{Mode: "profile", ProfileID: profile.ID}}
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, searchCalls.Load() == 1 && len(job.Steps) == 1 && job.Steps[0].Tool == "search", "Explicit search was omitted or ran more than once")
			if fails {
				assert(t, job.Status == "error" && strings.Contains(job.Error, "Web search failed") && job.Steps[0].Status == "error" && modelCalls.Load() == 0, "Search failure silently became an unresearched model answer")
			} else {
				assert(t, job.Status == "done" && job.Steps[0].Status == "done" && modelCalls.Load() == 1 && job.Calls == 1 && evidenceReceived.Load(), "The model did not receive actual explicit-search evidence")
			}
		})
	}
}

// TestScopedAgentUsesFrozenSkillInstructions keeps delegation bound to instructions actually read by the parent.
func TestScopedAgentUsesFrozenSkillInstructions(t *testing.T) {
	project := testProject()
	loaded := LoadedSkill{SkillDescriptor: SkillDescriptor{ID: "dynamic-skill", Name: "Style", Version: "read-version"}, Content: "Frozen reference rules.", ResourcePath: "references/style.md"}
	job := Job{
		ID: "parent", ProjectID: project.ID, Instruction: "Write with the selected method",
		Prompt: Prompt{Skills: []SkillDescriptor{{ID: "explicit", Version: "original"}}, Messages: []Message{{Role: "system", Content: "Base instructions.\n<selected_skill id=\"explicit\" version=\"original\">\nFrozen selected rules.\n</selected_skill>\n<available_skills>Other catalog.</available_skills>"}}},
		Steps:  []AgentStep{{ID: "load", Tool: "load_skill", Status: "done", Detail: "excerpt", FullDetail: jsonText(loaded)}},
	}
	request := scopedAgentRequest(&project, job, agentStepEnvelope{Tool: "writer", Query: "Write a paragraph"})
	assert(t, strings.Contains(request.systemAppend, "Frozen selected rules.") && strings.Contains(request.systemAppend, "Frozen reference rules.") && strings.Contains(request.systemAppend, "read-version"), "Delegation omitted frozen Skill content or its version")
	assert(t, !strings.Contains(request.systemAppend, "Other catalog."), "Delegation copied unrelated discovery metadata")
}
