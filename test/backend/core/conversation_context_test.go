//go:build quillweave_whitebox

package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestLatestToolPreviewFitsInputBudget preserves the immutable result source while retaining full readback elsewhere.
func TestLatestToolPreviewFitsInputBudget(t *testing.T) {
	for _, fixed := range []int{2000, 2900} {
		result := toolResultExcerpt(strings.Repeat("source evidence ", 180), "job", "latest", 6500)
		messages := []Message{{Role: "system", Content: strings.Repeat("s", fixed)}, {Role: "user", Content: "Current author goal"}, {Role: "assistant", Content: `{"tool":"recall"}`}, {Role: "tool", Content: result}}
		compacted := condenseAgentToolResults(messages, 1, 3600)
		assert(t, messageChars(compacted) <= 3600 && strings.Contains(compacted[3].Content, "J:job:latest") && strings.Contains(compacted[3].Content, "get_task_step"), "A fresh result exhausted the input budget or lost its source pointer")
		assert(t, messages[3].Content == result, "Condensing a preview mutated its original record")
	}
}

// TestOversizedRecentExchangeCanBeCheckpointed preserves the current task when even the newest completed exchange must be folded.
func TestOversizedRecentExchangeCanBeCheckpointed(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	var calls atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := string(readBodyForStudioTest(t, r))
		index := calls.Add(1)
		if strings.Contains(body, "Summarize this fiction-author conversation") {
			fmt.Fprint(w, streamPayload(`{"goal":"Preserve LATEST_AUTHOR_CORRECTION","constraints":["Use the saved chapter"],"rejected":[],"pending":[],"open_questions":[],"next_steps":["CURRENT_AUTHOR_TASK"]}`))
			return
		}
		if index == 1 {
			fmt.Fprint(w, streamPayload(strings.Repeat("Older working notes. ", 1100)))
			return
		}
		fmt.Fprint(w, streamPayload("Continue with the current author task."))
	})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "recent-checkpoint-fixture", 24000
	configureModel(t, engine, config)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID})
	first := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "ask", "instruction": "LATEST_AUTHOR_CORRECTION"}))
	assert(t, first.Status == "done", first.Error)
	second := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "agent", "instruction": "CURRENT_AUTHOR_TASK"}))
	assert(t, second.Status == "done" && second.Prompt.Checkpoint != nil && len(second.Prompt.HistoryGroups) == 0, "A large most-recent exchange made the conversation unrecoverable: "+second.Error)
	assert(t, strings.Contains(second.Prompt.Messages[len(second.Prompt.Messages)-1].Content, "CURRENT_AUTHOR_TASK") && strings.Contains(second.Prompt.Checkpoint.Summary, "LATEST_AUTHOR_CORRECTION"), "Checkpointing discarded the current request or correction")
	stored := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	assert(t, len(stored.Turns) == 4 && strings.Contains(stored.Turns[1].Content, "Older working notes."), "Checkpointing removed the original discussion")
}

// TestConversationWritesStayIncremental bounds real message writes while exercising metadata edits, retries and forks.
func TestConversationWritesStayIncremental(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	server := staticModel(t, streamPayload("A saved reply."), 0)
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": project.ID})
	_, err := engine.store.db.Exec(`CREATE TABLE turn_write_audit(session_id TEXT);
CREATE TRIGGER count_turn_insert BEFORE INSERT ON conversation_turns BEGIN INSERT INTO turn_write_audit VALUES(new.session_id); END;
CREATE TRIGGER count_turn_update BEFORE UPDATE ON conversation_turns BEGIN INSERT INTO turn_write_audit VALUES(new.session_id); END`)
	assert(t, err == nil, fmt.Sprint(err))
	// attempts observes both insert attempts and actual updates without assuming a particular SQL upsert spelling.
	attempts := func() int {
		t.Helper()
		var count int
		assert(t, engine.store.db.QueryRow("SELECT COUNT(*) FROM turn_write_audit").Scan(&count) == nil, "Could not count turn writes")
		return count
	}
	var last Job
	request := AIRequest{ProjectID: project.ID, SessionID: session.ID, Action: "ask", History: []Message{}}
	for i := 0; i < 6; i++ {
		request.RequestID = fmt.Sprintf("incremental-%d", i)
		request.Instruction = fmt.Sprintf("Discuss choice %d", i)
		last = finishJob(t, engine, invoke[Job](t, engine, "start_ai", request))
		assert(t, last.Status == "done", last.Error)
		assert(t, attempts() <= 4*(i+1), "A new exchange rewrote older message rows")
	}
	session = invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": project.ID, "session_id": session.ID})
	assert(t, len(session.Turns) == 12, "An incremental write lost conversation history")
	for _, turn := range session.Turns {
		assert(t, turn.Status == "done", "A completed turn retained a running status")
	}
	beforeMetadata := attempts()
	updated := invoke[Conversation](t, engine, "update_conversation", map[string]any{"project_id": project.ID, "session_id": session.ID, "revision": session.Revision, "title": "Renamed conversation", "draft": "Keep this draft"})
	assert(t, updated.Revision == session.Revision+1 && len(updated.Turns) == 12 && attempts() == beforeMetadata, "A metadata edit wrote message rows or lost history")
	retried := invoke[Job](t, engine, "start_ai", request)
	assert(t, retried.ID == last.ID && attempts() == beforeMetadata, "A request retry wrote new message rows")
	branch, err := engine.store.forkConversation(project.ID, session.ID, session.Turns[4].ID)
	assert(t, err == nil && len(branch.Turns) == 4 && attempts() == beforeMetadata+4, "Branching did not persist exactly the selected prefix")
	for i, turn := range branch.Turns {
		assert(t, turn.ID != session.Turns[i].ID && turn.Content == session.Turns[i].Content, "Branching changed content or reused original message identities")
	}
	t.Logf("Six complete exchanges used %d audited insert/update events; metadata edits and retry added zero", beforeMetadata)
}

// TestDurableAdmissionReusesPreparedRequests checks retries after another Engine accepts, finishes or edits the work.
func TestDurableAdmissionReusesPreparedRequests(t *testing.T) {
	for _, phase := range []string{"running", "done", "changed_project"} {
		t.Run(phase, func(t *testing.T) {
			first := testEngine(t)
			second, err := NewEngine(first.store.path)
			assert(t, err == nil, fmt.Sprint(err))
			t.Cleanup(func() { second.Close() })
			project := testProject()
			insertProject(t, first, project)
			entered, release := make(chan struct{}), make(chan struct{})
			var released sync.Once
			defer released.Do(func() { close(release) })
			var calls atomic.Int32
			server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
				if calls.Add(1) == 1 {
					close(entered)
					select {
					case <-release:
					case <-request.Context().Done():
						return
					}
				}
				fmt.Fprint(writer, streamPayload("One accepted answer"))
			})
			config := nativeConfig("openai")
			config.BaseURL = server.URL
			configureModel(t, first, config)
			session := invoke[Conversation](t, first, "create_conversation", map[string]any{"project_id": project.ID, "chapter_id": ""})
			request := AIRequest{ProjectID: project.ID, SessionID: session.ID, Action: "agent", Instruction: "The same text", RequestID: "shared-request", History: []Message{}}
			prepared, prior, err := second.store.prepareConversation(request)
			assert(t, err == nil && prior == "", "Second Engine did not capture the pre-admission state")
			original := invoke[Job](t, first, "start_ai", request)
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("The first request did not reach the model")
			}
			if phase != "running" {
				released.Do(func() { close(release) })
				original = finishJob(t, first, original)
			}
			if phase == "changed_project" {
				changed := invoke[Project](t, first, "get_project", map[string]any{"project_id": project.ID})
				changed.Title = "Edited after the accepted request"
				assert(t, first.store.Save(&changed, changed.Revision) == nil, "Unable to prepare a newer project revision")
			}
			fresh := invoke[Conversation](t, first, "get_conversation", map[string]any{"project_id": project.ID, "session_id": session.ID})
			invoke[Conversation](t, first, "update_conversation", map[string]any{"project_id": project.ID, "session_id": session.ID, "revision": fresh.Revision, "draft": "Keep this newer draft"})
			attempt := Job{ID: newID(), ProjectID: project.ID, SessionID: session.ID, BaseRevision: project.Revision, ownerID: second.owner.id}
			prior, err = second.store.beginDurableJob(prepared, &attempt)
			assert(t, err == nil && prior == original.ID, "A stale preparation did not resolve to the accepted request")
			fresh = invoke[Conversation](t, first, "get_conversation", map[string]any{"project_id": project.ID, "session_id": session.ID})
			assert(t, fresh.Draft == "Keep this newer draft", "An idempotent retry cleared a newer author draft")
			var accepted int
			assert(t, first.store.db.QueryRow("SELECT COUNT(*) FROM runs WHERE project_id=?", project.ID).Scan(&accepted) == nil && accepted == 1, "The retry inserted another durable job")
			released.Do(func() { close(release) })
			assert(t, finishJob(t, first, original).Status == "done" && calls.Load() == 1, "The accepted task failed or invoked the model twice")
			request.RequestID = "new-author-request"
			next := finishJob(t, second, invoke[Job](t, second, "start_ai", request))
			assert(t, next.Status == "done" && next.ID != original.ID && calls.Load() == 2, "A distinct request ID was incorrectly deduplicated")
		})
	}
}

// TestCacheWireAndUsage checks actual cache fields and usage events arriving after text completion.
func TestCacheWireAndUsage(t *testing.T) {
	messages := []Message{{Role: "system", Content: "Stable instructions", CacheControl: &CacheControl{Type: "ephemeral"}}, {Role: "user", Content: "Continue"}}
	claude := nativeConfig("anthropic")
	body, _, err := modelRequest(claude, messages)
	assert(t, err == nil, fmt.Sprint(err))
	system := body["system"].([]map[string]any)
	assert(t, system[0]["cache_control"] != nil, "Claude cache control was lost on the wire")
	router := nativeConfig("openai")
	router.ProviderID, router.Model = "openrouter", "anthropic/claude-sonnet-4.6"
	body, _, err = modelRequest(router, messages)
	assert(t, err == nil, fmt.Sprint(err))
	content := body["messages"].([]map[string]any)[0]["content"].([]map[string]any)
	assert(t, content[0]["cache_control"] != nil, "OpenRouter lost its content-block cache breakpoint")
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		assert(t, objectField(request, "stream_options")["include_usage"] == true, "Streaming usage was not requested")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Answer\"},\"finish_reason\":\"stop\"}]}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(20 * time.Millisecond)
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":123,\"completion_tokens\":9,\"prompt_tokens_details\":{\"cached_tokens\":100,\"cache_write_tokens\":0}}}\n\ndata: [DONE]\n\n")
	})
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	output, err := streamModelOutput(context.Background(), config, messages, func(string) {})
	assert(t, err == nil && output.Usage.CacheReadTokens != nil && *output.Usage.CacheReadTokens == 100, "Late cache usage was lost")
	assert(t, output.Usage.CacheWriteTokens != nil && *output.Usage.CacheWriteTokens == 0, "Explicit zero became unknown")
	stream := modelStream{config: claude, onText: func(string) {}}
	assert(t, stream.payload("message_start", `{"type":"message_start","message":{"usage":{"input_tokens":8,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}`) == nil, "Claude usage failed")
	assert(t, stream.payload("message_delta", `{"type":"message_delta","usage":{"output_tokens":7},"delta":{"stop_reason":"end_turn"}}`) == nil, "Claude output usage failed")
	assert(t, *stream.usage.InputTokens == 128 && *stream.usage.OutputTokens == 7, "Claude total input did not include cache reads and writes")
	unknown := modelStream{config: config, onText: func(string) {}}
	assert(t, unknown.payload("", `{"choices":[{"delta":{"content":"Answer"}}]}`) == nil && unknown.usage.InputTokens == nil, "Missing usage became a fabricated zero")
}

// TestProviderUsageNormalization checks protocol-specific counters and prevents cumulative thought double-counting.
func TestProviderUsageNormalization(t *testing.T) {
	for _, item := range []struct {
		protocol, payload     string
		input, output, cached int64
	}{
		{"responses", `{"response":{"usage":{"input_tokens":1600,"output_tokens":80,"input_tokens_details":{"cached_tokens":1000,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":20}}}}`, 1600, 80, 1000},
		{"openai", `{"usage":{"prompt_tokens":120,"completion_tokens":9,"prompt_cache_hit_tokens":100}}`, 120, 9, 100},
		{"gemini_generate", `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":7,"thoughtsTokenCount":3,"cachedContentTokenCount":50}}`, 100, 10, 50},
		{"gemini", `{"interaction":{"usage":{"total_input_tokens":7,"total_output_tokens":23,"total_thought_tokens":49,"total_cached_tokens":0}}}`, 7, 72, 0},
	} {
		t.Run(item.protocol, func(t *testing.T) {
			stream := modelStream{config: nativeConfig(item.protocol)}
			value, err := jsonObject(item.payload)
			assert(t, err == nil, fmt.Sprint(err))
			stream.readUsage(value)
			stream.readUsage(map[string]any{})
			assert(t, stream.usage.InputTokens != nil && *stream.usage.InputTokens == item.input && stream.usage.OutputTokens != nil && *stream.usage.OutputTokens == item.output && stream.usage.CacheReadTokens != nil && *stream.usage.CacheReadTokens == item.cached, "Usage counters diverged from the provider's cumulative values")
		})
	}
}

// TestConversationReplayAndCheckpoints follows real multi-turn requests through compaction and restart.
func TestConversationReplayAndCheckpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.sqlite3")
	engine, err := NewEngine(path)
	assert(t, err == nil, fmt.Sprint(err))
	t.Cleanup(func() { engine.Close() })
	p := testProject()
	insertProject(t, engine, p)
	var requests [][]Message
	var mu sync.Mutex
	var summaries atomic.Int32
	var summaryInputs strings.Builder
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		mu.Lock()
		requests = append(requests, request.Messages)
		mu.Unlock()
		text := `{"tool":"finish","text":"We can keep discussing."}`
		if strings.Contains(request.Messages[0].Content, "Summarize this fiction-author conversation") {
			summaries.Add(1)
			mu.Lock()
			summaryInputs.WriteString(request.Messages[1].Content)
			mu.Unlock()
			text = jsonText(discussionSummary{Goal: "Continue the mystery", Constraints: []string{"Magic costs memory"}, Rejected: []string{"No resurrection"}, Pending: []string{"Map awaiting review"}, OpenQuestions: []string{"Who sent the letter?"}, NextSteps: []string{"Discuss chapter two"}})
		} else if strings.Contains(request.Messages[len(request.Messages)-1].Content, "FIRST_EXCHANGE") {
			text = `{"tool":"get_entity_detail","query":"Lin Wu"}`
		}
		fmt.Fprint(w, streamPayload(text))
	})
	config := nativeConfig("openai")
	config.BaseURL, config.ContextChars = server.URL, 24000
	configureModel(t, engine, config)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
	// send uses the normal start and durable-completion commands for every real request.
	send := func(text string) Job {
		job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "agent", "instruction": text}))
		assert(t, job.Status == "done", job.Error)
		return job
	}
	first := send("FIRST_EXCHANGE")
	second := send("Continue our discussion")
	assert(t, len(first.Steps) == 2 && len(second.Metrics) == 1, "Tool loop or request telemetry missing")
	assert(t, second.Metrics[0].ChangeReason == "History appended" && second.Metrics[0].SharedPrefixBytes != nil && *second.Metrics[0].SharedPrefixBytes > 1000, "Request diagnostics lost the byte-stable growing prefix")
	mu.Lock()
	before, replayed := requests[1], requests[2]
	mu.Unlock()
	assert(t, len(replayed) == len(before)+2, "The complete previous exchange was not replayed")
	assert(t, jsonText(before) == jsonText(replayed[:len(before)]), "Previously sent user context or tool pairs changed")
	assert(t, strings.Contains(replayed[len(before)].Content, `"tool":"finish"`), "The actual provider response was replaced by display text")
	p.Title = "Updated title"
	assert(t, engine.store.Save(&p, p.Revision) == nil, "Could not update the author-approved story")
	changed := invoke[Prompt](t, engine, "prepare_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "agent", "instruction": "Use the updated title"})
	assert(t, changed.Messages[0].Content == before[0].Content && strings.Contains(changed.Messages[len(changed.Messages)-1].Content, "<story_update>"), "A story update rewrote the stable prefix instead of adding the current records")
	for i := 0; i < 14; i++ {
		send(fmt.Sprintf("Discuss %d %s Magic costs memory. No resurrection. Map awaiting review. Who sent the letter?", i, strings.Repeat("A scene detail. ", 110)))
	}
	loaded := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	assert(t, summaries.Load() > 0 && loaded.Checkpoint != nil, "Long discussion never created a durable checkpoint")
	mu.Lock()
	summarySource := summaryInputs.String()
	mu.Unlock()
	assert(t, strings.Contains(summarySource, "No resurrection") && strings.Contains(summarySource, "Who sent the letter?"), "Summary input silently lost late-message constraints")
	assert(t, len(loaded.Turns) == 32, "Compaction removed original turns")
	checkpoint := *loaded.Checkpoint
	assert(t, engine.Close() == nil, "Could not close first process")
	engine, err = NewEngine(path)
	assert(t, err == nil, fmt.Sprint(err))
	preview := invoke[Prompt](t, engine, "prepare_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "agent", "instruction": "Resume after restart"})
	assert(t, preview.Checkpoint != nil && preview.Checkpoint.SourceHash == checkpoint.SourceHash && preview.Messages[1].Content == checkpoint.Summary, "Restart did not reuse the exact saved checkpoint")
	for i := 0; i < 14; i++ {
		send(fmt.Sprintf("Later discussion %d %s", i, strings.Repeat("Review the established mystery. ", 55)))
	}
	later := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	assert(t, later.Checkpoint.ThroughTurnID != checkpoint.ThroughTurnID && strings.Contains(later.Checkpoint.Summary, "No resurrection"), "A later checkpoint lost established constraints")
	branch := invoke[Conversation](t, engine, "fork_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID, "turn_id": loaded.Turns[2].ID})
	assert(t, branch.Checkpoint == nil, "A rewind inherited a later branch checkpoint")
	_, err = engine.readConversationSource(Job{ProjectID: p.ID, SessionID: branch.ID}, agentStepEnvelope{Tool: "get_task_step", JobID: first.ID, StepID: first.Steps[0].ID})
	assert(t, err == nil, "A branch could not recover a tool result from its copied prefix")
	_, err = engine.readConversationSource(Job{ProjectID: p.ID, SessionID: branch.ID}, agentStepEnvelope{Tool: "get_task_step", JobID: second.ID, StepID: second.Steps[0].ID})
	assert(t, err != nil, "A sibling branch exposed a task result")
	archive := cloneProject(p)
	archive.Conversations = []Conversation{later}
	rekeyArchive(&archive)
	assert(t, archive.Conversations[0].Checkpoint == nil && archive.Conversations[0].BibleBaseline == "" && len(archive.Conversations[0].Turns) == len(later.Turns), "Restoring a backup reused old checkpoint identities or discarded original turns")
}

// TestScopedEntityHistoryRespectsChapter excludes later and stale facts while preserving effective earlier state.
func TestScopedEntityHistoryRespectsChapter(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Entities = []Entity{{ID: "lin", Name: "Lin Wu", Kind: "character", Aliases: []string{}}}
	p.Studio.Memories = []StoryMemory{
		{ID: "early", Kind: "character", Entity: "Lin Wu", Title: "State", Content: "EARLY_STATE", Status: "superseded", Chapter: 1, ThroughChapter: 9},
		{ID: "later", Kind: "character", Entity: "Lin Wu", Title: "State", Content: "FUTURE_STATE", Status: "confirmed", Chapter: 10, Supersedes: "early"},
	}
	ingestCandidate(t, &p)
	p.Facts[0].Status, p.Facts[0].SourceHash, p.Facts[0].Claim = "confirmed", "stale-hash", "STALE_FACT"
	text, err := engine.executeScopedAgentTool(&p, Job{ProjectID: p.ID, ChapterID: p.Chapters[0].ID}, agentStepEnvelope{Tool: "get_entity_detail", EntityID: "lin"})
	assert(t, err == nil && strings.Contains(text, "EARLY_STATE") && !strings.Contains(text, "FUTURE_STATE") && !strings.Contains(text, "STALE_FACT"), "Chapter-scoped tool returned future or stale state")
}

// seedDiscussion creates old-format original turns without checkpoints for migration and failure tests.
func seedDiscussion(t *testing.T, engine *Engine, projectID string) Conversation {
	t.Helper()
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": projectID, "chapter_id": ""})
	for i := 0; i < 18; i++ {
		jobID := newID()
		session.Turns = append(session.Turns,
			ConversationTurn{ID: newID(), Role: "user", Content: strings.Repeat("Some original discussion. ", 35) + "KEEP_THIS_LAST_SENTENCE", JobID: jobID, Action: "agent", Status: "done", CreatedAt: now()},
			ConversationTurn{ID: newID(), Role: "assistant", Content: "Candidate remains unconfirmed", JobID: jobID, Action: "agent", Status: "done", CreatedAt: now()})
	}
	err := engine.store.withWriteTransaction(func(conn *sql.Conn) error { return saveConversation(conn, &session) })
	assert(t, err == nil, fmt.Sprint(err))
	return session
}

// TestFailedCompactionPreservesOriginals checks invalid summaries and cancellation without advancing the checkpoint.
func TestFailedCompactionPreservesOriginals(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			engine := testEngine(t)
			p := testProject()
			insertProject(t, engine, p)
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			defer close(release)
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				entered <- struct{}{}
				if cancel {
					select {
					case <-r.Context().Done():
					case <-release:
					}
					return
				}
				fmt.Fprint(w, streamPayload("Invalid summary"))
			})
			config := nativeConfig("openai")
			config.BaseURL, config.ContextChars = server.URL, 24000
			configureModel(t, engine, config)
			session := seedDiscussion(t, engine, p.ID)
			job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "agent", "instruction": "Continue"})
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("Compaction never reached the model")
			}
			if cancel {
				invoke[any](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
			}
			job = finishJob(t, engine, job)
			assert(t, job.Status == "error" || job.Status == "cancelled", "Failed compaction became successful")
			loaded := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
			assert(t, loaded.Checkpoint == nil && len(loaded.Turns) == len(session.Turns)+2, "Failure advanced a checkpoint or dropped originals")
			assert(t, loaded.Turns[0].Content == session.Turns[0].Content, "Failure rewrote original discussion")
		})
	}
}

// TestCheckpointPreservesNewDraft saves user input arriving while the summary request is in flight.
func TestCheckpointPreservesNewDraft(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	entered, release := make(chan struct{}), make(chan struct{})
	var signal, unblock sync.Once
	defer unblock.Do(func() { close(release) })
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		text := `{"tool":"finish","text":"Discussion resumed."}`
		if strings.Contains(request.Messages[0].Content, "Summarize this fiction-author conversation") {
			signal.Do(func() { close(entered) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			text = jsonText(discussionSummary{Goal: "Preserve the current author discussion", Constraints: []string{"KEEP_THIS_LAST_SENTENCE"}})
		}
		fmt.Fprint(w, streamPayload(text))
	})
	config := nativeConfig("openai")
	config.BaseURL, config.ContextChars = server.URL, 24000
	configureModel(t, engine, config)
	session := seedDiscussion(t, engine, p.ID)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "agent", "instruction": "Continue"})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Summary was not started")
	}
	current := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	invoke[Conversation](t, engine, "update_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID, "revision": current.Revision, "draft": "New input while the checkpoint is being written"})
	unblock.Do(func() { close(release) })
	job = finishJob(t, engine, job)
	assert(t, job.Status == "done", job.Error)
	current = invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	assert(t, current.Checkpoint != nil && current.Draft == "New input while the checkpoint is being written", "Checkpoint persistence overwrote concurrent author input")
}

// TestFailedAgentKeepsReviewableCandidate retains a validated partial result without changing formal story content.
func TestFailedAgentKeepsReviewableCandidate(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	var calls atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, streamPayload(`{"tool":"propose","proposal":{"kind":"decision","title":"Partial idea","content":"The tower remains closed."}}`))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "action": "agent", "instruction": "Prepare a candidate"}))
	assert(t, job.Status == "error" && len(job.ChangeSets) == 1, "The interrupted tool sequence lost its valid candidate")
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, current.Revision == p.Revision && len(current.Studio.Decisions) == 0 && len(current.Studio.ChangeSets) == 1, "A partial candidate changed canon or was absent from the workspace")
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": p.ID, "changeset_id": job.ChangeSets[0].ID})
	result := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "apply_changeset", map[string]any{"project_id": p.ID, "changeset_id": preview.ChangeSet.ID, "revision": preview.ChangeSet.PreviewRevision, "review_token": preview.ChangeSet.ReviewToken})
	assert(t, len(result.Project.Studio.Decisions) == 1, "An author could not confirm a validated partial result")
}

// TestWorkspaceUpgradeKeepsChapterDiscussionsVisible promotes old chapter scopes without losing their draft or provenance.
func TestWorkspaceUpgradeKeepsChapterDiscussionsVisible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite3")
	engine, err := NewEngine(path)
	assert(t, err == nil, fmt.Sprint(err))
	p := testProject()
	insertProject(t, engine, p)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID})
	invoke[Conversation](t, engine, "update_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID, "revision": session.Revision, "draft": "An older chapter discussion draft"})
	_, err = engine.store.db.Exec("PRAGMA user_version=3")
	assert(t, err == nil, fmt.Sprint(err))
	assert(t, engine.Close() == nil, "Could not close the legacy fixture")
	engine, err = NewEngine(path)
	assert(t, err == nil, fmt.Sprint(err))
	t.Cleanup(func() { engine.Close() })
	sessions := invoke[[]Conversation](t, engine, "list_conversations", map[string]any{"project_id": p.ID, "chapter_id": ""})
	assert(t, len(sessions) == 1 && sessions[0].ID == session.ID && sessions[0].OriginChapterID == p.Chapters[0].ID && sessions[0].Draft == "An older chapter discussion draft", "Upgrade hid a chapter-scoped conversation or lost its original scope")
}

// TestConversationPaginationUsesScopeIndex keeps large migrated histories reachable through bounded pages.
func TestConversationPaginationUsesScopeIndex(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	for i := 0; i < 105; i++ {
		invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": "", "title": fmt.Sprintf("Older discussion %d", i)})
	}
	first := invoke[[]Conversation](t, engine, "list_conversations", map[string]any{"project_id": p.ID, "chapter_id": ""})
	second := invoke[[]Conversation](t, engine, "list_conversations", map[string]any{"project_id": p.ID, "chapter_id": "", "offset": 100})
	assert(t, len(first) == 100 && len(second) == 5 && first[0].Title == "Older discussion 104" && second[4].Title == "Older discussion 0", "An older conversation was omitted or repeated at a page boundary")
	reject(t, engine, "list_conversations", map[string]any{"project_id": p.ID, "chapter_id": "", "offset": -1})
	rows, err := engine.store.db.Query("EXPLAIN QUERY PLAN SELECT id FROM conversations WHERE project_id=? AND chapter_id=? ORDER BY rowid DESC LIMIT 100 OFFSET ?", p.ID, "", 100)
	assert(t, err == nil, fmt.Sprint(err))
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		assert(t, rows.Scan(&id, &parent, &unused, &detail) == nil, "Could not read the conversation query plan")
		plan += detail
	}
	assert(t, rows.Err() == nil && strings.Contains(plan, "conversations_project"), "Conversation pagination missed its scope index")
	t.Logf("Conversation pagination plan: %s", plan)
}
