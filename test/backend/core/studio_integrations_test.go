//go:build quillweave_whitebox

package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// readBodyForStudioTest reports request decoding failures in the owning test while limiting input bytes.
func readBodyForStudioTest(t *testing.T, r *http.Request) []byte {
	t.Helper()
	data, err := io.ReadAll(io.LimitReader(r.Body, 2_000_000))
	if err != nil {
		t.Error(err)
	}
	return data
}

// TestResearchAdaptersAndFallback validates real provider schemas, citations, query limits and secret redaction.
func TestResearchAdaptersAndFallback(t *testing.T) {
	for _, kind := range []string{"tavily", "bocha", "exa", "serper", "searxng", "wikipedia"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				entry := map[string]any{"title": "Historical office", "name": "Historical office", "url": "https://example.org/source", "link": "https://example.org/source", "content": "Source excerpt", "snippet": "Source excerpt"}
				var response any
				switch kind {
				case "tavily", "exa", "searxng":
					response = map[string]any{"results": []any{entry}}
				case "bocha":
					response = map[string]any{"data": map[string]any{"webPages": map[string]any{"value": []any{entry}}}}
				case "serper":
					response = map[string]any{"organic": []any{entry}}
				case "wikipedia":
					response = map[string]any{"query": map[string]any{"search": []any{entry}}}
				}
				fmt.Fprint(w, jsonText(response))
			}))
			t.Cleanup(server.Close)
			settings := defaultIntegrations()
			settings.SearchEnabled = true
			settings.Providers = []SearchProvider{{ID: "fixture", Name: "Fixture", Kind: kind, Endpoint: server.URL, Enabled: true, APIKey: "secret-fixture"}}
			result, err := runResearch(context.Background(), settings, "historical office", "zh-CN", "")
			assert(t, err == nil && len(result.Results) == 1 && result.Results[0].RetrievedAt != "", fmt.Sprint(err))
			assert(t, !strings.Contains(jsonText(integrationView(settings)), "secret-fixture"), "Integration view leaked credentials")
			settings.SearchEnabled = false
			_, err = runResearch(context.Background(), settings, "historical office", "zh-CN", "")
			assert(t, err != nil && calls.Load() == 1, "Disabled research made a network call")
		})
	}
	failure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	t.Cleanup(failure.Close)
	success := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":[{"title":"Source","url":"https://example.org/source","content":"Verified retrieval"}]}`)
	}))
	t.Cleanup(success.Close)
	settings := defaultIntegrations()
	settings.SearchEnabled = true
	settings.Providers = []SearchProvider{{ID: "limited", Name: "Limited", Kind: "tavily", Endpoint: failure.URL, Enabled: true}, {ID: "fallback", Name: "Fallback", Kind: "searxng", Endpoint: success.URL, Enabled: true}}
	response, err := runResearch(context.Background(), settings, "history", "en", "")
	assert(t, err == nil && len(response.Warnings) == 1 && len(response.Results) == 1 && response.Provider == "Fallback", "Rate-limit fallback lost provenance or errors")
}

// TestExternalMemoryRetirement rejects legacy operations before any network request.
func TestExternalMemoryRetirement(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	project.Studio.Memories = []StoryMemory{{ID: "approved", Kind: "rule", Title: "Cost", Content: "Magic has a cost", Status: "confirmed"}, {ID: "unapproved", Kind: "setting", Content: "Must not be sent", Status: "proposed"}}
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		raw := string(readBodyForStudioTest(t, r))
		if strings.Contains(raw, "Must not be sent") {
			t.Error("Unconfirmed knowledge was exported")
		}
		fmt.Fprint(w, `{"success":true,"results":[]}`)
	}))
	t.Cleanup(server.Close)
	settings := defaultIntegrations()
	settings.HindsightURL = server.URL
	view := invoke[map[string]any](t, engine, "save_integrations", map[string]any{"settings": settings, "key_updates": map[string]SecretUpdate{"hindsight": {Action: "replace", Value: "RETIRED_TEST_SECRET"}}})
	assert(t, !strings.Contains(jsonText(view), server.URL) && !strings.Contains(jsonText(view), "RETIRED_TEST_SECRET"), "retired service remains exposed")
	for _, operation := range []string{"retain_external", "recall_external", "reflect_external"} {
		_, err := engine.Dispatch(operation, rawMessage(map[string]any{"project_id": project.ID, "query": "magic"}))
		assert(t, err != nil && strings.Contains(err.Error(), "retired"), "legacy operation was accepted")
	}
	assert(t, len(paths) == 0, "retired service received a request")
}

// TestAgentToolsStageChangesAndRespectCallBounds checks real model-tool-model iteration and author control.
func TestAgentToolsStageChangesAndRespectCallBounds(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	responses := []string{`{"tool":"recall","query":"letter"}`, `{"tool":"propose","proposal":{"kind":"decision","title":"A costly secret","content":"The keeper must choose whom to trust."}}`, `{"tool":"finish","text":"Choose the conflict you want to explore; the idea remains a proposal."}`}
	server, calls := sequenceModel(t, responses)
	config := defaultProvider()
	config.Model = "fixture"
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	done := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": "", "action": "agent", "instruction": "Help develop this story"}))
	assert(t, done.Status == "done" && calls.Load() == 3 && len(done.Steps) == 3 && len(done.Proposals) == 1, done.Error)
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(current.Studio.Decisions) == 0 && current.Revision == project.Revision, "The agent applied a proposal without the author")
}
