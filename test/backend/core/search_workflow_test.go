//go:build quillweave_whitebox

package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	websearch "quillweave/internal/search"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// configureSearchFixture writes a real configuration and encrypted key through public commands.
func configureSearchFixture(t *testing.T, engine *Engine, family, address string) websearch.Profile {
	t.Helper()
	view := invoke[searchSettingsView](t, engine, "get_search_settings", nil)
	profile := websearch.DefaultProfile(family)
	profile.ID = newID()
	profile.Name = "Fixture " + profile.ID[:8]
	profile.Endpoint = address
	profile.Enabled = true
	view = invoke[searchSettingsView](t, engine, "mutate_search_settings", searchMutation{Operation: "upsert_profile", ExpectedRevision: view.Revision, ModelRevision: view.ModelRevision, Profile: profile, KeyUpdates: map[string]SecretUpdate{profile.ID: {Action: "replace", Value: "SEARCH_FIXTURE_KEY"}}})
	invoke[searchSettingsView](t, engine, "mutate_search_settings", map[string]any{"operation": "set_policy", "expected_revision": view.Revision, "model_revision": view.ModelRevision, "policy": map[string]any{"enabled": true, "default_profile_id": profile.ID, "fallback_policy": "none", "fallback_profile_ids": []string{}}})
	return profile
}

// awaitSearchFixture polls the real persisted lifecycle and fails instead of accepting an empty test run.
func awaitSearchFixture(t *testing.T, engine *Engine, run searchRun) searchRun {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		current, err := engine.getSearchRun(run.ID, run.ProjectID)
		if err != nil {
			t.Fatal(err)
		}
		if terminalSearch(current.Status) {
			return current
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Search did not reach a durable terminal state")
	return searchRun{}
}

// TestSearchProfilesExecuteAndRedact verifies actual DeepSeek/Kimi requests, source types and secret boundaries.
func TestSearchProfilesExecuteAndRedact(t *testing.T) {
	for _, family := range []string{"deepseek", "kimi"} {
		t.Run(family, func(t *testing.T) {
			engine := testEngine(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer SEARCH_FIXTURE_KEY" {
					t.Error("Wrong search credential")
				}
				var body map[string]any
				if err := decodeJSON(readBodyForStudioTest(t, r), &body); err != nil {
					t.Error(err)
				}
				if family == "deepseek" {
					if r.URL.Path != "/v1/tools/search" || body["query"] != "明代官制" || body["count"] == nil || body["text_query"] != nil {
						t.Error("Wrong DeepSeek request")
					}
					fmt.Fprint(w, `{"results":[{"title":"官制资料","url":"https://example.org/history","snippet":"摘要","summary":"供应商摘要"}]}`)
				} else {
					if r.URL.Path != "/v1/tools/search" || body["text_query"] != "明代官制" || body["query"] != nil {
						t.Error("Wrong Kimi request")
					}
					fmt.Fprint(w, `{"search_results":[{"title":"官制资料","url":"https://example.org/history","snippet":"搜索摘录"}]}`)
				}
			}))
			defer server.Close()
			profile := configureSearchFixture(t, engine, family, server.URL)
			run := invoke[searchRun](t, engine, "start_search_run", searchStart{RequestID: newID(), Purpose: "test", Operation: "search", Query: "明代官制", Selection: websearch.Selection{Mode: "profile", ProfileID: profile.ID}, UILanguage: "zh-CN"})
			done := awaitSearchFixture(t, engine, run)
			assert(t, done.Status == "succeeded" && len(done.Response.Results) == 1 && done.Response.SearchState == "executed" && done.Response.TestVerdict == "passed", jsonText(done))
			assert(t, calls.Load() == 1, "Search executed more than once")
			raw, err := engine.store.db.Query("SELECT document FROM search_settings UNION ALL SELECT document FROM search_runs")
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			for raw.Next() {
				var text string
				raw.Scan(&text)
				assert(t, !strings.Contains(text, "SEARCH_FIXTURE_KEY"), "Search state persisted a credential")
			}
			assert(t, !strings.Contains(jsonText(invoke[searchSettingsView](t, engine, "get_search_settings", nil)), "SEARCH_FIXTURE_KEY"), "Search settings exposed a credential")
		})
	}
}

// TestSearchRequestIdentityAndScope preserves one accepted run across configuration edits and rejects payload reuse.
func TestSearchRequestIdentityAndScope(t *testing.T) {
	engine := testEngine(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"results":[{"title":"Source","url":"https://example.org/source","snippet":"Original excerpt"}]}`)
	}))
	defer server.Close()
	profile := configureSearchFixture(t, engine, "deepseek", server.URL)
	input := searchStart{RequestID: newID(), Purpose: "test", Operation: "search", Query: "history", Selection: websearch.Selection{Mode: "profile", ProfileID: profile.ID}}
	first := invoke[searchRun](t, engine, "start_search_run", input)
	done := awaitSearchFixture(t, engine, first)
	view := invoke[searchSettingsView](t, engine, "get_search_settings", nil)
	profile.Name += " renamed"
	invoke[searchSettingsView](t, engine, "mutate_search_settings", searchMutation{Operation: "upsert_profile", ExpectedRevision: view.Revision, ModelRevision: view.ModelRevision, Profile: profile})
	again := invoke[searchRun](t, engine, "start_search_run", input)
	assert(t, again.ID == done.ID && calls.Load() == 1, "Retry after settings change repeated a paid request")
	input.Query = "different"
	reject(t, engine, "start_search_run", input)
	reject(t, engine, "get_search_run", map[string]any{"run_id": done.ID, "project_id": "another-work"})
	invoke[searchSettingsView](t, engine, "disable_search", nil)
	input.RequestID = newID()
	reject(t, engine, "start_search_run", input)
	assert(t, calls.Load() == 1, "Global off allowed a new supplier call")
}

// TestSearchCancelAndSourceAdoption binds source provenance to executed input and preserves cancellation.
func TestSearchCancelAndSourceAdoption(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	var block atomic.Bool
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = readBodyForStudioTest(t, r)
		if block.Load() {
			select {
			case started <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, `{"results":[{"title":"Source","url":"https://example.org/source","snippet":"Original source excerpt"}]}`)
	}))
	defer server.Close()
	configureSearchFixture(t, engine, "deepseek", server.URL)
	run := invoke[searchRun](t, engine, "start_search_run", searchStart{RequestID: newID(), ProjectID: project.ID, Purpose: "research", Operation: "search", Query: "original question", Selection: websearch.Selection{Mode: "inherit"}})
	done := awaitSearchFixture(t, engine, run)
	assert(t, done.Status == "succeeded", jsonText(done))
	adopted := invoke[Project](t, engine, "adopt_search_result", map[string]any{"project_id": project.ID, "revision": project.Revision, "run_id": done.ID, "result_id": done.Response.Results[0].ID})
	again := invoke[Project](t, engine, "adopt_search_result", map[string]any{"project_id": project.ID, "revision": project.Revision, "run_id": done.ID, "result_id": done.Response.Results[0].ID})
	assert(t, len(adopted.Sources) == 1 && len(again.Sources) == 1 && again.Sources[0].Provenance.Query == "original question", "Source adoption lost provenance or duplicated on retry")
	block.Store(true)
	input := searchStart{RequestID: newID(), Purpose: "test", Operation: "search", Query: "slow"}
	run = invoke[searchRun](t, engine, "start_search_run", input)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("Slow search never started")
	}
	invoke[searchRun](t, engine, "cancel_search_run", map[string]any{"run_id": run.ID})
	cancelled := awaitSearchFixture(t, engine, run)
	assert(t, cancelled.Status == "cancelled", "Late completion changed cancellation")
}

// TestSearchNativeEvidenceDoesNotTrustProse refuses to call an uncited model answer a successful web search.
func TestSearchNativeEvidenceDoesNotTrustProse(t *testing.T) {
	engine := testEngine(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Error("Wrong native endpoint")
		}
		fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"I searched the web, trust me."}]}],"usage":{"input_tokens":4,"output_tokens":9}}`)
	}))
	defer server.Close()
	models := testSettings()
	for i := range models.Profiles {
		models.Profiles[i].Config.BaseURL = server.URL + "/v1"
		models.Profiles[i].Config.ProviderID = ""
		models.Profiles[i].Config.Model = "deepseek-v4-pro"
	}
	invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": models})
	profile := websearch.DefaultProfile("deepseek")
	profile.ID = newID()
	profile.Name = "Native fixture"
	profile.Endpoint = server.URL
	profile.Mode = "native"
	profile.Enabled = true
	profile.ModelProfileID = "writer"
	profile.Limits.StrictBudget = false
	view := invoke[searchSettingsView](t, engine, "get_search_settings", nil)
	invoke[searchSettingsView](t, engine, "mutate_search_settings", searchMutation{Operation: "upsert_profile", ExpectedRevision: view.Revision, ModelRevision: view.ModelRevision, Profile: profile, KeyUpdates: map[string]SecretUpdate{profile.ID: {Action: "replace", Value: "FAKE_NATIVE_KEY"}}})
	view = invoke[searchSettingsView](t, engine, "get_search_settings", nil)
	invoke[searchSettingsView](t, engine, "mutate_search_settings", map[string]any{"operation": "set_policy", "expected_revision": view.Revision, "model_revision": view.ModelRevision, "policy": map[string]any{"enabled": true, "default_profile_id": profile.ID, "fallback_policy": "none", "fallback_profile_ids": []string{}}})
	run := invoke[searchRun](t, engine, "start_search_run", searchStart{RequestID: newID(), Purpose: "test", Operation: "search", Query: "history"})
	done := awaitSearchFixture(t, engine, run)
	assert(t, done.Status == "succeeded" && done.Response.SearchState == "not_triggered" && done.Response.TestVerdict == "inconclusive" && len(done.Response.Results) == 0, "Model prose masqueraded as verified search")
}
