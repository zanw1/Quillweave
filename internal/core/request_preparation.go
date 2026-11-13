package core

import (
	"errors"
	"fmt"
	"strings"
)

type preparedTask struct {
	project    Project
	request    AIRequest
	prior      string
	resumeText string
	selected   SelectedModel
	synthesis  SelectedModel
	prompt     Prompt
}

// prepareTaskLocked shares the exact ordering of preview and submission; the caller holds engine.mu.
func (engine *Engine) prepareTaskLocked(request AIRequest, preview ...bool) (preparedTask, error) {
	task := preparedTask{request: request}
	if request.CommitPolicy == "" {
		request.CommitPolicy = "review"
	}
	if !contains([]string{"review", "auto"}, request.CommitPolicy) {
		return task, errors.New("Choose review or auto for the output save policy")
	}
	if err := engine.reconcileJobsLocked(); err != nil {
		return task, err
	}
	project, err := engine.store.Get(request.ProjectID)
	if err != nil {
		return task, err
	}
	project.requestLanguage, project.taskLanguage = request.UILanguage, request.Language
	request, prior, err := engine.store.prepareConversation(request)
	if err != nil {
		return task, err
	}
	task.project, task.request, task.prior = project, request, prior
	if prior != "" {
		return task, nil
	}
	if request.ChapterID != "" && findChapter(&project, request.ChapterID) == nil {
		return task, errors.New("Select a chapter from this project")
	}
	if request.SourceID != "" {
		if _, _, found := project.SourceText(request.SourceID); !found {
			return task, errors.New("Select a source from this project")
		}
	}
	request, task.resumeText, err = engine.resumeRequest(&project, request)
	if err != nil {
		return task, err
	}
	if err := engine.refreshModelsLocked(); err != nil {
		return task, err
	}
	modelAction := modelTask(request.Action)
	if modelAction == "wiki" {
		modelAction = "wiki_extract"
	}
	modelChosen := request.ModelSelection != nil || request.conversation != nil && request.conversation.ModelSelection != nil
	task.selected, request.ModelSelection, err = engine.models.selectChatModel(&project, request, modelAction)
	if err != nil {
		if modelChosen {
			return task, err
		}
		if len(preview) == 0 || !preview[0] {
			return task, err
		}
		task.selected = SelectedModel{Config: ProviderConfig{ContextChars: 24000}}
	}
	modelReady := err == nil
	task.selected.Config.nativeTools = modelReady && request.Action == "agent" && !engine.nativeToolFallback[nativeCapabilityKey(task.selected)]
	task.synthesis = task.selected
	if request.Action == "wiki" {
		if request.ModelSelection == nil {
			task.synthesis, err = engine.models.Select(&project, "wiki_compile")
			modelReady = modelReady && err == nil
			if err != nil && (len(preview) == 0 || !preview[0]) {
				return task, err
			}
		}
		task.prompt, err = wikiSourcePrompt(&project, request)
		if err == nil {
			source, _, _ := project.SourceText(task.prompt.SourceID)
			var parts []string
			parts, err = prepareWikiChunks(&project, source, task.selected.Config)
			if err == nil {
				task.prompt.Messages = extractionPrompt(parts[0], &project)
				task.prompt.Chars = messageChars(task.prompt.Messages)
			}
		}
	} else {
		request, err = engine.restoreProviderHistory(request, task.selected.Config, task.selected.ProfileID)
		if err != nil {
			return task, err
		}
		if request.Action != "agent" {
			request.Retrieved, err = engine.contextPassages(&project, request)
			if err != nil {
				return task, err
			}
		}
		if request.ExtractionJobID != "" {
			selected, loadErr := engine.loadStoredJob(request.ExtractionJobID)
			if loadErr != nil {
				return task, loadErr
			}
			if selected.ProjectID != project.ID || selected.Status != "done" || strings.TrimSpace(selected.Text) == "" {
				return task, errors.New("Select a completed response from this project")
			}
			request.IntentHint = "commit_extract"
			request.Retrieved = append(request.Retrieved, ContextItem{ID: "J:" + selected.ID, Kind: "selected_response", Title: "Author-selected response", Text: selected.Text})
		}
		var skills []SkillDescriptor
		request, skills, err = engine.prepareSkills(request, skillCatalogTokenBudget(task.selected.Config))
		if err != nil {
			return task, err
		}
		if request.SearchSelection != nil && request.SearchSelection.Mode == "profile" {
			err = engine.validateRequestedSearch(request, task.selected.ProfileID)
			if err != nil {
				return task, err
			}
		}
		toolChars := nativeToolChars(task.selected.Config)
		budget := int(task.selected.Config.ContextChars) - toolChars
		if budget <= 0 {
			return task, errors.New("Native tool definitions exceed the model context budget")
		}
		sourceBudget := budget
		if request.Action == "agent" {
			// Pack story sources after accounting for the actual protocol. Tool
			// results are excerpted separately without truncating required author text.
			sourceBudget -= charCount(agentToolProtocol(task.selected.Config.nativeTools)) + 160
			if sourceBudget <= 0 {
				return task, errors.New("Tool instructions exceed the model context budget")
			}
		}
		task.prompt, err = prepareAI(&project, request, sourceBudget)
		if err == nil && request.Action == "agent" {
			if err = engine.refreshIntegrationsLocked(); err == nil {
				task.prompt, err = prepareAgentPrompt(task.prompt, engine.agentSearchPolicyLocked(request, task.selected.ProfileID), budget, task.selected.Config.nativeTools)
			}
		}
		task.prompt.ToolChars = toolChars
		task.prompt.Chars += toolChars
		task.prompt.Skills = skills
		task.prompt.CarriedRecords = carriedContextRecords(&project, request, task.prompt)
	}
	task.prompt.ForceCompact = request.Compact
	task.selected.Config = engine.withTokenUsageBaseline(task.selected.Config, request)
	prepareTokenBudget(&task.prompt, &task.selected.Config, request.ModelSelection)
	if request.Compact && len(task.prompt.HistoryGroups) == 0 {
		return task, errors.New("This conversation has too few complete exchanges to compact")
	}
	if err == nil && task.prompt.Budget != nil && !task.prompt.Budget.CanSend && len(preview) == 0 {
		return task, errors.New(task.prompt.Budget.Warning)
	}
	task.project, task.request = project, request
	if !modelReady {
		task.prompt.Warning = "No usable model is assigned. This is an offline context preview; configure a model before sending."
	}
	return task, err
}

// resumeRequest validates an interrupted source and adds the same prefix for preview and generation.
func (engine *Engine) resumeRequest(project *Project, request AIRequest) (AIRequest, string, error) {
	if request.ResumeJobID == "" {
		return request, "", nil
	}
	previous, err := engine.loadJob(request.ResumeJobID, request.ProjectID)
	if err != nil {
		return request, "", err
	}
	if request.Action == "agent" {
		if previous.Action != "agent" || previous.Status == "running" || previous.Status == "done" || previous.SessionID != request.SessionID {
			return request, "", errors.New("Select an interrupted agent task in this conversation")
		}
		steps := []string{}
		for _, step := range previous.Steps {
			steps = append(steps, fmt.Sprintf("%s %s [J:%s:%s]", step.Tool, step.Status, previous.ID, step.ID))
		}
		for _, candidate := range previous.ChangeSets {
			steps = append(steps, fmt.Sprintf("Existing candidate %s (%s): %s. Continue its review before proposing the same changes again.", candidate.ID, candidate.Status, truncateSummary(candidate.Summary, 180)))
		}
		context := fmt.Sprintf("Resume task %s. Original goal: %s\nPrevious project revision: %d; current revision: %d. Reread changed story records. Completed tools are recoverable with get_task_step. Existing candidate proposals still require preview and confirmation.\n%s", previous.ID, previous.Instruction, previous.BaseRevision, project.Revision, strings.Join(steps, "\n"))
		if previous.Writing != nil && previous.Writing.Mode != "changeset" {
			request.writingResume = copyWritingRun(previous.Writing)
		}
		request.Retrieved = append(request.Retrieved, ContextItem{ID: "J:" + previous.ID, Kind: "selected_response", Title: "Interrupted task to resume", Text: context})
		return request, "", nil
	}
	if previous.Status == "running" || previous.Status == "done" || previous.ChapterID != request.ChapterID || contains([]string{"wiki", "memory_update", "agent"}, request.Action) {
		return request, "", errors.New("Only interrupted prose in the same chapter can be continued")
	}
	if previous.Prompt.SourceID != "" {
		text, _, ok := project.SourceText(previous.Prompt.SourceID)
		if !ok || hashText(text) != previous.Prompt.SourceHash {
			return request, "", errors.New("The source changed after interruption. Review the new version before continuing")
		}
	}
	if previous.Writing != nil && previous.Writing.Mode != "changeset" {
		request.writingResume = copyWritingRun(previous.Writing)
		request.Retrieved = append([]ContextItem{}, previous.Prompt.Context...)
		for i := range request.Retrieved {
			request.Retrieved[i].Required = true
		}
		text := previous.Writing.Draft
		if contains([]string{"drafting", "revising"}, previous.Writing.Phase) {
			text = previous.Writing.Continuation
			if text == "" && previous.Writing.Calls == 0 {
				text = previous.Text
				request.writingResume.Continuation = text
			}
		}
		return request, text, nil
	}
	if strings.TrimSpace(previous.Text) == "" {
		return request, "", errors.New("There is no partial response to continue")
	}
	request.History = append(request.History, Message{Role: "assistant", Content: previous.Text})
	request.Instruction += "\n" + localText(project, "Continue the interrupted response after the supplied assistant text. Return only the remaining continuation, without repeating that text.", "接着已提供的助手回答续写，只返回尚未输出的部分，不重复已有文本。")
	return request, previous.Text, nil
}

// wikiSourcePrompt describes the full source consumed by the later chunked Wiki stages.
func wikiSourcePrompt(project *Project, request AIRequest) (Prompt, error) {
	id := request.SourceID
	if id == "" {
		id = request.ChapterID
	}
	for _, reference := range project.Sources {
		if reference.ID == id && reference.Category == "craft" {
			return Prompt{}, errors.New("Analyze writing references with /reference. Story Wiki compilation uses this work's prose and world sources")
		}
	}
	source, _, ok := project.SourceText(id)
	if !ok {
		return Prompt{}, errors.New("Select a chapter or source to analyze")
	}
	if strings.TrimSpace(source) == "" {
		return Prompt{}, errors.New("There is no text to analyze")
	}
	return Prompt{Messages: []Message{}, Context: []ContextItem{}, SourceID: id, SourceHash: hashText(source), Chars: charCount(source)}, nil
}
