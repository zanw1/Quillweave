//go:build quillweave_whitebox

package core

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	websearch "quillweave/internal/search"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// saveSearchFixturePolicy mutates one current aggregate rather than bypassing CAS in lifecycle tests.
func saveSearchFixturePolicy(t *testing.T, engine *Engine, primary string, fallbacks []string) {
	t.Helper()
	view := invoke[searchSettingsView](t, engine, "get_search_settings", nil)
	invoke[searchSettingsView](t, engine, "mutate_search_settings", map[string]any{"operation": "set_policy", "expected_revision": view.Revision, "model_revision": view.ModelRevision, "policy": map[string]any{"enabled": true, "default_profile_id": primary, "fallback_policy": "ordered", "fallback_profile_ids": fallbacks}})
}

// TestSearchAgentPromptUsesResolvedPermission keeps the previewed protocol consistent with actual model/request permission and budget.
func TestSearchAgentPromptUsesResolvedPermission(t *testing.T) {
	engine := testEngine(t)
	invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": testSettings()})
	projectID := insertSearchProject(t, engine)
	profile := configureSearchFixture(t, engine, "deepseek", "https://search-fixture.example")
	view := invoke[searchSettingsView](t, engine, "get_search_settings", nil)
	profile.Limits.MaxSearches = 4
	view = invoke[searchSettingsView](t, engine, "mutate_search_settings", searchMutation{Operation: "upsert_profile", ExpectedRevision: view.Revision, ModelRevision: view.ModelRevision, Profile: profile})
	invoke[searchSettingsView](t, engine, "mutate_search_settings", searchMutation{Operation: "set_binding", ExpectedRevision: view.Revision, ModelRevision: view.ModelRevision, ModelProfileID: "writer", Binding: websearch.Selection{Mode: "off"}})
	for _, c := range []struct {
		selection websearch.Selection
		enabled   string
		budget    string
	}{{websearch.Selection{Mode: "inherit"}, "false", "0"}, {websearch.Selection{Mode: "profile", ProfileID: profile.ID}, "true", "4"}, {websearch.Selection{Mode: "off"}, "false", "0"}} {
		prompt := invoke[Prompt](t, engine, "prepare_ai", AIRequest{ProjectID: projectID, Action: "agent", History: []Message{}, Instruction: "Discuss the creative idea", SearchSelection: &c.selection})
		text := prompt.Messages[0].Content
		assert(t, strings.Contains(text, "Web research enabled: "+c.enabled) && strings.Contains(text, "and "+c.budget+" web queries"), "Agent prompt differs from resolved search permission")
	}
}

// TestSearchSharedFallbackBudget proves provider switches cannot replenish attempts or retry authentication failures.
func TestSearchSharedFallbackBudget(t *testing.T) {
	for _, scenario := range []struct {
		limit, status, wantCalls int
		want                     string
	}{{1, 503, 1, "failed"}, {2, 503, 2, "succeeded"}, {2, 401, 1, "failed"}} {
		t.Run(fmt.Sprintf("limit%d_status%d", scenario.limit, scenario.status), func(t *testing.T) {
			engine := testEngine(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_ = readBodyForStudioTest(t, r)
				if r.URL.Path == "/primary/v1/tools/search" {
					w.WriteHeader(scenario.status)
					fmt.Fprint(w, `{"error":{"message":"fixture"}}`)
					return
				}
				fmt.Fprint(w, `{"search_results":[{"url":"https://example.org/a","snippet":"fallback"}]}`)
			}))
			defer server.Close()
			primary := configureSearchFixture(t, engine, "deepseek", server.URL+"/primary")
			fallback := configureSearchFixture(t, engine, "kimi", server.URL+"/fallback")
			view := invoke[searchSettingsView](t, engine, "get_search_settings", nil)
			primary.Limits.MaxSearches = scenario.limit
			invoke[searchSettingsView](t, engine, "mutate_search_settings", searchMutation{Operation: "upsert_profile", ExpectedRevision: view.Revision, ModelRevision: view.ModelRevision, Profile: primary})
			saveSearchFixturePolicy(t, engine, primary.ID, []string{fallback.ID})
			run := invoke[searchRun](t, engine, "start_search_run", searchStart{RequestID: newID(), Purpose: "research", ProjectID: insertSearchProject(t, engine), Operation: "search", Query: "history"})
			done := awaitSearchFixture(t, engine, run)
			assert(t, done.Status == scenario.want && int(calls.Load()) == scenario.wantCalls && done.ReservedSearches <= scenario.limit, jsonText(done))
		})
	}
}

// insertSearchProject gives a research request a real author-owned scope.
func insertSearchProject(t *testing.T, engine *Engine) string {
	t.Helper()
	project := testProject()
	insertProject(t, engine, project)
	return project.ID
}

// TestSearchReferencedCredentialInvalidation covers matching endpoints, same-value replacement, peer isolation and model-off precedence.
func TestSearchReferencedCredentialInvalidation(t *testing.T) {
	engine := testEngine(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = readBodyForStudioTest(t, r)
		if r.Header.Get("Authorization") != "Bearer REF_KEY" {
			t.Error("Wrong referenced key")
		}
		fmt.Fprint(w, `{"results":[{"url":"https://example.org/a","snippet":"reference"}]}`)
	}))
	defer server.Close()
	models := testSettings()
	models.Profiles[0].Config.ProviderID = ""
	models.Profiles[0].Config.BaseURL = server.URL + "/v1"
	invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": models, "key_updates": map[string]SecretUpdate{"writer": {Action: "replace", Value: "REF_KEY"}}})
	profile := configureSearchFixture(t, engine, "deepseek", server.URL)
	view := invoke[searchSettingsView](t, engine, "get_search_settings", nil)
	profile.CredentialSource, profile.ModelProfileID = "model_ref", "writer"
	invoke[searchSettingsView](t, engine, "mutate_search_settings", searchMutation{Operation: "upsert_profile", ExpectedRevision: view.Revision, ModelRevision: view.ModelRevision, Profile: profile})
	input := searchStart{RequestID: newID(), Purpose: "test", Operation: "search", Query: "history", Selection: websearch.Selection{Mode: "profile", ProfileID: profile.ID}}
	done := awaitSearchFixture(t, engine, invoke[searchRun](t, engine, "start_search_run", input))
	assert(t, done.Status == "succeeded", "Referenced credential did not execute")
	info := invoke[map[string]any](t, engine, "get_search_profile", map[string]any{"profile_id": profile.ID})
	assert(t, info["test_valid"] == true, "Fresh referenced-credential test not valid")
	current := invoke[settingsResponse](t, engine, "get_model_settings", nil)
	invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": current.Settings, "key_updates": map[string]SecretUpdate{"writer": {Action: "replace", Value: "REF_KEY"}}})
	info = invoke[map[string]any](t, engine, "get_search_profile", map[string]any{"profile_id": profile.ID})
	assert(t, info["test_valid"] == false, "Same-value replacement reused old test validity")
	peer, err := NewEngine(engine.store.path)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	reject(t, peer, "start_search_run", searchStart{RequestID: newID(), Purpose: "test", Operation: "search", Query: "history", Selection: input.Selection})
	view = invoke[searchSettingsView](t, engine, "get_search_settings", nil)
	invoke[searchSettingsView](t, engine, "mutate_search_settings", searchMutation{Operation: "set_binding", ExpectedRevision: view.Revision, ModelRevision: view.ModelRevision, ModelProfileID: "writer", Binding: websearch.Selection{Mode: "off"}})
	reject(t, engine, "start_search_run", searchStart{RequestID: newID(), Purpose: "test", Operation: "search", Query: "history", ModelProfileID: "writer"})
	input.RequestID = newID()
	assert(t, awaitSearchFixture(t, engine, invoke[searchRun](t, engine, "start_search_run", input)).Status == "succeeded", "Explicit choice did not override model-off")
	current = invoke[settingsResponse](t, engine, "get_model_settings", nil)
	current.Settings.Profiles[0].Config.BaseURL = "https://elsewhere.example/v1"
	reject(t, engine, "save_model_settings", map[string]any{"settings": current.Settings})
}

// TestSearchConcurrentIdentityAndDeletion checks durable deduplication and project guards while a real HTTP request is outstanding.
func TestSearchConcurrentIdentityAndDeletion(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	var calls atomic.Int32
	started, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = readBodyForStudioTest(t, r)
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `{"results":[{"url":"https://example.org/a","snippet":"late"}]}`)
	}))
	defer server.Close()
	defer close(release)
	configureSearchFixture(t, engine, "deepseek", server.URL)
	input := searchStart{RequestID: newID(), ProjectID: project.ID, Purpose: "research", Operation: "search", Query: "history"}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			run, err := engine.startSearchRun(input)
			if err != nil {
				failures <- err
				return
			}
			ids <- run.ID
		})
	}
	wg.Wait()
	close(ids)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	id := ""
	for current := range ids {
		if id != "" && current != id {
			t.Error("Duplicate run allocated")
		}
		id = current
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("Request did not start")
	}
	if err := engine.store.DeleteProject(project.ID, project.Revision); err == nil {
		t.Fatal("Deleted project with active research")
	}
	invoke[searchSettingsView](t, engine, "disable_search", nil)
	done := awaitSearchFixture(t, engine, searchRun{ID: id, ProjectID: project.ID})
	assert(t, done.Status == "cancelled" && calls.Load() == 1, "Disable failed cancellation or request deduplication")
	if err := engine.store.DeleteProject(project.ID, project.Revision); err != nil {
		t.Fatal(err)
	}
}

// TestSearchTerminalCommitRecovery recovers a synced result after SQL failure without purchasing a second search.
func TestSearchTerminalCommitRecovery(t *testing.T) {
	engine := testEngine(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"results":[{"url":"https://example.org/a","snippet":"recover me"}]}`)
	}))
	defer server.Close()
	configureSearchFixture(t, engine, "deepseek", server.URL)
	if _, err := engine.store.db.Exec("CREATE TRIGGER reject_search_finish BEFORE UPDATE ON search_runs WHEN new.status NOT IN ('queued','running') BEGIN SELECT RAISE(ABORT,'fixture storage failure'); END"); err != nil {
		t.Fatal(err)
	}
	run := invoke[searchRun](t, engine, "start_search_run", searchStart{RequestID: newID(), Purpose: "test", Operation: "search", Query: "history"})
	path, _ := searchIntentPath(engine.owner.dir, run.ID)
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("No recoverable terminal intent")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := engine.store.db.Exec("DROP TRIGGER reject_search_finish"); err != nil {
		t.Fatal(err)
	}
	done := awaitSearchFixture(t, engine, run)
	assert(t, done.Status == "succeeded" && len(done.Response.Results) == 1 && calls.Load() == 1, "Commit recovery replayed or lost the request")
	pathDB := engine.store.path
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewEngine(pathDB)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	read := invoke[searchRun](t, restarted, "get_search_run", map[string]any{"run_id": run.ID})
	assert(t, read.Status == "succeeded" && calls.Load() == 1, "Restart replayed paid work")
}

// TestSearchMigrationBackupAndLegacyGuard upgrades a realistic version-two database without losing stories or memory settings.
func TestSearchMigrationBackupAndLegacyGuard(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	path := engine.store.path
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	old := defaultIntegrations()
	old.SearchEnabled = true
	old.MaxSearches = 4
	old.HindsightURL = "http://127.0.0.1:8888"
	old.Providers[0].Enabled = true
	if _, err := db.Exec("DROP TRIGGER search_legacy_insert_guard; DROP TRIGGER search_legacy_update_guard; DROP TABLE search_runs; DROP TABLE search_settings; PRAGMA user_version=2; UPDATE integrations SET document=? WHERE id=1", jsonText(old)); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	view := invoke[searchSettingsView](t, restarted, "get_search_settings", nil)
	assert(t, len(view.Settings.Profiles) == 9 && view.Settings.Enabled && len(view.Settings.FallbackProfileIDs) == 1, "Legacy search metadata was lost")
	legacy := invoke[struct {
		Settings IntegrationSettings `json:"settings"`
	}](t, restarted, "get_integrations", nil)
	assert(t, legacy.Settings.HindsightURL == "" && legacy.Settings.MaxSearches == 4, "Retirement or legacy search limits were lost")
	assert(t, invoke[Project](t, restarted, "get_project", map[string]any{"project_id": project.ID}).Title == project.Title, "Migration changed a story")
	stat, err := os.Stat(path + ".before-search.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	assert(t, stat.Mode().IsRegular() && stat.Size() > 0, "Backup is not a usable regular file")
	// Windows permissions use ACLs; Go's FileMode does not expose POSIX owner/group bits there.
	if runtime.GOOS != "windows" {
		assert(t, stat.Mode().Perm() == 0600, "Backup not private")
	}
	backup, err := sql.Open("sqlite", path+".before-search.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var backupTitle string
	if err := backup.QueryRow("SELECT json_extract(document,'$.title') FROM projects WHERE id=?", project.ID).Scan(&backupTitle); err != nil {
		t.Fatal(err)
	}
	assert(t, backupTitle == project.Title, "Recovery backup lost the original work")
	if _, err := db.Exec("UPDATE integrations SET document=? WHERE id=1", jsonText(old)); err == nil {
		t.Fatal("Old writer restored a second search truth")
	}
}

// TestSearchSnapshotReferenceGuards protects frozen fallbacks and keeps scoped tests out of global profile health.
func TestSearchSnapshotReferenceGuards(t *testing.T) {
	engine := testEngine(t)
	started, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = readBodyForStudioTest(t, r)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `{"results":[{"url":"https://example.org/a","snippet":"snapshot"}]}`)
	}))
	defer server.Close()
	defer close(release)
	primary := configureSearchFixture(t, engine, "deepseek", server.URL)
	fallback := configureSearchFixture(t, engine, "kimi", server.URL)
	saveSearchFixturePolicy(t, engine, primary.ID, []string{fallback.ID})
	projectID := insertSearchProject(t, engine)
	run := invoke[searchRun](t, engine, "start_search_run", searchStart{RequestID: newID(), Purpose: "research", Operation: "search", ProjectID: projectID, Query: "history"})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("Search not dispatched")
	}
	saveSearchFixturePolicy(t, engine, primary.ID, []string{})
	view := invoke[searchSettingsView](t, engine, "get_search_settings", nil)
	reject(t, engine, "mutate_search_settings", searchMutation{Operation: "delete_profile", ExpectedRevision: view.Revision, ModelRevision: view.ModelRevision, ProfileID: fallback.ID})
	invoke[searchRun](t, engine, "cancel_search_run", map[string]any{"run_id": run.ID, "project_id": projectID})
	awaitSearchFixture(t, engine, run)
	view = invoke[searchSettingsView](t, engine, "get_search_settings", nil)
	invoke[searchSettingsView](t, engine, "mutate_search_settings", searchMutation{Operation: "delete_profile", ExpectedRevision: view.Revision, ModelRevision: view.ModelRevision, ProfileID: fallback.ID})
	info := invoke[map[string]any](t, engine, "get_search_profile", map[string]any{"profile_id": primary.ID})
	assert(t, info["last_test"] == nil, "Research was reported as a global credential test")
}

// TestSearchNativeCallsReachParentHistory counts research models in the real Agent history as well as its enforced local allowance.
func TestSearchNativeCallsReachParentHistory(t *testing.T) {
	engine := testEngine(t)
	var outer, native atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := decodeJSON(readBodyForStudioTest(t, r), &body); err != nil {
			t.Error(err)
		}
		if body["tools"] != nil && !strings.Contains(jsonText(body["tools"]), nativeAgentFunction) {
			native.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"content":"Cited research","annotations":[{"type":"url_citation","url_citation":{"url":"https://example.org/a","content":"source"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
			return
		}
		text := `{"tool":"finish","text":"Done"}`
		if outer.Add(1) == 1 {
			text = `{"tool":"search","query":"history"}`
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", jsonText(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}, "finish_reason": "stop"}}}))
	}))
	defer server.Close()
	models := testSettings()
	for i := range models.Profiles {
		models.Profiles[i].Config.ProviderID = ""
		models.Profiles[i].Config.BaseURL = server.URL + "/v1"
	}
	invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": models})
	view := invoke[searchSettingsView](t, engine, "get_search_settings", nil)
	profile := websearch.DefaultProfile("openrouter")
	profile.ID = newID()
	profile.Name = "Agent research"
	profile.Endpoint = server.URL + "/v1"
	profile.ModelProfileID = "writer"
	profile.Enabled = true
	invoke[searchSettingsView](t, engine, "mutate_search_settings", searchMutation{Operation: "upsert_profile", ExpectedRevision: view.Revision, ModelRevision: view.ModelRevision, Profile: profile, KeyUpdates: map[string]SecretUpdate{profile.ID: {Action: "replace", Value: "LOCAL_NATIVE_KEY"}}})
	saveSearchFixturePolicy(t, engine, profile.ID, []string{})
	job := invoke[Job](t, engine, "start_ai", AIRequest{ProjectID: insertSearchProject(t, engine), Action: "agent", History: []Message{}, Instruction: "Research the history"})
	deadline := time.Now().Add(8 * time.Second)
	for job.Status == "running" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		job = invoke[Job](t, engine, "get_job", map[string]any{"job_id": job.ID})
	}
	assert(t, job.Status == "done" && outer.Load() == 2 && native.Load() == 1 && job.Calls == 3, jsonText(job))
}
