//go:build quillweave_whitebox

package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestChatSelectionPrecedenceAndSnapshot keeps explicit choices above routing and copies session intent.
func TestChatSelectionPrecedenceAndSnapshot(t *testing.T) {
	models := testSettings()
	models.Routes["ask"] = "writer"
	project := testProject()
	project.ModelOverrides = map[string]string{"ask": "writer"}
	choice := &ChatModelSelection{ProfileID: "reviewer", OutputMode: "custom", MaxTokens: 1024}
	selected, frozen, err := models.selectChatModel(&project, AIRequest{ModelSelection: choice}, "ask")
	assert(t, err == nil && selected.ProfileID == "reviewer" && selected.Config.MaxTokens == 1024, "explicit chat choice did not override task routing")
	choice.MaxTokens = 2048
	assert(t, frozen.MaxTokens == 1024, "accepted selection retained the mutable composer pointer")
	selected, inherited, err := models.selectChatModel(&project, AIRequest{conversation: &Conversation{ModelSelection: frozen}}, "rewrite")
	assert(t, err == nil && selected.ProfileID == "reviewer" && inherited != frozen, "session model was not inherited by another creative action")
	selected, legacy, err := models.selectChatModel(&project, AIRequest{}, "ask")
	assert(t, err == nil && selected.ProfileID == "writer" && legacy == nil, "legacy routing changed without a chat selection")
}

// TestTokenUsageCalibrationRequiresAnUnchangedWirePrefix reuses measured usage while falling back after context changes.
func TestTokenUsageCalibrationRequiresAnUnchangedWirePrefix(t *testing.T) {
	config := defaultProvider()
	config.Model = "fixture-model"
	old := []Message{{Role: "system", Content: "Stable system"}, {Role: "user", Content: strings.Repeat("old prose ", 100)}}
	body, _, err := modelRequest(config, old)
	assert(t, err == nil, "fixture model request failed")
	var stored map[string]any
	err = decodeJSON(rawMessage(body), &stored)
	assert(t, err == nil, "wire snapshot normalization failed")
	config.tokenBaseline = &tokenUsageBaseline{Body: stored, InputTokens: 100}
	current := append(append([]Message{}, old...), Message{Role: "assistant", Content: "Previous answer"}, Message{Role: "user", Content: "Continue"})
	calibrated, ok := calibratedInputTokens(config, current)
	assert(t, ok && calibrated > 100 && calibrated < estimateMessageTokens(current), "provider input usage was not reused for an exact prefix")
	current[0].Content = "Changed system"
	_, ok = calibratedInputTokens(config, current)
	assert(t, !ok, "changed system instructions reused an unrelated measured token baseline")
	current[0] = old[0]
	config.nativeTools = true
	_, ok = calibratedInputTokens(config, current)
	assert(t, !ok, "changed tool schemas reused a measured baseline from a text-only request")
}

// TestCompactionRebasesGroupsBeforeCheckingDenseSummary rejects a dense summary without slicing stale history indices.
func TestCompactionRebasesGroupsBeforeCheckingDenseSummary(t *testing.T) {
	engine := testEngine(t)
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, streamPayload(jsonText(discussionSummary{Goal: strings.Repeat("中", 1100)})))
	})
	config := defaultProvider()
	config.Model, config.BaseURL, config.ContextChars, config.MaxTokens = "fixture-model", server.URL, 5000, 1000
	config.chatOutputAuto = true
	config.ModelLimits = &ModelLimits{ContextWindowTokens: 5000, MaxOutputTokens: 1000}
	prompt := Prompt{Messages: []Message{
		{Role: "system", Content: "Stable instructions"},
		{Role: "user", Content: strings.Repeat("a", 1000)},
		{Role: "assistant", Content: strings.Repeat("b", 1000)},
		{Role: "user", Content: strings.Repeat("c", 1000)},
		{Role: "assistant", Content: strings.Repeat("d", 1000)},
		{Role: "user", Content: strings.Repeat("中", 1000)},
		{Role: "assistant", Content: strings.Repeat("文", 300)},
		{Role: "user", Content: "Continue"},
	}, HistoryGroups: []HistoryGroup{{End: 3, TurnID: "old-1"}, {End: 5, TurnID: "old-2"}, {End: 7, TurnID: "recent"}}}
	job := Job{ID: "density-check", SessionID: "fixture-session", ctx: context.Background(), Prompt: prompt, ModelSelection: &ChatModelSelection{ProfileID: "default", OutputMode: "auto"}}
	compacted, err := engine.compactJobConversation(job, SelectedModel{ProfileID: "default", Config: config})
	assert(t, err != nil && strings.Contains(err.Error(), "assembled input") && compacted.Prompt.Budget != nil, "dense summary bypassed the final request budget check")
	for _, group := range compacted.Prompt.HistoryGroups {
		assert(t, group.End <= len(compacted.Prompt.Messages), "compaction retained a stale history end index")
	}
}

// TestCompactionDoesNotPersistStillCompressibleOverflow rejects residual over-window context even with two groups left.
func TestCompactionDoesNotPersistStillCompressibleOverflow(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": project.ID, "chapter_id": ""})
	messages := []Message{{Role: "system", Content: strings.Repeat("s", 100)}}
	groups := []HistoryGroup{}
	for i := 0; i < 5; i++ {
		text := strings.Repeat("a", 2000)
		if i == 4 {
			text = strings.Repeat("中", 400)
		}
		for _, role := range []string{"user", "assistant"} {
			turn := ConversationTurn{ID: newID(), Role: role, Content: text, JobID: fmt.Sprintf("prior-%d", i), Status: "done", Action: "ask", CreatedAt: now()}
			session.Turns = append(session.Turns, turn)
			messages = append(messages, Message{Role: role, Content: text})
		}
		boundary := session.Turns[len(session.Turns)-1].ID
		groups = append(groups, HistoryGroup{End: len(messages), TurnID: boundary, SourceHash: conversationPrefixHash(&session, boundary)})
	}
	if err := engine.store.withWriteTransaction(func(db *sql.Conn) error { return saveConversation(db, &session) }); err != nil {
		t.Fatal(err)
	}
	messages = append(messages, Message{Role: "user", Content: strings.Repeat("u", 100)})
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, streamPayload(jsonText(discussionSummary{Goal: strings.Repeat("中", 1500)})))
	})
	config := defaultProvider()
	config.Model, config.BaseURL, config.MaxTokens = "fixture-model", server.URL, 1000
	config.chatOutputAuto = true
	config.ModelLimits = &ModelLimits{ContextWindowTokens: 5000, MaxOutputTokens: 1000}
	selection := &ChatModelSelection{ProfileID: "default", OutputMode: "auto"}
	prompt := Prompt{Messages: messages, HistoryGroups: groups}
	prepareTokenBudget(&prompt, &config, selection)
	assert(t, prompt.Budget.CanSend && prompt.Budget.NeedsCompaction, "fixture did not reach the compressible old-history boundary")
	job := Job{ID: "residual-overflow", ProjectID: project.ID, SessionID: session.ID, Status: "running", Prompt: prompt, ModelSelection: selection, ownerID: engine.owner.id, ctx: context.Background(), cancel: func() {}}
	engine.jobsMu.Lock()
	engine.jobs[job.ID] = &job
	engine.jobsMu.Unlock()
	if err := engine.store.SaveRun(job.ID, project.ID, storedJob(&job)); err != nil {
		t.Fatal(err)
	}
	compacted, err := engine.compactJobConversation(job, SelectedModel{ProfileID: "default", Config: config})
	assert(t, err != nil && strings.Contains(err.Error(), "assembled input") && len(compacted.Prompt.HistoryGroups) == 2 && compacted.Prompt.Budget.CanSend, "residual over-window summary bypassed the final request check")
	restored, err := readConversation(engine.store.db, project.ID, session.ID)
	assert(t, err == nil && restored.Checkpoint == nil, "unusable checkpoint replaced the original persisted conversation")
}

// TestChatAutomaticOutputReachesNormalRequest verifies the chosen capability through start_ai and serialization.
func TestChatAutomaticOutputReachesNormalRequest(t *testing.T) {
	engine := testEngine(t)
	_, err := engine.setSkillRoots([]string{})
	assert(t, err == nil, "isolated Skill roots could not be configured")
	observed := make(chan uint32, 1)
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		var input struct {
			MaxTokens uint32 `json:"max_tokens"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		observed <- input.MaxTokens
		fmt.Fprint(writer, streamPayload("A complete response."))
	})
	config := defaultProvider()
	config.Model, config.BaseURL = "gpt-4.1", server.URL
	configureModel(t, engine, config)
	project := testProject()
	insertProject(t, engine, project)
	started := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": "", "action": "ask", "instruction": "Discuss the story", "model_selection": ChatModelSelection{ProfileID: "default", OutputMode: "auto"}})
	done := finishJob(t, engine, started)
	assert(t, done.Status == "done" && <-observed == 32768 && done.ModelSelection != nil, "normal request lost its automatic output capacity or selected-model identity")
}

// TestChatSelectionModelCapacities uses advertised limits for automatic output without changing saved profiles.
func TestChatSelectionModelCapacities(t *testing.T) {
	models := testSettings()
	models.Profiles[0].Config.Model = "gpt-4.1"
	models.Profiles[0].Config.MaxTokens = 1000
	selected, choice, err := models.selectChatModel(&Project{}, AIRequest{ModelSelection: &ChatModelSelection{ProfileID: "writer", OutputMode: "auto"}}, "ask")
	assert(t, err == nil && selected.Config.MaxTokens == 32768 && selected.Config.chatOutputAuto, "automatic output did not adopt model capacity")
	assert(t, models.Profiles[0].Config.MaxTokens == 1000 && choice.MaxTokens == 0, "selection overwrote the saved profile or exposed an effective cap as custom intent")
	_, _, err = models.selectChatModel(&Project{}, AIRequest{ModelSelection: &ChatModelSelection{ProfileID: "writer", OutputMode: "custom", MaxTokens: 50000}}, "ask")
	assert(t, err != nil, "custom output above the model capacity was accepted")
	_, _, err = models.selectChatModel(&Project{}, AIRequest{ModelSelection: &ChatModelSelection{ProfileID: "writer", OutputMode: "mystery"}}, "ask")
	assert(t, err != nil, "unknown output mode was accepted")
}

// TestChatReasoningChoiceControlsSupportedThinkingMode overrides a profile's opposing toggle in the frozen request.
func TestChatReasoningChoiceControlsSupportedThinkingMode(t *testing.T) {
	models := testSettings()
	models.Profiles[0].Config = providerPresets()[0].Config
	for _, preset := range providerPresets() {
		if preset.ID == "deepseek" {
			models.Profiles[0].Config = preset.Config
			break
		}
	}
	models.Profiles[0].Config.ThinkingMode = "disabled"
	selected, _, err := models.selectChatModel(&Project{}, AIRequest{ModelSelection: &ChatModelSelection{ProfileID: "writer", OutputMode: "auto", ReasoningEffort: "high"}}, "ask")
	assert(t, err == nil && selected.Config.ThinkingMode == "enabled" && selected.Config.ReasoningEffort == "high", "chat reasoning was overridden by a disabled profile toggle")
	selected, _, err = models.selectChatModel(&Project{}, AIRequest{ModelSelection: &ChatModelSelection{ProfileID: "writer", OutputMode: "auto", ReasoningEffort: "none"}}, "ask")
	assert(t, err == nil && selected.Config.ThinkingMode == "disabled" && optionalMode(selected.Config.ReasoningEffort) == "", "chat could not disable a supported thinking mode")
}

// TestDeepSeekFinalReasoningRemainsPrivateAndReplayable retains thinking from a tool-free final request for later tools.
func TestDeepSeekFinalReasoningRemainsPrivateAndReplayable(t *testing.T) {
	var config ProviderConfig
	for _, preset := range providerPresets() {
		if preset.ID == "deepseek" {
			config = preset.Config
			break
		}
	}
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: "+jsonText(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning_content": "private reasoning"}}}})+"\n\n"+streamPayload("Visible final answer."))
	})
	config.BaseURL, config.ThinkingMode, config.nativeTools = server.URL, "enabled", false
	output, err := streamModelOutput(context.Background(), config, []Message{{Role: "user", Content: "Finish in prose"}}, func(string) {})
	assert(t, err == nil && output.Reasoning == "private reasoning" && output.Text == "Visible final answer.", "tool-free final request lost private DeepSeek reasoning")
	config.nativeTools = true
	wire := modelWireMessages([]Message{assistantModelMessage(output)}, config)
	assert(t, wire[0]["reasoning_content"] == "private reasoning", "later tool request omitted the prior final response's reasoning")
	config.nativeTools, config.ThinkingMode, config.ReasoningEffort = false, "disabled", ""
	disabled, err := streamModelOutput(context.Background(), config, []Message{{Role: "user", Content: "Answer directly"}}, func(string) {})
	assert(t, err == nil && disabled.Reasoning == "", "disabled thinking retained private reasoning content")
}

// TestChatRequestTokenBudgetIncludesToolsAndRemainingOutput validates complete input before the network request.
func TestChatRequestTokenBudgetIncludesToolsAndRemainingOutput(t *testing.T) {
	config := defaultProvider()
	config.Model = "fixture-model"
	config.MaxTokens, config.chatOutputAuto = 3000, true
	config.ModelLimits = &ModelLimits{ContextWindowTokens: 5000, MaxOutputTokens: 3000}
	messages := []Message{{Role: "user", Content: strings.Repeat("中", 1400)}}
	adjusted, err := applyRequestTokenBudget(config, messages)
	assert(t, err == nil && adjusted.MaxTokens < config.MaxTokens && adjusted.MaxTokens > 1000, "automatic output was not bounded by remaining input capacity")
	config.chatOutputAuto = false
	_, err = applyRequestTokenBudget(config, messages)
	assert(t, err != nil, "custom output silently exceeded the remaining context window")
	without := requestInputTokens(config, messages)
	config.nativeTools = true
	assert(t, requestInputTokens(config, messages) > without, "native function declarations were omitted from token estimates")
	config.ModelLimits.MaxInputTokens = 100
	_, err = applyRequestTokenBudget(config, messages)
	assert(t, err != nil, "advertised model input limit was ignored")
}

// TestAutomaticBudgetLeavesInputRoomForSmallWindows avoids reserving the complete context for automatic output.
func TestAutomaticBudgetLeavesInputRoomForSmallWindows(t *testing.T) {
	config := defaultProvider()
	config.Model, config.MaxTokens, config.chatOutputAuto = "small-model", 8192, true
	config.ModelLimits = &ModelLimits{ContextWindowTokens: 8192, MaxOutputTokens: 8192}
	prompt := Prompt{Messages: []Message{{Role: "system", Content: "Assist the author."}, {Role: "user", Content: "Continue."}}}
	prepareTokenBudget(&prompt, &config, &ChatModelSelection{ProfileID: "small", OutputMode: "auto"})
	assert(t, prompt.Budget.CanSend && tokenInputLimit(config) > 1000 && prompt.Budget.OutputTokens < 8192, "automatic output consumed the entire small-model window")
}

// TestChatPreviewAllowsCompressibleHistory keeps long history from blocking its own compression task.
func TestChatPreviewAllowsCompressibleHistory(t *testing.T) {
	config := defaultProvider()
	config.Model, config.MaxTokens, config.chatOutputAuto = "fixture-model", 1000, true
	config.ModelLimits = &ModelLimits{ContextWindowTokens: 6000, MaxOutputTokens: 1000}
	prompt := Prompt{Messages: []Message{
		{Role: "system", Content: "Preserve author choices."},
		{Role: "user", Content: strings.Repeat("a", 14000)},
		{Role: "assistant", Content: strings.Repeat("b", 14000)},
		{Role: "user", Content: "Recent question"},
		{Role: "assistant", Content: "Recent answer"},
		{Role: "user", Content: "Continue from those choices."},
	}, HistoryGroups: []HistoryGroup{{End: 3}, {End: 5}}}
	prepareTokenBudget(&prompt, &config, &ChatModelSelection{ProfileID: "writer", OutputMode: "auto"})
	assert(t, prompt.Budget.InputTokensEstimate > 6000 && prompt.Budget.NeedsCompaction && prompt.Budget.CanSend, "compressible old history was rejected before compaction")
	prompt.Messages[0].Content = strings.Repeat("中", 3500)
	prepareTokenBudget(&prompt, &config, prompt.ModelSelection)
	assert(t, !prompt.Budget.CanSend && prompt.Budget.Warning != "", "oversized fixed instructions were treated as compressible")
}

// TestLegacyModelChoiceStillUsesTokenCompaction protects CLI callers that rely on task routing without explicit selection.
func TestLegacyModelChoiceStillUsesTokenCompaction(t *testing.T) {
	config := defaultProvider()
	config.Model, config.MaxTokens, config.ContextChars = "legacy-small", 1024, 24000
	config.ModelLimits = &ModelLimits{ContextWindowTokens: 8192, MaxOutputTokens: 1024}
	prompt := Prompt{Messages: []Message{
		{Role: "system", Content: "Preserve the author's constraints."},
		{Role: "user", Content: strings.Repeat("中", 5000)},
		{Role: "assistant", Content: strings.Repeat("文", 5000)},
		{Role: "user", Content: "Recent question"},
		{Role: "assistant", Content: "Recent answer"},
		{Role: "user", Content: "Continue."},
	}, HistoryGroups: []HistoryGroup{{End: 3}, {End: 5}}}
	prepareTokenBudget(&prompt, &config, nil)
	assert(t, prompt.Budget.CanSend && prompt.Budget.NeedsCompaction && config.ContextChars < 24000 && prompt.ModelSelection == nil, "legacy routing skipped known token limits or was silently changed to a chat model choice")
	_, err := applyRequestTokenBudget(config, prompt.Messages)
	assert(t, err != nil, "fixture should require compression before a model request")
}

// TestChatOmissionsReplaceCachedBibleAndManifest describes exactly the visible record markers sent this turn.
func TestChatOmissionsReplaceCachedBibleAndManifest(t *testing.T) {
	project := testProject()
	project.Entities = []Entity{}
	for i := 1; i <= 25; i++ {
		project.Entities = append(project.Entities, Entity{ID: fmt.Sprintf("entity-%02d", i), Name: fmt.Sprintf("Character %d", i), Kind: "character"})
	}
	baseline := buildStoryBibleLayer1(&project, 64000, nil)
	request := AIRequest{Action: "agent", Instruction: "Discuss the remaining characters", WorkspaceContext: &WorkspaceContext{OmitEntityIDs: []string{"entity-01"}}, conversation: &Conversation{BibleBaseline: baseline, LastBibleHash: hashText(baseline)}}
	prompt, err := prepareAI(&project, request, 64000)
	assert(t, err == nil, fmt.Sprint(err))
	current := prompt.Messages[len(prompt.Messages)-1].Content
	assert(t, !strings.Contains(current, "[entity-01]") && strings.Contains(current, "[entity-25]"), "Current story snapshot reintroduced an omitted record")
	assert(t, !strings.Contains(prompt.Messages[0].Content, "[entity-25]"), "Dynamic story records changed the reusable system prefix")
	withoutOmissions := request
	withoutOmissions.WorkspaceContext = nil
	baselinePrompt, err := prepareAI(&project, withoutOmissions, 64000)
	assert(t, err == nil && baselinePrompt.Messages[0].Content == prompt.Messages[0].Content, "A changed story selection rewrote the stable instruction prefix")
	records := carriedContextRecords(&project, request, prompt)
	assert(t, len(records) == 24 && records[0].ID == "entity:entity-02" && records[23].ID == "entity:entity-25", "carried record list disagrees with the prepared model context")
}

// TestChatModelBudgetKeepsFullSelectedText replaces fixed excerpts with the selected model's inspectable capacity.
func TestChatModelBudgetKeepsFullSelectedText(t *testing.T) {
	project := testProject()
	selected := strings.Repeat("选", 2100) + "SELECTED_END"
	chapter := &Chapter{ID: "active", Title: "Chapter", Body: "CHAPTER_START" + strings.Repeat("文", 1800)}
	context := &WorkspaceContext{SelectedText: selected}
	legacy := buildWorkspaceContextBlock(&project, context, chapter)
	assert(t, !strings.Contains(legacy, "SELECTED_END") && !strings.Contains(legacy, "CHAPTER_START"), "legacy excerpt contract changed")
	full := buildWorkspaceContextBlock(&project, context, chapter, true)
	assert(t, strings.Contains(full, selected) && strings.Contains(full, chapter.Body), "selected-model context silently truncated author-selected prose")
	context.OmitChapter, context.OmitSelectedText = true, true
	assert(t, buildWorkspaceContextBlock(&project, context, chapter, true) == "", "full-text mode ignored author exclusions")
}

// TestChatSelectionDispatchPreviewRejectsUnavailableModel validates explicit choices through the real command boundary.
func TestChatSelectionDispatchPreviewRejectsUnavailableModel(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	config := defaultProvider()
	config.Model = "fixture-model"
	configureModel(t, engine, config)
	selection := &ChatModelSelection{ProfileID: "default", OutputMode: "custom", MaxTokens: 1024}
	prompt := invoke[Prompt](t, engine, "prepare_ai", map[string]any{"project_id": project.ID, "chapter_id": "", "action": "ask", "instruction": "Discuss this story", "model_selection": selection})
	assert(t, prompt.ModelSelection != nil && prompt.ModelSelection.ProfileID == "default" && prompt.Budget != nil && prompt.Budget.Estimated, "preview did not expose the accepted model and token estimate")
	selection.ProfileID = "missing-profile"
	reject(t, engine, "prepare_ai", map[string]any{"project_id": project.ID, "chapter_id": "", "action": "ask", "instruction": "Discuss this story", "model_selection": selection})
}
