package core

import (
	"errors"
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
	task.selected, err = engine.models.Select(&project, modelAction)
	if err != nil {
		if len(preview) == 0 || !preview[0] {
			return task, err
		}
		task.selected = SelectedModel{Config: ProviderConfig{ContextChars: 24000}}
	}
	modelReady := err == nil
	task.synthesis = task.selected
	if request.Action == "wiki" {
		task.synthesis, err = engine.models.Select(&project, "wiki_compile")
		modelReady = modelReady && err == nil
		if err != nil && (len(preview) == 0 || !preview[0]) {
			return task, err
		}
		task.prompt, err = wikiSourcePrompt(&project, request)
	} else {
		request, err = engine.restoreProviderHistory(request, task.selected.Config, task.selected.ProfileID)
		if err != nil {
			return task, err
		}
		request.Retrieved, err = engine.contextPassages(&project, request)
		if err != nil {
			return task, err
		}
		task.prompt, err = prepareAI(&project, request, int(task.selected.Config.ContextChars))
		if err == nil && request.Action == "agent" {
			if err = engine.refreshIntegrationsLocked(); err == nil {
				task.prompt, err = prepareAgentPrompt(task.prompt, engine.agentSearchPolicyLocked(request, task.selected.ProfileID), int(task.selected.Config.ContextChars))
			}
		}
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
	if previous.Status == "running" || previous.Status == "done" || previous.ChapterID != request.ChapterID || contains([]string{"wiki", "memory_update", "agent"}, request.Action) {
		return request, "", errors.New("Only interrupted prose in the same chapter can be continued")
	}
	if previous.Prompt.SourceID != "" {
		text, _, ok := project.SourceText(previous.Prompt.SourceID)
		if !ok || hashText(text) != previous.Prompt.SourceHash {
			return request, "", errors.New("The source changed after interruption. Review the new version before continuing")
		}
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
	source, _, ok := project.SourceText(id)
	if !ok {
		return Prompt{}, errors.New("Select a chapter or source to analyze")
	}
	if strings.TrimSpace(source) == "" {
		return Prompt{}, errors.New("There is no text to analyze")
	}
	return Prompt{Messages: []Message{}, Context: []ContextItem{}, SourceID: id, SourceHash: hashText(source), Chars: charCount(source)}, nil
}
