package core

import (
	"context"
	"encoding/json"
	"errors"
	websearch "quillweave/internal/search"
)

// researchForJob gives an Agent child one durable research run and shares the parent's remaining allowances.
func (engine *Engine) researchForJob(job Job, query string, searchesLeft, modelsLeft int) (ResearchResponse, int, error) {
	response := ResearchResponse{Results: []ResearchResult{}, Warnings: []string{}}
	if searchesLeft < 1 {
		return response, 0, websearch.Fail("SEARCH_BUDGET_EXHAUSTED", "The parent task's search budget is exhausted", 409)
	}
	selection := websearch.Selection{Mode: "inherit"}
	if job.SearchSelection != nil {
		selection = *job.SearchSelection
	}
	input := searchStart{RequestID: newID(), ProjectID: job.ProjectID, SessionID: job.SessionID, ModelProfileID: job.SearchModelProfileID, Purpose: "research", Operation: "search", Query: query, Selection: selection, UILanguage: job.Language, ParentJobID: job.ID, SearchAllowance: searchesLeft, ModelAllowance: modelsLeft}
	run, err := engine.startSearchRun(input)
	if err != nil {
		return response, 0, err
	}
	engine.searchRuntime.mu.Lock()
	task := engine.searchRuntime.tasks[run.ID]
	engine.searchRuntime.mu.Unlock()
	if task != nil {
		select {
		case <-task.done:
		case <-job.ctx.Done():
			_, _ = engine.cancelSearchRun(run.ID, job.ProjectID)
			return response, 0, job.ctx.Err()
		}
	}
	done, err := engine.getSearchRun(run.ID, job.ProjectID)
	if err != nil {
		return response, 0, err
	}
	response.Results = done.Response.Results
	response.Warnings = done.Response.Warnings
	response.Attempts = done.ReservedSearches
	response.RunID = done.ID
	if done.Plan.Profile.Name != "" {
		response.Provider = done.Plan.Profile.Name
	}
	if len(done.Attempts) > 0 {
		response.Provider = done.Attempts[len(done.Attempts)-1].Provider
	}
	response.Answer = done.Response.Answer
	response.SearchState = done.Response.SearchState
	if done.Error != nil {
		return response, done.NativeModelCalls, done.Error
	}
	return response, done.NativeModelCalls, nil
}

// legacySearchCommand preserves old result fields while delegating every actual request to the new service.
func (engine *Engine) legacySearchCommand(command string, args *arguments) (ResearchResponse, error) {
	response := ResearchResponse{Results: []ResearchResult{}, Warnings: []string{}}
	profileID := ""
	purpose := "research"
	projectID := args.optionalString("project_id")
	if command == "test_search" {
		purpose = "test"
		projectID = ""
		view, err := engine.currentSearchView()
		if err != nil {
			return response, err
		}
		for _, profile := range view.Settings.Profiles {
			if profile.LegacyID == args.optionalString("provider_id") {
				profileID = profile.ID
				break
			}
		}
		if profileID == "" {
			return response, websearch.Fail("SEARCH_PROFILE_UNAVAILABLE", "The search provider is unavailable", 422)
		}
	} else if projectID == "" {
		purpose = "test"
	}
	selection := websearch.Selection{Mode: "inherit"}
	if profileID != "" {
		selection = websearch.Selection{Mode: "profile", ProfileID: profileID}
	}
	run, err := engine.startSearchRun(searchStart{RequestID: newID(), ProjectID: projectID, Purpose: purpose, Operation: "search", Query: args.text("query"), Selection: selection, UILanguage: args.optionalString("ui_language")})
	if err != nil {
		return response, err
	}
	engine.searchRuntime.mu.Lock()
	task := engine.searchRuntime.tasks[run.ID]
	engine.searchRuntime.mu.Unlock()
	if task != nil {
		select {
		case <-task.done:
		case <-engine.ctx.Done():
			return response, context.Canceled
		}
	}
	run, err = engine.getSearchRun(run.ID, projectID)
	if err != nil {
		return response, err
	}
	response.Results = run.Response.Results
	response.Warnings = run.Response.Warnings
	response.Attempts = run.ReservedSearches
	response.RunID = run.ID
	response.Provider = run.Plan.Profile.Name
	response.Answer = run.Response.Answer
	response.SearchState = run.Response.SearchState
	if len(run.Attempts) > 0 {
		response.Provider = run.Attempts[len(run.Attempts)-1].Provider
	}
	if run.Error != nil {
		return response, run.Error
	}
	return response, nil
}

// decodeSearchSelection validates an optional per-request permission without giving it authority over the global switch.
func decodeSearchSelection(raw json.RawMessage) (*SearchSelection, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var selection SearchSelection
	if err := decodeJSON(raw, &selection); err != nil {
		return nil, errors.New("Invalid search selection")
	}
	if err := websearch.ValidateSelection(selection); err != nil {
		return nil, err
	}
	return &selection, nil
}

// agentSearchLimit freezes one parent allowance; child searches do not each receive a fresh configuration limit.
func (engine *Engine) agentSearchLimit(job Job, fallback int) int {
	selection := websearch.Selection{Mode: "inherit"}
	if job.SearchSelection != nil {
		selection = *job.SearchSelection
	}
	limit := max(1, min(2, fallback))
	_ = engine.store.withReadSnapshot(func(db documentDB) error {
		plan, _, err := resolveSearchPlan(db, searchStart{ProjectID: job.ProjectID, ModelProfileID: job.SearchModelProfileID, Selection: selection, Purpose: "research", Operation: "search"})
		if err == nil {
			limit = plan.Profile.Limits.MaxSearches
		}
		return nil
	})
	return limit
}

// agentSearchPolicyLocked reports the resolved task permission before prompt budgeting; Engine.mu is held by preparation.
func (engine *Engine) agentSearchPolicyLocked(request AIRequest, modelID string) IntegrationSettings {
	settings := engine.integrations
	settings.SearchEnabled, settings.MaxSearches = false, 0
	selection := websearch.Selection{Mode: "inherit"}
	if request.SearchSelection != nil {
		selection = *request.SearchSelection
	}
	_ = engine.store.withReadSnapshot(func(db documentDB) error {
		plan, _, err := resolveSearchPlan(db, searchStart{ProjectID: request.ProjectID, SessionID: request.SessionID, ModelProfileID: modelID, Selection: selection, Purpose: "research", Operation: "search"})
		if err == nil {
			settings.SearchEnabled, settings.MaxSearches = true, plan.Profile.Limits.MaxSearches
		}
		return nil
	})
	return settings
}
