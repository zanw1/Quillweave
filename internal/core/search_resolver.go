package core

import (
	"errors"
	websearch "quillweave/internal/search"
)

// resolveSearchPlan freezes the effective public configuration from the caller's database transaction.
func resolveSearchPlan(db documentDB, input searchStart) (searchPlan, ModelSettings, error) {
	settings, revision, err := readSearchSettings(db)
	if err != nil {
		return searchPlan{}, ModelSettings{}, err
	}
	if !settings.Enabled {
		return searchPlan{}, ModelSettings{}, websearch.Fail("SEARCH_DISABLED", "Enable web search before searching or testing a connection", 403)
	}
	models, err := readModelSettings(db)
	if err != nil {
		return searchPlan{}, models, err
	}
	taskModelID := input.ModelProfileID
	if input.ProjectID != "" {
		project, err := readProject(db, input.ProjectID)
		if err != nil {
			return searchPlan{}, models, err
		}
		if input.SessionID != "" {
			if _, err := readConversation(db, input.ProjectID, input.SessionID); err != nil {
				return searchPlan{}, models, err
			}
		}
		if taskModelID == "" {
			taskModelID = project.ModelOverrides["ask"]
		}
	}
	if taskModelID == "" {
		taskModelID = models.Routes["ask"]
	}
	if taskModelID == "" {
		taskModelID = models.DefaultProfile
	}
	if taskModelID != "" {
		if _, err := models.profile(taskModelID); err != nil {
			return searchPlan{}, models, websearch.Fail("SEARCH_PROFILE_UNAVAILABLE", "The task model connection is unavailable", 422)
		}
	}
	choice := input.Selection
	if choice.Mode == "" {
		choice.Mode = "inherit"
	}
	if err := websearch.ValidateSelection(choice); err != nil {
		return searchPlan{}, models, err
	}
	source := "request"
	if choice.Mode == "inherit" {
		choice = settings.Bindings[taskModelID]
		source = "model"
		if choice.Mode == "" || choice.Mode == "inherit" {
			choice = websearch.Selection{Mode: "profile", ProfileID: settings.DefaultProfileID}
			source = "default"
		}
	}
	if choice.Mode == "off" {
		return searchPlan{}, models, websearch.Fail("SEARCH_DISABLED", "This task or model explicitly disables web search", 403)
	}
	profile, err := settings.Find(choice.ProfileID)
	if err != nil {
		return searchPlan{}, models, err
	}
	if !profile.Enabled && input.Purpose != "test" {
		return searchPlan{}, models, websearch.Fail("SEARCH_PROFILE_UNAVAILABLE", "The selected search profile is disabled", 422)
	}
	if err := profile.Validate(); err != nil {
		return searchPlan{}, models, err
	}
	if profile.Limits.StrictBudget && !profile.HardLimitKnown() && input.Operation != "fetch" {
		return searchPlan{}, models, websearch.Fail("SEARCH_CAPABILITY_UNSUPPORTED", "This route cannot guarantee the search limit; choose standalone search or a pinned engine", 422)
	}
	plan := searchPlan{Profile: profile, Fallbacks: []websearch.Profile{}, Policy: "none", Source: source, SettingsRevision: revision, ModelRevision: models.Revision, TaskModelProfileID: taskModelID, Models: map[string]websearch.Model{}}
	if input.Purpose != "test" && input.Selection.Mode != "profile" && settings.FallbackPolicy != "none" {
		plan.Policy = settings.FallbackPolicy
		for _, id := range settings.FallbackProfileIDs {
			if id == profile.ID {
				continue
			}
			fallback, err := settings.Find(id)
			if err != nil {
				return plan, models, err
			}
			if fallback.Enabled {
				plan.Fallbacks = append(plan.Fallbacks, fallback)
			}
		}
	}
	for _, candidate := range append([]websearch.Profile{profile}, plan.Fallbacks...) {
		if candidate.ModelProfileID == "" {
			continue
		}
		model, err := models.profile(candidate.ModelProfileID)
		if err != nil {
			return plan, models, websearch.Fail("SEARCH_PROFILE_UNAVAILABLE", "The linked research model is missing", 422)
		}
		family := websearch.ModelFamily(model.Config.ProviderID, model.Config.BaseURL)
		if family != "" && family != candidate.Family {
			return plan, models, websearch.Fail("SEARCH_CAPABILITY_UNSUPPORTED", "Research model and search service must belong to the same provider", 422)
		}
		plan.Models[candidate.ModelProfileID] = websearch.Model{ID: model.ID, Name: model.Name, Family: candidate.Family, Protocol: model.Config.Protocol, BaseURL: model.Config.BaseURL, NameID: model.Config.Model, Enabled: model.Enabled, MaxTokens: int(model.Config.MaxTokens)}
	}
	plan.Model = plan.Models[profile.ModelProfileID]
	plan.ModelProfileID = profile.ModelProfileID
	return plan, models, nil
}

// searchExecution resolves a matching live credential immediately before an already reserved network attempt.
func (engine *Engine) searchExecution(profile websearch.Profile, run searchRun, remaining int) (websearch.ExecuteRequest, uint64, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed || engine.ctx.Err() != nil {
		return websearch.ExecuteRequest{}, 0, websearch.Fail("SEARCH_CANCELLED", "The application is closing", 409)
	}
	view, err := engine.searchViewLocked()
	if err != nil {
		return websearch.ExecuteRequest{}, 0, err
	}
	if !view.Settings.Enabled {
		return websearch.ExecuteRequest{}, 0, websearch.Fail("SEARCH_DISABLED", "Web search was disabled", 403)
	}
	current, err := view.Settings.Find(profile.ID)
	if err != nil {
		return websearch.ExecuteRequest{}, 0, err
	}
	if current.Identity() != profile.Identity() || (!current.Enabled && run.Purpose != "test") {
		return websearch.ExecuteRequest{}, 0, websearch.Fail("SEARCH_PROFILE_UNAVAILABLE", "The search connection was changed or disabled", 422)
	}
	key, generation, err := engine.searchCredentialLocked(profile, engine.models)
	if err != nil {
		return websearch.ExecuteRequest{}, generation, err
	}
	if searchRequiresKey(profile) && key == "" {
		return websearch.ExecuteRequest{}, generation, websearch.Fail("SEARCH_CREDENTIAL_REQUIRED", "Enter the search API key for this process before running", 422)
	}
	model := run.Plan.Models[profile.ModelProfileID]
	if profile.ModelProfileID != "" {
		live, err := engine.models.profile(profile.ModelProfileID)
		if err != nil || !live.Enabled {
			return websearch.ExecuteRequest{}, generation, errors.New("The research model was removed or disabled")
		}
	}
	request := websearch.ExecuteRequest{Profile: profile, Key: key, Model: model, Operation: run.Operation, Query: run.Query, URL: run.URL, Language: run.Language, Remaining: remaining}
	return request, generation, websearch.ValidateExecution(request)
}

// searchRequiresKey preserves explicitly configured anonymous legacy gateways without weakening new service profiles.
func searchRequiresKey(profile websearch.Profile) bool {
	capability, _ := websearch.CapabilityFor(profile.Family)
	return capability.RequiresKey && !(profile.LegacyID != "" && contains([]string{"tavily", "bocha", "exa", "serper", "searxng", "wikipedia"}, profile.Family))
}
