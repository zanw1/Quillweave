package core

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	websearch "quillweave/internal/search"
)

type CommandEnvelope struct {
	OK        bool           `json:"ok"`
	Result    any            `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	ErrorCode string         `json:"error_code,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// Envelope keeps native and browser search errors structurally identical without changing legacy Dispatch.
func Envelope(value any, err error) CommandEnvelope {
	if err == nil {
		return CommandEnvelope{OK: true, Result: value}
	}
	var fault *websearch.Fault
	if errors.As(err, &fault) {
		return CommandEnvelope{Error: fault.Message, ErrorCode: fault.Code, Details: fault.Details}
	}
	return CommandEnvelope{Error: err.Error()}
}

// IsSearchCommand recognizes only the new bounded command surface.
func IsSearchCommand(command string) bool {
	return contains([]string{"search_capabilities", "page_search_profiles", "get_search_settings", "get_search_profile", "mutate_search_settings", "start_search_run", "get_search_run", "cancel_search_run", "disable_search", "adopt_search_result"}, command)
}

// dispatchSearch routes settings and durable research through the same Engine business boundary.
func (engine *Engine) dispatchSearch(command string, raw json.RawMessage, args *arguments) (any, error) {
	switch command {
	case "search_capabilities":
		presets := []websearch.Profile{}
		for _, capability := range websearch.Catalog() {
			presets = append(presets, websearch.DefaultProfile(capability.Family))
		}
		return map[string]any{"version": websearch.CapabilityVersion, "services": websearch.Catalog(), "presets": presets}, nil
	case "get_search_settings":
		return engine.currentSearchView()
	case "page_search_profiles":
		page, size := 1, 10
		if value, exists := args.fields["pageNo"]; exists {
			number, ok := unsigned(value)
			if !ok || number > 1_000_000 {
				return nil, websearch.Fail("SEARCH_INVALID_ARGUMENT", "Invalid page number", 400)
			}
			page = int(number)
		}
		if value, exists := args.fields["pageSize"]; exists {
			number, ok := unsigned(value)
			if !ok || number > 50 {
				return nil, websearch.Fail("SEARCH_INVALID_ARGUMENT", "Invalid page size", 400)
			}
			size = int(number)
		}
		return engine.searchProfilePage(page, size, args.optionalString("query"), args.optionalString("family"))
	case "get_search_profile":
		return engine.searchProfileInfo(args.text("profile_id"))
	case "mutate_search_settings":
		var mutation searchMutation
		if err := decodeJSON(raw, &mutation); err != nil {
			return nil, websearch.Fail("SEARCH_INVALID_ARGUMENT", "Invalid search settings request", 400)
		}
		return engine.mutateSearchSettings(mutation)
	case "start_search_run":
		if _, exists := args.fields["job_id"]; exists {
			return nil, websearch.Fail("SEARCH_INVALID_ARGUMENT", "Parent search jobs are linked by the server", 400)
		}
		var input searchStart
		if err := decodeJSON(raw, &input); err != nil {
			return nil, websearch.Fail("SEARCH_INVALID_ARGUMENT", "Invalid search run request", 400)
		}
		return engine.startSearchRun(input)
	case "get_search_run":
		if id := args.optionalString("request_id"); id != "" {
			run, found, err := findSearchRequest(engine.store.db, searchScope(args.optionalString("project_id")), id)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, websearch.Fail("SEARCH_RUN_NOT_FOUND", "Search run was not found", 404)
			}
			return engine.getSearchRun(run.ID, run.ProjectID)
		}
		return engine.getSearchRun(args.text("run_id"), args.optionalString("project_id"))
	case "cancel_search_run":
		return engine.cancelSearchRun(args.text("run_id"), args.optionalString("project_id"))
	case "disable_search":
		return engine.disableSearch()
	case "adopt_search_result":
		revision, ok := unsigned(args.fields["revision"])
		if !ok {
			return nil, websearch.Fail("SEARCH_INVALID_ARGUMENT", "A project revision is required", 400)
		}
		return engine.adoptSearchResult(args.text("project_id"), revision, args.text("run_id"), args.text("result_id"))
	}
	return nil, errors.New("Unknown search command")
}

// searchProfileInfo reports current credential/test validity separately from historical success.
func (engine *Engine) searchProfileInfo(id string) (any, error) {
	view, err := engine.currentSearchView()
	if err != nil {
		return nil, err
	}
	profile, err := view.Settings.Find(id)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"profile": profile, "revision": view.Revision, "key_configured": view.KeyStatus[id], "test_valid": false}
	var runID string
	err = engine.store.db.QueryRow("SELECT id FROM search_runs WHERE profile_id=? AND purpose='test' AND project_id IS NULL ORDER BY created_at DESC LIMIT 1", id).Scan(&runID)
	if err == nil {
		run, err := engine.getSearchRun(runID, "")
		if err != nil {
			return nil, err
		}
		result["last_test"] = run
		engine.mu.Lock()
		_, generation, keyErr := engine.searchCredentialLocked(profile, engine.models)
		engine.mu.Unlock()
		capability, _ := websearch.CapabilityFor(profile.Family)
		modelMatches := true
		if profile.Native() {
			modelMatches = false
			for _, model := range view.Models {
				if model.ID == profile.ModelProfileID {
					frozen := run.Plan.Model
					model.Family, frozen.Family = "", ""
					modelMatches = websearch.Canonical(model) == websearch.Canonical(frozen)
				}
			}
		}
		result["test_valid"] = modelMatches && keyErr == nil && (!capability.RequiresKey || view.KeyStatus[id]) && run.Owner == engine.owner.id && run.Plan.Profile.Fingerprint() == profile.Fingerprint() && run.CredentialGeneration == generation && run.Response.TestVerdict == "passed" && run.Status == "succeeded"
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return result, nil
}

// adoptSearchResult takes source text from the recorded result, not from an untrusted client replacement.
func (engine *Engine) adoptSearchResult(projectID string, revision uint64, runID, resultID string) (Project, error) {
	run, err := engine.getSearchRun(runID, projectID)
	if err != nil {
		return Project{}, err
	}
	if run.Purpose != "research" || run.Status != "succeeded" || run.Response.SearchState != "executed" {
		return Project{}, websearch.Fail("SEARCH_SOURCE_UNAVAILABLE", "Only completed research with source evidence can be adopted", 422)
	}
	var result *websearch.Result
	for index := range run.Response.Results {
		if run.Response.Results[index].ID == resultID {
			result = &run.Response.Results[index]
			break
		}
	}
	if result == nil || result.Content == "" {
		return Project{}, websearch.Fail("SEARCH_SOURCE_UNAVAILABLE", "This source has no readable excerpt; fetch its original page first", 422)
	}
	project, err := engine.store.Get(projectID)
	if err != nil {
		return Project{}, err
	}
	for _, source := range project.Sources {
		if source.Provenance != nil && source.Provenance.RunID == runID && source.Provenance.ResultID == resultID {
			return project, nil
		}
	}
	if project.Revision != revision {
		return Project{}, errors.New("The project changed. Your edits are retained. Refresh and try again.")
	}
	if len(project.Sources) >= 20000 {
		return Project{}, websearch.Fail("SEARCH_INVALID_ARGUMENT", "The research source limit was reached", 400)
	}
	provenance := &SearchSourceProvenance{RunID: runID, ResultID: resultID, Query: run.Query, URL: result.URL, Provider: result.Provider, Engine: result.Engine, Reading: result.Reading, RetrievedAt: result.RetrievedAt, PublishedAt: result.PublishedAt, ContentHash: hashText(result.Content), Truncated: result.Truncated}
	content := fmt.Sprintf("[Research reference; reading=%s; review against the original before treating it as a fact]\n\n%s\n\nRetrieved: %s\nProvider: %s\nQuery: %s", result.Reading, result.Content, result.RetrievedAt, result.Provider, run.Query)
	if run.Language == "zh-CN" || run.Language == "zh" {
		content = fmt.Sprintf("[研究参考资料；阅读类型：%s；认定为事实前请核对原文]\n\n%s\n\n获取时间：%s\n搜索服务：%s\n实际查询：%s", result.Reading, result.Content, result.RetrievedAt, result.Provider, run.Query)
	}
	project.Sources = append(project.Sources, Source{ID: newID(), Title: result.Title, URL: result.URL, Category: "research", Content: content, CreatedAt: now(), Provenance: provenance})
	if err := engine.store.Save(&project, revision); err != nil {
		fresh, readErr := engine.store.Get(projectID)
		if readErr == nil {
			for _, source := range fresh.Sources {
				if source.Provenance != nil && source.Provenance.RunID == runID && source.Provenance.ResultID == resultID {
					return fresh, nil
				}
			}
		}
		return Project{}, err
	}
	return project, nil
}
