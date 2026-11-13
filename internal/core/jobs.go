package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Job struct {
	ID                   string              `json:"id"`
	ProjectID            string              `json:"project_id"`
	ChapterID            string              `json:"chapter_id"`
	Action               string              `json:"action"`
	Instruction          string              `json:"instruction"`
	Status               string              `json:"status"`
	Text                 string              `json:"text"`
	Error                string              `json:"error"`
	Progress             string              `json:"progress"`
	CreatedAt            string              `json:"created_at"`
	Model                string              `json:"model"`
	ModelSelection       *ChatModelSelection `json:"model_selection,omitempty"`
	SkillIDs             []string            `json:"skill_ids,omitempty"`
	Prompt               Prompt              `json:"prompt"`
	SessionID            string              `json:"session_id,omitempty"`
	Language             string              `json:"language,omitempty"`
	IntentHint           string              `json:"intent_hint,omitempty"`
	CommitPolicy         string              `json:"commit_policy,omitempty"`
	WorkspaceContext     *WorkspaceContext   `json:"workspace_context,omitempty"`
	Steps                []AgentStep         `json:"steps"`
	Proposals            []StudioProposal    `json:"proposals"`
	ChangeSets           []ChangeSet         `json:"changesets,omitempty"`
	Calls                int                 `json:"calls"`
	Metrics              []ModelCallMetric   `json:"metrics,omitempty"`
	SearchSelection      *SearchSelection    `json:"search_selection,omitempty"`
	SearchModelProfileID string              `json:"search_model_profile_id,omitempty"`
	BaseRevision         uint64              `json:"base_revision"`
	Writing              *WritingRun         `json:"writing,omitempty"`
	Activity             []ChatActivity      `json:"activity,omitempty"`
	checkpointedAt       time.Time
	ctx                  context.Context
	cancel               context.CancelFunc
	committing           bool
	providerContext      *providerContext
	ownerID              string
	pendingFinal         *Job
	lastRequestBody      string
	lastRequestConfig    string
}

type providerContext struct {
	Fingerprint       string          `json:"fingerprint"`
	Reasoning         string          `json:"reasoning,omitempty"`
	Encrypted         string          `json:"encrypted,omitempty"`
	VisibleText       string          `json:"visible_text,omitempty"`
	Messages          []replayMessage `json:"messages,omitempty"`
	UserContent       string          `json:"-"`
	LastRequestBody   string          `json:"last_request_body,omitempty"`
	OptionFingerprint string          `json:"option_fingerprint,omitempty"`
}

// storedJob persists private context for completed planning turns in a separate local field.
func storedJob(job *Job) string {
	var private *providerContext
	if job.Status == "done" {
		private = job.providerContext
	}
	return jsonText(struct {
		Job
		ProviderContext *providerContext `json:"provider_context,omitempty"`
		Owner           string           `json:"execution_owner,omitempty"`
	}{Job: job.snapshot(), ProviderContext: private, Owner: job.ownerID})
}

// restoreProviderHistory restores completed planning context only for a matching project and model identity.
func (engine *Engine) restoreProviderHistory(request AIRequest, config ProviderConfig, profileID string) (AIRequest, error) {
	if !conversationAction(request.Action) {
		request.conversation = nil
		return request, nil
	}
	if request.conversation != nil {
		return engine.restoreConversationHistory(request, config, profileID)
	}
	if !preservesProviderContext(config) {
		return request, nil
	}
	request.History = append([]Message{}, request.History...)
	for i := max(0, len(request.History)-maxHistoryMessages); i < len(request.History); i++ {
		message := &request.History[i]
		if message.Role != "assistant" || message.SourceJobID == "" {
			continue
		}
		private, text, err := engine.store.RunProviderContext(request.ProjectID, message.SourceJobID)
		if err != nil {
			return request, err
		}
		if providerContextMatches(private, config, profileID) && text == message.Content {
			message.reasoningContent, message.encryptedContent = private.Reasoning, private.Encrypted
			if private.VisibleText != "" {
				message.Content = private.VisibleText
			}
		}
	}
	return request, nil
}

// retainProviderOutput binds private continuation state to the exact visible provider response.
func (engine *Engine) retainProviderOutput(jobID string, selected SelectedModel, output modelOutput) {
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	if current := engine.jobs[jobID]; current != nil {
		messages := []Message{}
		if len(current.Prompt.Messages) > 0 {
			messages = append(messages, current.Prompt.Messages[len(current.Prompt.Messages)-1])
		}
		messages = append(messages, Message{Role: "assistant", Content: output.Text, reasoningContent: output.Reasoning, encryptedContent: output.Encrypted})
		current.providerContext = replayContext(selected, output, messages)
		current.providerContext.LastRequestBody = compressRequestBody(current.lastRequestBody)
	}
}

// snapshot copies task output for synchronized encoding and background updates.
func (job *Job) snapshot() Job {
	value := *job
	value.Steps = append([]AgentStep{}, job.Steps...)
	value.Activity = append([]ChatActivity{}, job.Activity...)
	value.Proposals = append([]StudioProposal{}, job.Proposals...)
	value.ChangeSets = append([]ChangeSet{}, job.ChangeSets...)
	for i := range value.ChangeSets {
		value.ChangeSets[i].Operations = append([]ChangeOperation{}, value.ChangeSets[i].Operations...)
		value.ChangeSets[i].Skipped = append([]ChangeOperation{}, value.ChangeSets[i].Skipped...)
	}
	value.Metrics = append([]ModelCallMetric{}, job.Metrics...)
	value.SkillIDs = append([]string{}, job.SkillIDs...)
	value.Writing = copyWritingRun(job.Writing)
	if job.ModelSelection != nil {
		selection := *job.ModelSelection
		value.ModelSelection = &selection
	}
	return value
}

// start enforces one active task per project and the four-task global limit.
func (engine *Engine) start(request AIRequest) (any, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return nil, errors.New("The application is closing")
	}
	prepared, err := engine.prepareTaskLocked(request)
	if err != nil {
		return nil, err
	}
	if prepared.prior != "" {
		return engine.loadJob(prepared.prior, request.ProjectID)
	}
	project, request := prepared.project, prepared.request
	selected, synthesis, prompt, resumeText := prepared.selected, prepared.synthesis, prepared.prompt, prepared.resumeText
	ctx, cancel := context.WithCancel(engine.ctx)
	job := &Job{ID: newID(), ProjectID: request.ProjectID, ChapterID: request.ChapterID, Action: request.Action, Instruction: request.Instruction, Status: "running", Progress: "Connecting to the model…", CreatedAt: now(), Model: selected.ProfileName + " / " + selected.Config.Model, Prompt: prompt, ctx: ctx, cancel: cancel, SessionID: request.SessionID, Language: responseLanguage(&project, request.Language), IntentHint: request.IntentHint, CommitPolicy: request.CommitPolicy, WorkspaceContext: request.WorkspaceContext, BaseRevision: project.Revision, Steps: []AgentStep{}, Proposals: []StudioProposal{}, ChangeSets: []ChangeSet{}}
	job.Text = resumeText
	job.Writing = copyWritingRun(request.writingResume)
	job.ModelSelection = request.ModelSelection
	job.SkillIDs = append([]string{}, request.SkillIDs...)
	job.SearchSelection = request.SearchSelection
	job.SearchModelProfileID = selected.ProfileID
	job.ownerID = engine.owner.id
	engine.seedRequestComparison(job, request)
	if request.Action == "wiki" {
		job.Model += " → " + synthesis.ProfileName + " / " + synthesis.Config.Model
		if !request.Force {
			fingerprint, err := modelsFingerprint(&project, prompt.SourceID, selected.Config, synthesis.Config)
			if err != nil {
				cancel()
				return nil, err
			}
			if memory := cachedCompilation(&project, prompt.SourceID, fingerprint); memory != nil {
				job.Status = "done"
				job.Progress = "Reused the previous compilation because the source, tracking rules, and models match"
				sections := []string{}
				for _, section := range memory.Sections {
					sections = append(sections, section.Summary)
				}
				job.Text = strings.Join(sections, "\n\n")
			}
		}
	}
	engine.jobsMu.Lock()
	running := 0
	for _, existing := range engine.jobs {
		if existing.Status == "running" {
			running++
			if existing.ProjectID == request.ProjectID {
				engine.jobsMu.Unlock()
				cancel()
				return nil, errors.New("This project already has a running task. Wait or cancel it")
			}
		}
	}
	if running >= 4 {
		engine.jobsMu.Unlock()
		cancel()
		return nil, errors.New("Four tasks are already running")
	}
	if len(engine.jobs) >= 100 {
		for id, existing := range engine.jobs {
			if existing.Status != "running" {
				existing.cancel()
				delete(engine.jobs, id)
			}
		}
	}
	prior, err := engine.store.beginDurableJob(request, job)
	if err != nil || prior != "" {
		engine.jobsMu.Unlock()
		cancel()
		if err != nil {
			return nil, err
		}
		return engine.loadJob(prior, request.ProjectID)
	}
	engine.jobs[job.ID] = job
	result := job.snapshot()
	engine.jobsMu.Unlock()
	if job.Status == "done" {
		cancel()
		engine.jobsMu.Lock()
		defer engine.jobsMu.Unlock()
		if err := engine.commitTerminalLocked(job); err != nil {
			return nil, err
		}
		return result, nil
	}
	engine.workers.Add(1)
	go engine.executeJob(job.snapshot(), project, selected, synthesis)
	return result, nil
}

// appendText appends generated text to the protected task snapshot for author review.
func (engine *Engine) appendText(id, text string) {
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	if job := engine.jobs[id]; job != nil {
		job.Text += text
		if time.Since(job.checkpointedAt) >= time.Second {
			job.checkpointedAt = time.Now()
			if err := engine.store.SaveRun(job.ID, job.ProjectID, storedJob(job)); err != nil {
				job.Error = "Unable to save a recovery checkpoint: " + err.Error()
			}
		}
	}
}

// noteModelCall counts real model requests rather than inferring cost from task labels.
func (engine *Engine) noteModelCall(id string) {
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	if job := engine.jobs[id]; job != nil {
		job.Calls++
	}
}

// setProgress updates stage progress using credential-free messages.
func (engine *Engine) setProgress(id, text string) {
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	if job := engine.jobs[id]; job != nil {
		job.Progress = text
	}
}

// executeJob records successful, cancelled, or failed completion and persists the final snapshot.
func (engine *Engine) executeJob(job Job, project Project, selected, synthesis SelectedModel) {
	defer engine.workers.Done()
	defer job.cancel()
	stopWatching := engine.watchJobCancellation(job)
	defer stopWatching()
	progress, outcome := engine.runJob(job, &project, selected, synthesis)
	// Use the same lock order as commands to avoid settings-save and task-completion deadlocks.
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	current := engine.jobs[job.ID]
	if current == nil {
		return
	}
	if outcome == nil && job.ctx.Err() != nil && !current.committing {
		outcome = errors.New("Task cancelled")
	}
	if outcome == nil {
		current.Status = "done"
		current.Error = ""
		current.Progress = progress
	} else {
		current.Status = "error"
		if job.ctx.Err() != nil {
			current.Status = "cancelled"
		}
		current.Error = outcome.Error()
	}
	_ = engine.commitTerminalLocked(current)
}

// beginCommit shares the task lock with cancellation and marks the start of irrevocable Wiki commit.
// The caller holds engine.mu, matching the task completion lock order.
func (engine *Engine) beginCommit(id string) error {
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	job := engine.jobs[id]
	if job == nil {
		return errors.New("Task not found")
	}
	if job.ctx.Err() != nil {
		return errors.New("Task cancelled")
	}
	job.committing = true
	return nil
}

// runJob validates both Wiki stages before atomically publishing knowledge.
func (engine *Engine) runJob(job Job, project *Project, selected, synthesis SelectedModel) (string, error) {
	config := selected.Config
	project.requestLanguage = job.Language
	project.taskLanguage = job.Language
	var err error
	job, err = engine.compactJobConversation(job, selected)
	if err != nil {
		return "", err
	}
	if job.Action == "agent" {
		return engine.runAgent(job, project, selected)
	}
	if contains([]string{"continue", "rewrite"}, job.Action) {
		if job.IntentHint == "write_from_outline" && job.Action == "continue" {
			if err := engine.ensureWritingChapter(project, &job, agentStepEnvelope{ChapterID: job.ChapterID}); err != nil {
				return "", err
			}
		}
		mode := "append"
		if job.Action == "rewrite" {
			mode = "replace"
		}
		_, _, err := engine.runChapterProduction(job, project, selected, job.Prompt, mode, 1, agentStepEnvelope{})
		return writingResultSummary(project, engine.writingOutcome(job.ID)), err
	}
	if job.Action != "wiki" {
		output, err := engine.streamJobModel(job.ID, "generation", job.ctx, config, job.Prompt.Messages, func(text string) { engine.appendText(job.ID, text) }, selected.ProfileID)
		if err != nil {
			return "", err
		}
		if _, propErr := engine.recordProposals(job.ID, output.Text, project); propErr != nil {
			return "", propErr
		}
		if conversationAction(job.Action) {
			engine.retainProviderOutput(job.ID, selected, output)
		}
		return "Generation complete. Review the result before applying it", nil
	}
	source, _, ok := project.SourceText(job.Prompt.SourceID)
	if !ok {
		return "", errors.New("Source text not found")
	}
	parts, err := prepareWikiChunks(project, source, config)
	if err != nil {
		return "", err
	}
	candidates := []Candidate{}
	skippedTotal := 0
	for index, part := range parts {
		if job.ctx.Err() != nil {
			return "", errors.New("Task cancelled")
		}
		engine.setProgress(job.ID, fmt.Sprintf("Stage 1: Extracting source evidence %d/%d", index+1, len(parts)))
		messages := extractionPrompt(part, project)
		if err := wikiMessagesBudget(config, messages); err != nil {
			return "", err
		}
		output, err := engine.streamJobModel(job.ID, "evidence_extraction", job.ctx, config, messages, func(text string) { engine.appendText(job.ID, text) }, selected.ProfileID)
		if err != nil {
			return "", err
		}
		parsed, skipped, err := parseCandidates(output.Text, part)
		if err != nil {
			return "", err
		}
		candidates = append(candidates, parsed...)
		if len(skipped) > 0 {
			skippedTotal += len(skipped)
			engine.appendText(job.ID, fmt.Sprintf("\n\nSkipped %d invalid candidates in this chunk:\n- %s\n", len(skipped), strings.Join(skipped, "\n- ")))
		}
		engine.appendText(job.ID, "\n\n")
	}
	if len(candidates) == 0 {
		if skippedTotal > 0 {
			return "", errors.New("Every extracted candidate failed evidence validation. Review the raw analysis and retry; no compilation was cached")
		}
		return "The source contained no supported evidence records. No compilation was cached", nil
	}
	staged := cloneProject(*project)
	if _, err := ingest(&staged, job.Prompt.SourceID, job.Prompt.SourceHash, candidates, config.Model); err != nil {
		return "", err
	}
	facts := []Fact{}
	for _, fact := range staged.Facts {
		if fact.SourceID == job.Prompt.SourceID && fact.SourceHash == job.Prompt.SourceHash && fact.Status != "rejected" {
			facts = append(facts, fact)
		}
	}
	if len(facts) == 0 {
		return "The extracted evidence was already rejected. No compilation was cached", nil
	}
	sections, err := engine.compileMemories(job, project, synthesis.Config, facts, synthesis.ProfileID)
	if err != nil {
		return "", err
	}
	if job.ctx.Err() != nil {
		return "", errors.New("Task cancelled")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if err := engine.beginCommit(job.ID); err != nil {
		return "", err
	}
	latest, err := engine.store.Get(job.ProjectID)
	if err != nil {
		return "", err
	}
	latest.requestLanguage = job.Language
	latest.taskLanguage = job.Language
	if rulesHash(&latest) != rulesHash(project) {
		return "", errors.New("Tracking focus changed during analysis. Compile the source again")
	}
	revision := latest.Revision
	count, err := ingest(&latest, job.Prompt.SourceID, job.Prompt.SourceHash, candidates, config.Model)
	if err != nil {
		return "", err
	}
	for _, section := range sections {
		for _, id := range section.EvidenceIDs {
			found := false
			for _, fact := range latest.Facts {
				if fact.ID == id && fact.Status != "rejected" && factCurrent(&latest, &fact) {
					found = true
					break
				}
			}
			if !found {
				return "", errors.New("Evidence changed during compilation. Compile the source again")
			}
		}
	}
	fingerprint, err := modelsFingerprint(project, job.Prompt.SourceID, config, synthesis.Config)
	if err != nil {
		return "", err
	}
	latest.Compilations = append(latest.Compilations, WikiCompilation{ID: newID(), SourceID: job.Prompt.SourceID, SourceHash: job.Prompt.SourceHash, RulesHash: rulesHash(project), Fingerprint: fingerprint, CompilerVersion: CompilerVersion, Model: job.Model, Chunks: uint64(len(parts)), Sections: sections, Status: "pending", CreatedAt: now()})
	if err := engine.store.Save(&latest, revision); err != nil {
		return "", err
	}
	summary := fmt.Sprintf("Evidence extraction and story memory compilation complete. Added %d evidence records for review. The memory awaits author approval", count)
	if skippedTotal > 0 {
		summary += fmt.Sprintf(". %d invalid candidates were skipped and listed in the raw analysis", skippedTotal)
	}
	return summary, nil
}

// compileMemories groups up to twelve evidence records per batch within the model context budget.
func (engine *Engine) compileMemories(job Job, project *Project, config ProviderConfig, facts []Fact, profileIDs ...string) ([]MemorySection, error) {
	sections := []MemorySection{}
	for start := 0; start < len(facts); {
		end := start + 1
		messages := memoryPrompt(project, facts[start:end])
		if err := wikiMessagesBudget(config, messages); err != nil {
			return nil, fmt.Errorf("One evidence record exceeds the memory context budget: %w", err)
		}
		for end < len(facts) && end-start < 12 {
			extended := memoryPrompt(project, facts[start:end+1])
			if wikiMessagesBudget(config, extended) != nil {
				break
			}
			messages = extended
			end++
		}
		engine.setProgress(job.ID, fmt.Sprintf("Stage 2: Compiling story memory from evidence %d–%d", start+1, end))
		output, err := engine.streamJobModel(job.ID, "memory_compilation", job.ctx, config, messages, func(text string) { engine.appendText(job.ID, text) }, profileIDs...)
		if err != nil {
			return nil, err
		}
		section, err := parseMemory(output.Text, facts[start:end])
		if err != nil {
			return nil, err
		}
		sections = append(sections, section)
		engine.appendText(job.ID, "\n\n")
		start = end
	}
	return sections, nil
}
