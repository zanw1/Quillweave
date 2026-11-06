//go:build quillweave_whitebox

package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fixtureProfile prepares public metadata only, using disposable loopback services instead of real paid accounts.
func fixtureProfile(family, endpoint string) Profile {
	p := DefaultProfile(family)
	p.ID, p.Name, p.Endpoint, p.Enabled = "fixture", "Fixture", endpoint, true
	return p
}

// TestStandaloneContracts verifies all maintained wire formats, including Pro chunks and standalone page reading.
func TestStandaloneContracts(t *testing.T) {
	cases := []struct{ family, mode, operation, path, queryField, response, reading string }{
		{"deepseek", "search", "search", "/v1/tools/search", "query", `{"results":[{"url":"https://example.org/a","title":"A","summary":"summary"}]}`, "model_summary"},
		{"deepseek", "search", "fetch", "/v1/tools/fetch", "url", `{"title":"A","content":"page"}`, "fetched_page"},
		{"kimi", "basic", "search", "/v1/tools/search", "text_query", `{"search_results":[{"url":"https://example.org/a","text":"page"}]}`, "fetched_page"},
		{"kimi", "pro", "search", "/v1/tools/search_pro", "text_query", `{"search_results":[{"url":"https://example.org/a","chunks":[{"text":"passage"}]}]}`, "provider_passage"},
		{"kimi", "pro", "fetch", "/v1/tools/fetch", "url", `{"title":"A","markdown":"page"}`, "fetched_page"},
		{"tavily", "search", "search", "/search", "query", `{"results":[{"url":"https://example.org/a","raw_content":"page"}]}`, "fetched_page"},
		{"bocha", "search", "search", "/search", "query", `{"data":{"webPages":{"value":[{"url":"https://example.org/a","summary":"excerpt"}]}}}`, "search_excerpt"},
		{"exa", "search", "search", "/search", "query", `{"results":[{"url":"https://example.org/a","text":"page"}]}`, "search_excerpt"},
		{"exa", "search", "fetch", "/contents", "ids", `{"results":[{"url":"https://example.org/a","text":"page"}]}`, "fetched_page"},
		{"serper", "search", "search", "/search", "q", `{"organic":[{"link":"https://example.org/a","snippet":"excerpt"}]}`, "search_excerpt"},
		{"searxng", "search", "search", "/search", "q", `{"results":[{"url":"https://example.org/a","content":"excerpt"}]}`, "search_excerpt"},
		{"wikipedia", "search", "search", "/search", "srsearch", `{"query":{"search":[{"title":"历史","snippet":"excerpt"}]}}`, "search_excerpt"},
	}
	for _, c := range cases {
		t.Run(c.family+"_"+c.mode+"_"+c.operation, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != c.path {
					t.Errorf("path=%s, want %s", r.URL.Path, c.path)
				}
				if r.Method == "GET" {
					if r.URL.Query().Get(c.queryField) != "history" {
						t.Error("missing query")
					}
				} else {
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || body[c.queryField] == nil {
						t.Error("wrong request body")
					}
					if c.family == "kimi" && c.mode == "pro" && c.operation == "search" && (body["sites"] == nil || body["time_window"] == nil || body["include_content"] != nil) {
						t.Error("Kimi Pro parameters lost")
					}
				}
				fmt.Fprint(w, c.response)
			}))
			defer server.Close()
			endpoint := server.URL + "/search"
			if c.family == "deepseek" || c.family == "kimi" {
				endpoint = server.URL + "/v1"
			}
			profile := fixtureProfile(c.family, endpoint)
			profile.Mode = c.mode
			if c.family == "kimi" && c.mode == "pro" {
				profile.Filters = Filters{AllowedDomains: []string{"example.org"}, StartDate: "2020-01-01", EndDate: "2026-01-01"}
			}
			response, err := Execute(context.Background(), ExecuteRequest{Profile: profile, Operation: c.operation, Query: "history", URL: "https://example.org/a", Key: "fake-only", Language: "zh-CN", Remaining: 2})
			if err != nil {
				t.Fatal(err)
			}
			if response.SearchState != "executed" || len(response.Results) != 1 || response.Results[0].Reading != c.reading {
				t.Fatalf("wrong evidence: %+v", response)
			}
			if c.operation == "fetch" && (response.Usage.FetchCalls == nil || *response.Usage.FetchCalls != 1 || *response.Usage.SearchCalls != 0) {
				t.Fatal("fetch charged as search")
			}
		})
	}
}

// TestNativeContracts verifies native Responses and OpenRouter server tools with actual structured evidence and bounded retention.
func TestNativeContracts(t *testing.T) {
	for _, family := range []string{"deepseek", "kimi", "openrouter"} {
		t.Run(family, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["stream"] != false || body["model"] == nil {
					t.Error("wrong model request")
				}
				if family == "openrouter" {
					if r.URL.Path != "/api/v1/chat/completions" {
						t.Error(r.URL.Path)
					}
					tool := body["tools"].([]any)[0].(map[string]any)
					params := Object(tool, "parameters")
					if Text(tool, "type") != "openrouter:web_search" || Text(params, "engine") != "exa" || params["max_uses"] != float64(2) {
						t.Error("missing bounded server tool")
					}
					fmt.Fprint(w, `{"choices":[{"message":{"content":"中文回答","annotations":[{"type":"url_citation","url_citation":{"url":"https://example.org/a","title":"资料","content":"可信摘录"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":20,"cost":0.001,"server_tool_use_details":{"web_search_requests":1}}}`)
				} else {
					if r.URL.Path != "/api/v1/responses" {
						t.Error(r.URL.Path)
					}
					if !strings.Contains(Text(body, "instructions"), "简体中文") {
						t.Error("UI language lost")
					}
					if family == "kimi" && body["include"] == nil {
						t.Error("Kimi source inclusion missing")
					}
					fmt.Fprint(w, `{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"sources":[{"url":"https://example.org/a","title":"资料","content":"可信摘录"}]}},{"type":"message","content":[{"type":"output_text","text":"中文回答","annotations":[{"type":"url_citation","url":"https://example.org/a","title":"资料"}]}]}],"usage":{"input_tokens":10,"output_tokens":20}}`)
				}
			}))
			defer server.Close()
			profile := fixtureProfile(family, server.URL+"/api/v1")
			profile.Mode, profile.ModelProfileID = "native", "model"
			profile.Limits.StrictBudget = family == "openrouter"
			name := "deepseek-v4-pro"
			if family == "kimi" {
				name = "kimi-k3"
			}
			if family == "openrouter" {
				name = "provider/model"
				profile.Mode = "server_tool"
			}
			response, err := Execute(context.Background(), ExecuteRequest{Profile: profile, Model: Model{NameID: name, Family: family, Enabled: true}, Operation: "search", Query: "history", Language: "zh-CN", Remaining: 2})
			if err != nil {
				t.Fatal(err)
			}
			if response.TestVerdict != "passed" || response.SearchState != "executed" || response.Answer == nil || len(response.Answer.Citations) != 1 || len(response.Results) != 1 || response.Results[0].Content != "可信摘录" {
				t.Fatalf("lost evidence: %+v", response)
			}
			if *response.Usage.ModelInputTokens != 10 || *response.Usage.ModelOutputTokens != 20 {
				t.Fatal("tokens lost")
			}
			if family == "openrouter" && (response.Usage.ReportedCost == nil || *response.Usage.SearchCalls != 1) {
				t.Fatal("billing metadata lost")
			}
		})
	}
}

// TestSearchValidationAndFailures makes invalid inputs, wrong schemas, credential echo and redirects observable failures.
func TestSearchValidationAndFailures(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/wrong/v1/tools/search":
			fmt.Fprint(w, `{"answer":"not a result array"}`)
		case "/denied/v1/tools/search":
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":"SECRET-ECHO"}`)
		default:
			fmt.Fprint(w, `{"results":[{"url":"https://example.org/a","title":"SECRET-ECHO","content":"SECRET-ECHO"},{"url":"javascript:alert(1)","content":"bad"}]}`)
		}
	}))
	defer server.Close()
	request := ExecuteRequest{Profile: fixtureProfile("deepseek", server.URL), Operation: "search", Query: "history", Key: "SECRET-ECHO"}
	response, err := Execute(context.Background(), request)
	if err != nil || len(response.Results) != 1 || strings.Contains(Canonical(response), request.Key) {
		t.Fatalf("redaction failed: %v", err)
	}
	for _, path := range []string{"wrong", "denied"} {
		request.Profile.Endpoint = server.URL + "/" + path
		_, err = Execute(context.Background(), request)
		var fault *Fault
		if !errors.As(err, &fault) || strings.Contains(err.Error(), request.Key) {
			t.Fatal("schema/error not safely rejected")
		}
	}
	before := calls.Load()
	for _, mutate := range []func(*ExecuteRequest){
		func(r *ExecuteRequest) { r.Query = strings.Repeat("中", 129) },
		func(r *ExecuteRequest) { r.Profile.ID = "off" },
		func(r *ExecuteRequest) { r.Profile.Endpoint = "https://user:secret@example.org" },
		func(r *ExecuteRequest) { r.Profile.Limits.MaxResults = 51 },
		func(r *ExecuteRequest) { r.Profile.Filters.EndDate = "2020-99-01" },
	} {
		candidate := request
		mutate(&candidate)
		if _, err := Execute(context.Background(), candidate); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	if calls.Load() != before {
		t.Fatal("invalid input reached network")
	}
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 307) }))
	defer redirect.Close()
	request.Profile = fixtureProfile("deepseek", redirect.URL)
	if _, err := Execute(context.Background(), request); err == nil || redirected.Load() != 0 {
		t.Fatal("credential redirected across origins")
	}
}

// TestNativeIncompleteUsage retains supplier-reported usage and evidence while returning an actual incomplete-response failure.
func TestNativeIncompleteUsage(t *testing.T) {
	invalidUsage := parseNative(Profile{}, map[string]any{"usage": map[string]any{"cost": "+Inf"}}, "search")
	if invalidUsage.Usage.ReportedCost != nil {
		t.Fatal("Non-finite cost cannot enter durable JSON")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}],"usage":{"input_tokens":15,"output_tokens":7}}`)
	}))
	defer server.Close()
	profile := fixtureProfile("deepseek", server.URL)
	profile.Mode = "native"
	profile.ModelProfileID = "model"
	profile.Limits.StrictBudget = false
	response, err := Execute(context.Background(), ExecuteRequest{Profile: profile, Operation: "search", Query: "history", Model: Model{Enabled: true, NameID: "deepseek-v4-pro", Family: "deepseek"}})
	if err == nil || response.Usage.ModelInputTokens == nil || *response.Usage.ModelInputTokens != 15 || response.Usage.ModelOutputTokens == nil || *response.Usage.ModelOutputTokens != 7 || response.TestVerdict != "failed" {
		t.Fatal("Incomplete response lost usage or became success")
	}
}
