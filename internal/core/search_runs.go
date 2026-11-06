package core

import (
	"context"
	"database/sql"
	"errors"
	websearch "quillweave/internal/search"
	"strings"
	"time"
)

// startSearchRun accepts a request exactly once before registering any paid network work.
func (engine *Engine) startSearchRun(input searchStart) (searchRun, error) {
	if input.RequestID == "" || len(input.RequestID) > 120 || !contains([]string{"test", "research"}, input.Purpose) || !contains([]string{"search", "fetch"}, input.Operation) {
		return searchRun{}, websearch.Fail("SEARCH_INVALID_ARGUMENT", "A valid request ID, purpose and operation are required", 400)
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Selection.Mode == "" {
		input.Selection.Mode = "inherit"
	}
	if input.Operation == "search" && (input.Query == "" || charCount(input.Query) > 500) {
		return searchRun{}, websearch.Fail("SEARCH_INVALID_ARGUMENT", "Search query must contain 1–500 characters", 400)
	}
	if input.Operation == "fetch" && !websearch.ValidURL(input.URL, false) {
		return searchRun{}, websearch.Fail("SEARCH_INVALID_ARGUMENT", "Enter a valid public page URL", 400)
	}
	if input.Purpose == "research" && input.ProjectID == "" && input.ParentJobID == "" {
		return searchRun{}, websearch.Fail("SEARCH_INVALID_ARGUMENT", "Research must be scoped to a project", 400)
	}
	intentHash := hashText(jsonText(struct {
		Project, Request, Purpose, Operation, Query, URL, Model, Session, Parent, Language string
		Selection                                                                          websearch.Selection
	}{input.ProjectID, input.RequestID, input.Purpose, input.Operation, input.Query, input.URL, input.ModelProfileID, input.SessionID, input.ParentJobID, input.UILanguage, input.Selection}))
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return searchRun{}, errors.New("The application is closing")
	}
	if err := engine.reconcileJobsLocked(); err != nil {
		return searchRun{}, err
	}
	var run searchRun
	created := false
	err := engine.store.withWriteTransaction(func(db *sql.Conn) error {
		existing, found, err := findSearchRequest(db, searchScope(input.ProjectID), input.RequestID)
		if err != nil {
			return err
		}
		if found {
			if existing.Hash != intentHash {
				return websearch.Fail("SEARCH_IDEMPOTENCY_CONFLICT", "This request ID was already used for different search input", 409)
			}
			run = existing
			return nil
		}
		plan, models, err := resolveSearchPlan(db, input)
		if err != nil {
			return err
		}
		var active int
		if err := db.QueryRowContext(background(), "SELECT COUNT(*) FROM search_runs WHERE status IN ('queued','running')").Scan(&active); err != nil {
			return err
		}
		if active >= 4 {
			return websearch.Fail("SEARCH_BUSY", "Four searches are already running. Wait or cancel one", 409)
		}
		if input.SearchAllowance > 0 {
			plan.Profile.Limits.MaxSearches = min(plan.Profile.Limits.MaxSearches, input.SearchAllowance)
		}
		plan.ModelAllowance = input.ModelAllowance
		if plan.Profile.Native() && input.ParentJobID != "" && input.ModelAllowance < 1 {
			return websearch.Fail("SEARCH_BUDGET_EXHAUSTED", "The parent task's model call budget is exhausted", 409)
		}
		for i := range models.Profiles {
			models.Profiles[i].Config.APIKey = engine.models.keyFor(models.Profiles[i])
		}
		key, generation, err := engine.searchCredentialLocked(plan.Profile, models)
		if err != nil {
			return err
		}
		if searchRequiresKey(plan.Profile) && key == "" {
			return websearch.Fail("SEARCH_CREDENTIAL_REQUIRED", "Enter the API key for this search service before running", 422)
		}
		if plan.Profile.Native() && (plan.Model.NameID == "" || !plan.Model.Enabled) {
			return websearch.Fail("SEARCH_PROFILE_UNAVAILABLE", "Choose an enabled research model connection", 422)
		}
		if err := websearch.ValidateExecution(websearch.ExecuteRequest{Profile: plan.Profile, Model: plan.Model, Operation: input.Operation, Query: input.Query, URL: input.URL}); err != nil {
			return err
		}
		run = searchRun{ID: newID(), ProjectID: input.ProjectID, RequestID: input.RequestID, Purpose: input.Purpose, Operation: input.Operation, Status: "queued", Query: input.Query, URL: input.URL, Language: input.UILanguage, Plan: plan, Response: websearch.EmptyResponse(), Attempts: []searchAttempt{}, CreatedAt: now(), Owner: engine.owner.id, Hash: intentHash, ParentJobID: input.ParentJobID, CredentialGeneration: generation}
		var project any
		if run.ProjectID != "" {
			project = run.ProjectID
		}
		_, err = db.ExecContext(background(), "INSERT INTO search_runs(id,revision,project_id,request_scope,request_id,payload_hash,profile_id,purpose,operation,status,execution_owner,document,created_at) VALUES(?,0,?,?,?,?,?,?,?,?,?,?,?)", run.ID, project, searchScope(run.ProjectID), input.RequestID, intentHash, plan.Profile.ID, run.Purpose, run.Operation, run.Status, run.Owner, jsonText(run), run.CreatedAt)
		created = err == nil
		return err
	})
	if err != nil {
		return searchRun{}, err
	}
	if !created {
		return run, nil
	}
	ctx, cancel := context.WithCancel(engine.ctx)
	task := &searchTask{cancel: cancel, done: make(chan struct{}), profiles: map[string]bool{}}
	for _, profile := range append([]websearch.Profile{run.Plan.Profile}, run.Plan.Fallbacks...) {
		task.profiles[profile.ID] = true
	}
	engine.searchRuntime.mu.Lock()
	if engine.searchRuntime.tasks == nil {
		engine.searchRuntime.tasks = map[string]*searchTask{}
	}
	engine.searchRuntime.tasks[run.ID] = task
	engine.searchRuntime.mu.Unlock()
	engine.workers.Add(1)
	go engine.workSearch(ctx, run, task)
	return run, nil
}

// cancelSearchProfiles stops this process's affected work; peers observe persisted policy before reserving again.
func (engine *Engine) cancelSearchProfiles(profiles map[string]bool) {
	engine.searchRuntime.mu.Lock()
	defer engine.searchRuntime.mu.Unlock()
	for _, task := range engine.searchRuntime.tasks {
		if profiles == nil {
			task.cancel()
			continue
		}
		for id := range profiles {
			if task.profiles[id] {
				task.cancel()
				break
			}
		}
	}
}

// reserveSearch atomically checks authority, status and a shared budget before a provider request is accepted.
func (engine *Engine) reserveSearch(id string, profile websearch.Profile) (searchRun, int, error) {
	remaining := 0
	var reserved searchRun
	err := engine.store.withWriteTransaction(func(db *sql.Conn) error {
		var err error
		reserved, err = readSearchRun(db, id)
		if err != nil {
			return err
		}
		if terminalSearch(reserved.Status) {
			return websearch.Fail("SEARCH_CANCELLED", "The search already stopped", 409)
		}
		settings, _, err := readSearchSettings(db)
		if err != nil {
			return err
		}
		if !settings.Enabled {
			return websearch.Fail("SEARCH_DISABLED", "Web search was disabled", 403)
		}
		current, err := settings.Find(profile.ID)
		if err != nil {
			return err
		}
		if current.Identity() != profile.Identity() || (!current.Enabled && reserved.Purpose != "test") {
			return websearch.Fail("SEARCH_PROFILE_UNAVAILABLE", "The search profile was changed or disabled", 422)
		}
		if reserved.Operation == "fetch" {
			remaining = reserved.Plan.Profile.Limits.MaxFetches - reserved.ReservedFetches
		} else {
			remaining = reserved.Plan.Profile.Limits.MaxSearches - reserved.ReservedSearches
		}
		if remaining < 1 {
			return websearch.Fail("SEARCH_BUDGET_EXHAUSTED", "The shared search budget is exhausted", 409)
		}
		cost := 1
		if profile.Native() && !(reserved.Operation == "fetch" && profile.Family != "openrouter") {
			if reserved.ParentJobID != "" && reserved.NativeModelCalls >= reserved.Plan.ModelAllowance {
				return websearch.Fail("SEARCH_BUDGET_EXHAUSTED", "The parent task's model call budget is exhausted", 409)
			}
			cost = remaining
			reserved.NativeModelCalls++
		}
		if reserved.Operation == "fetch" {
			reserved.ReservedFetches += cost
		} else {
			reserved.ReservedSearches += cost
		}
		reserved.Response.Usage.AttemptsReserved = reserved.ReservedSearches + reserved.ReservedFetches
		reserved.Status = "running"
		reserved.Attempts = append(reserved.Attempts, searchAttempt{ProfileID: profile.ID, Provider: profile.Name, Status: "running"})
		return updateSearchRun(db, &reserved, reserved.Revision)
	})
	return reserved, remaining, err
}

// workSearch executes the frozen authorized chain and keeps retries within one persistent budget.
func (engine *Engine) workSearch(ctx context.Context, initial searchRun, task *searchTask) {
	defer engine.workers.Done()
	defer task.cancel()
	defer close(task.done)
	defer func() {
		engine.searchRuntime.mu.Lock()
		delete(engine.searchRuntime.tasks, initial.ID)
		engine.searchRuntime.mu.Unlock()
	}()
	profiles := append([]websearch.Profile{initial.Plan.Profile}, initial.Plan.Fallbacks...)
	final := initial
	for index, profile := range profiles {
		if ctx.Err() != nil {
			final.Status = "cancelled"
			final.Error = websearch.Fail("SEARCH_CANCELLED", "Search was cancelled; an accepted supplier request may still be billed", 409)
			break
		}
		reserved, remaining, err := engine.reserveSearch(initial.ID, profile)
		if err != nil {
			final.Error = searchFault(err)
			final.Status = "failed"
			if final.Error.Code == "SEARCH_DISABLED" || final.Error.Code == "SEARCH_CANCELLED" {
				final.Status = "cancelled"
			}
			break
		}
		final = reserved
		request, generation, err := engine.searchExecution(profile, final, remaining)
		if index == 0 && err == nil && generation != initial.CredentialGeneration {
			err = websearch.Fail("SEARCH_CREDENTIAL_CHANGED", "The search credential changed before execution", 409)
		}
		response := websearch.EmptyResponse()
		if err == nil {
			if ctx.Err() != nil {
				err = websearch.Fail("SEARCH_CANCELLED", "Search was cancelled", 409)
			} else {
				if profile.Native() && !(final.Operation == "fetch" && profile.Family != "openrouter") && final.ParentJobID != "" {
					engine.noteModelCall(final.ParentJobID)
				}
				response, err = executeConfiguredSearch(ctx, request)
			}
		}
		if ctx.Err() != nil {
			err = websearch.Fail("SEARCH_CANCELLED", "Search was cancelled; accepted supplier work may still be billed", 409)
		}
		attempt := &final.Attempts[len(final.Attempts)-1]
		if err != nil {
			if response.SearchState != "unknown" || response.Usage.ModelInputTokens != nil || response.Usage.ReportedCost != nil {
				response.Usage.AttemptsReserved = final.ReservedSearches + final.ReservedFetches
				response.Warnings = append(final.Response.Warnings, response.Warnings...)
				final.Response = response
			}
			attempt.Status = "failed"
			attempt.Error = searchFault(err)
			final.Error = attempt.Error
			final.Response.Warnings = append(final.Response.Warnings, profile.Name+": "+attempt.Error.Message)
		} else {
			attempt.Status = "succeeded"
			response.Usage.AttemptsReserved = final.ReservedSearches + final.ReservedFetches
			response.Warnings = append(final.Response.Warnings, response.Warnings...)
			final.Response = response
			final.Error = nil
			if len(response.Results) > 0 || response.Answer != nil {
				final.Status = "succeeded"
				break
			}
			attempt.Status = "empty"
			final.Status = "empty"
		}
		if err != nil && (searchFault(err).Code == "SEARCH_CANCELLED" || searchFault(err).Code == "SEARCH_DISABLED" || searchFault(err).Code == "SEARCH_CREDENTIAL_CHANGED") {
			final.Status = "cancelled"
			break
		}
		if index == len(profiles)-1 || !retrySearch(final.Plan.Policy, err) {
			if err != nil {
				final.Status = "failed"
			}
			break
		}
		if _, err := engine.store.changeSearchRun(final.ID, func(run *searchRun) error { run.Attempts = final.Attempts; run.Response = final.Response; return nil }); err != nil {
			final.Status = "failed"
			final.Error = searchFault(err)
			break
		}
	}
	if !terminalSearch(final.Status) {
		final.Status = "failed"
		if final.Error == nil {
			final.Error = websearch.Fail("SEARCH_PROVIDER_ERROR", "No search result was produced", 502)
		}
	}
	final.FinishedAt = now()
	engine.finishSearchWithRetry(ctx, &final)
}

// executeConfiguredSearch reuses the legacy compatibility adapter path while sharing the new lifecycle and budget.
func executeConfiguredSearch(ctx context.Context, request websearch.ExecuteRequest) (websearch.Response, error) {
	if request.Operation != "search" || !contains([]string{"tavily", "bocha", "exa", "serper", "searxng", "wikipedia"}, request.Profile.Family) {
		return websearch.Execute(ctx, request)
	}
	settings := IntegrationSettings{SearchEnabled: true, MaxSearches: 1, Providers: []SearchProvider{{ID: request.Profile.ID, Name: request.Profile.Name, Kind: request.Profile.Family, Endpoint: request.Profile.Endpoint, Enabled: true, APIKey: request.Key, profile: &request.Profile}}}
	legacy, err := runResearch(ctx, settings, request.Query, request.Language, "")
	if err == nil {
		err = legacy.failure
	}
	response := websearch.EmptyResponse()
	response.Results = legacy.Results
	response.Warnings = legacy.Warnings
	response.Engine = request.Profile.Family
	if err == nil {
		response.SearchState = "executed"
		response.TestVerdict = "passed"
		if len(response.Results) == 0 {
			response.TestVerdict = "connected_empty"
		}
		response.Usage.SearchCalls = intPointer(1)
		response.Usage.FetchCalls = intPointer(0)
	}
	return response, err
}

// intPointer keeps known local usage distinct from supplier fields that were not reported.
func intPointer(value int) *int { return &value }

// retrySearch excludes credential/configuration errors from newly configured automatic fallback policies.
func retrySearch(policy string, err error) bool {
	if policy == "none" {
		return false
	}
	if policy == "legacy" {
		return true
	}
	if err == nil {
		return true
	}
	fault := searchFault(err)
	return contains([]string{"SEARCH_NETWORK_ERROR", "SEARCH_TIMEOUT", "SEARCH_RATE_LIMITED", "SEARCH_RESPONSE_ERROR"}, fault.Code) || (fault.Code == "SEARCH_PROVIDER_ERROR" && fault.Details != nil && intField(fault.Details["upstream_status"]) >= 500)
}

// searchFault classifies unexpected local errors without returning raw database paths or secret data.
func searchFault(err error) *websearch.Fault {
	var fault *websearch.Fault
	if errors.As(err, &fault) {
		return fault
	}
	return websearch.Fail("SEARCH_STORAGE_ERROR", "The search state could not be saved or read; retry after storage recovers", 500)
}

// intField reads a status code from local structured metadata.
func intField(value any) int {
	if pointer := websearch.Number(value); pointer != nil {
		return *pointer
	}
	return 0
}

// finishSearchWithRetry preserves a terminal intent before retrying a temporarily unavailable database.
func (engine *Engine) finishSearchWithRetry(ctx context.Context, final *searchRun) {
	_ = engine.saveSearchIntent(*final)
	for {
		stored, err := engine.store.changeSearchRun(final.ID, func(run *searchRun) error {
			revision := run.Revision
			owner, hash := run.Owner, run.Hash
			*run = *final
			run.Revision = revision
			run.Owner = owner
			run.Hash = hash
			return nil
		})
		if err == nil {
			engine.removeSearchIntent(final.ID)
			_ = stored
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}
