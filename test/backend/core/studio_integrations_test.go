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

// TestExternalMemoryUsesSelectedEvidenceAndBranchBank validates Hindsight operations without a real account.
func TestExternalMemoryUsesSelectedEvidenceAndBranchBank(t *testing.T) {
	project := testProject()
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
	for _, operation := range []string{"retain_external", "recall_external", "reflect_external"} {
		_, err := externalMemory(context.Background(), settings, &project, "branch-a", operation, "magic", []string{"approved", "unapproved"})
		if err != nil {
			t.Fatal(err)
		}
	}
	assert(t, len(paths) == 3 && strings.Contains(paths[0], project.ID+"-branch-a") && strings.HasSuffix(paths[1], "/memories/recall") && strings.HasSuffix(paths[2], "/reflect"), "Hindsight operation or bank isolation is incorrect")
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

// TestPolicyReplayRequiresReviewedDisjointCases rejects unverifiable gains and replay look-ahead.
func TestPolicyReplayRequiresReviewedDisjointCases(t *testing.T) {
	experiment := PolicyExperiment{Policies: []ExplorationPolicy{{ID: "baseline", Order: "breadth", MaxCalls: 2, MaxDepth: 3}, {ID: "candidate", Order: "best_parent", MaxCalls: 2, MaxDepth: 3}}, MinGain: .02}
	for _, split := range []string{"train", "holdout"} {
		for i := 0; i < 5; i++ {
			experiment.Cases = append(experiment.Cases, DiscoveryCase{ID: fmt.Sprintf("%s-%d", split, i), Split: split, Nodes: []DiscoveryNode{{ID: "a", Score: .5, Cost: 1, Reviewed: true}, {ID: "b", Score: .2, Cost: 1, Reviewed: true}, {ID: "c", ParentID: "a", Score: 1, Cost: 1, Reviewed: true}}})
		}
	}
	result, err := evaluatePolicies(experiment)
	assert(t, err == nil && result.Candidate == "candidate" && result.Promotable, "Reviewed held-out evidence did not support a genuinely better exploration order")
	experiment.Cases[0].Nodes[0].Reviewed = false
	_, err = evaluatePolicies(experiment)
	assert(t, err != nil, "Unreviewed model scores were treated as evidence")
	experiment.Cases[0].Nodes[0].Reviewed = true
	experiment.Cases[1].ID = experiment.Cases[0].ID
	_, err = evaluatePolicies(experiment)
	assert(t, err != nil, "Duplicate case IDs could leak across evaluation splits")
}
