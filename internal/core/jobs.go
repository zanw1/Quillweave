package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type Job struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	ChapterID       string `json:"chapter_id"`
	Action          string `json:"action"`
	Instruction     string `json:"instruction"`
	Status          string `json:"status"`
	Text            string `json:"text"`
	Error           string `json:"error"`
	Progress        string `json:"progress"`
	CreatedAt       string `json:"created_at"`
	Model           string `json:"model"`
	Prompt          Prompt `json:"prompt"`
	ctx             context.Context
	cancel          context.CancelFunc
	committing      bool
	providerContext *providerContext
}

type providerContext struct {
	Fingerprint string `json:"fingerprint"`
	Reasoning   string `json:"reasoning,omitempty"`
	Encrypted   string `json:"encrypted,omitempty"`
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
	}{Job: job.snapshot(), ProviderContext: private})
}

// restoreProviderHistory restores completed planning context only for a matching project and model identity.
func (engine *Engine) restoreProviderHistory(request AIRequest, config ProviderConfig, profileID string) (AIRequest, error) {
	if !conversationAction(request.Action) || !preservesProviderContext(config) {
		return request, nil
	}
	request.History = append([]Message{}, request.History...)
	for i := max(0, len(request.History)-8); i < len(request.History); i++ {
		message := &request.History[i]
		if message.Role != "assistant" || message.SourceJobID == "" {
			continue
		}
		private, text, err := engine.store.RunProviderContext(request.ProjectID, message.SourceJobID)
		if err != nil {
			return request, err
		}
		if private.Fingerprint == providerIdentity(config, profileID) && text == message.Content {
			message.reasoningContent, message.encryptedContent = private.Reasoning, private.Encrypted
		}
	}
	return request, nil
}

// snapshot copies task output for synchronized encoding and background updates.
func (job *Job) snapshot() Job { return *job }

// start enforces one active task per project and the four-task global limit.
func (engine *Engine) start(request AIRequest) (any, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return nil, errors.New("The application is closing")
	}
	project, err := engine.store.Get(request.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := engine.refreshModelsLocked(); err != nil {
		return nil, err
	}
	task := request.Action
	if task == "wiki" {
		task = "wiki_extract"
	}
	selected, err := engine.models.Select(&project, task)
	if err != nil {
		return nil, err
	}
	synthesis := selected
	if request.Action == "wiki" {
		synthesis, err = engine.models.Select(&project, "wiki_compile")
		if err != nil {
			return nil, err
		}
	}
	prompt := Prompt{Messages: []Message{}, Context: []ContextItem{}}
	if request.Action == "wiki" {
		sourceID := request.SourceID
		if sourceID == "" {
			sourceID = request.ChapterID
		}
		source, _, ok := project.SourceText(sourceID)
		if !ok {
			return nil, errors.New("Select a chapter or source to analyze")
		}
		if strings.TrimSpace(source) == "" {
			return nil, errors.New("There is no text to analyze")
		}
		prompt.SourceID = sourceID
		prompt.SourceHash = hashText(source)
		prompt.Chars = charCount(source)
	} else {
		request, err = engine.restoreProviderHistory(request, selected.Config, selected.ProfileID)
		if err != nil {
			return nil, err
		}
		prompt, err = prepareAI(&project, request, int(selected.Config.ContextChars))
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(engine.ctx)
	job := &Job{ID: newID(), ProjectID: request.ProjectID, ChapterID: request.ChapterID, Action: request.Action, Instruction: request.Instruction, Status: "running", Progress: "Connecting to the model…", CreatedAt: now(), Model: selected.ProfileName + " / " + selected.Config.Model, Prompt: prompt, ctx: ctx, cancel: cancel}
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
	engine.jobs[job.ID] = job
	result := job.snapshot()
	engine.jobsMu.Unlock()
	if job.Status == "done" {
		cancel()
		if err := engine.store.SaveRun(job.ID, job.ProjectID, storedJob(job)); err != nil {
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
		current.Progress = progress
	} else {
		current.Status = "error"
		if job.ctx.Err() != nil {
			current.Status = "cancelled"
		}
		current.Error = outcome.Error()
	}
	if err := engine.store.SaveRun(current.ID, current.ProjectID, storedJob(current)); err != nil {
		current.Error = "Unable to save task history: " + err.Error()
	}
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
	if job.Action != "wiki" {
		output, err := streamModelOutput(job.ctx, config, job.Prompt.Messages, func(text string) { engine.appendText(job.ID, text) })
		if err != nil {
			return "", err
		}
		if conversationAction(job.Action) && preservesProviderContext(config) && (output.Reasoning != "" || output.Encrypted != "") {
			engine.jobsMu.Lock()
			if current := engine.jobs[job.ID]; current != nil {
				current.providerContext = &providerContext{Fingerprint: providerIdentity(config, selected.ProfileID), Reasoning: output.Reasoning, Encrypted: output.Encrypted}
			}
			engine.jobsMu.Unlock()
		}
		return "Generation complete. Review the result before applying it", nil
	}
	source, _, ok := project.SourceText(job.Prompt.SourceID)
	if !ok {
		return "", errors.New("Source text not found")
	}
	parts := chunks(source, min(6000, max(1000, int(config.ContextChars)-3000)), 150)
	candidates := []Candidate{}
	for index, part := range parts {
		if job.ctx.Err() != nil {
			return "", errors.New("Task cancelled")
		}
		engine.setProgress(job.ID, fmt.Sprintf("Stage 1: Extracting source evidence %d/%d", index+1, len(parts)))
		messages := extractionPrompt(part, project)
		if uint64(messageChars(messages)) > config.ContextChars {
			return "", errors.New("Character details and tracking focus exceed the context budget. Increase the budget")
		}
		response, err := streamModel(job.ctx, config, messages, func(text string) { engine.appendText(job.ID, text) })
		if err != nil {
			return "", err
		}
		parsed, err := parseCandidates(response, part)
		if err != nil {
			return "", err
		}
		candidates = append(candidates, parsed...)
		engine.appendText(job.ID, "\n\n")
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
	sections, err := engine.compileMemories(job, project, synthesis.Config, facts)
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
	return fmt.Sprintf("Evidence extraction and story memory compilation complete. Added %d evidence records for review. The memory awaits author approval", count), nil
}

// compileMemories groups up to twelve evidence records per batch within the model context budget.
func (engine *Engine) compileMemories(job Job, project *Project, config ProviderConfig, facts []Fact) ([]MemorySection, error) {
	sections := []MemorySection{}
	for start := 0; start < len(facts); {
		end := start + 1
		messages := memoryPrompt(project, facts[start:end])
		if uint64(messageChars(messages)) > config.ContextChars {
			return nil, errors.New("One evidence record exceeds the memory context budget. Increase the budget")
		}
		for end < len(facts) && end-start < 12 {
			extended := memoryPrompt(project, facts[start:end+1])
			if uint64(messageChars(extended)) > config.ContextChars {
				break
			}
			messages = extended
			end++
		}
		engine.setProgress(job.ID, fmt.Sprintf("Stage 2: Compiling story memory from evidence %d–%d", start+1, end))
		response, err := streamModel(job.ctx, config, messages, func(text string) { engine.appendText(job.ID, text) })
		if err != nil {
			return nil, err
		}
		section, err := parseMemory(response, facts[start:end])
		if err != nil {
			return nil, err
		}
		sections = append(sections, section)
		engine.appendText(job.ID, "\n\n")
		start = end
	}
	return sections, nil
}
