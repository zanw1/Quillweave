// Package search owns provider-neutral search metadata and network adapters, without depending on core.Engine.
package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type Fault struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Status  int            `json:"-"`
	Details map[string]any `json:"details,omitempty"`
}

// Error exposes a safe message while transports retain the stable classification separately.
func (fault *Fault) Error() string { return fault.Message }

// Fail creates a domain failure without including credentials or upstream response bodies.
func Fail(code, message string, status int) *Fault {
	return &Fault{Code: code, Message: message, Status: status}
}

type Selection struct {
	Mode      string `json:"mode"`
	ProfileID string `json:"profile_id,omitempty"`
}
type Limits struct {
	MaxSearches     int  `json:"max_searches"`
	MaxResults      int  `json:"max_results"`
	MaxFetches      int  `json:"max_fetches"`
	TimeoutSeconds  int  `json:"timeout_seconds"`
	MaxContentChars int  `json:"max_content_chars"`
	StrictBudget    bool `json:"strict_budget"`
}
type Filters struct {
	Language        string   `json:"language,omitempty"`
	AllowedDomains  []string `json:"allowed_domains,omitempty"`
	ExcludedDomains []string `json:"excluded_domains,omitempty"`
	StartDate       string   `json:"start_date,omitempty"`
	EndDate         string   `json:"end_date,omitempty"`
	IncludeContent  bool     `json:"include_content"`
}
type Profile struct {
	ID               string  `json:"id"`
	LegacyID         string  `json:"legacy_id,omitempty"`
	Name             string  `json:"name"`
	Family           string  `json:"family"`
	Mode             string  `json:"mode"`
	Region           string  `json:"region"`
	Endpoint         string  `json:"endpoint"`
	Enabled          bool    `json:"enabled"`
	CredentialSource string  `json:"credential_source"`
	ModelProfileID   string  `json:"model_profile_id,omitempty"`
	Engine           string  `json:"engine,omitempty"`
	SearchMode       string  `json:"search_mode,omitempty"`
	Limits           Limits  `json:"limits"`
	Filters          Filters `json:"filters"`
}

// UnmarshalJSON rejects secret/read-only or misspelled profile fields instead of silently accepting a fake configuration.
func (profile *Profile) UnmarshalJSON(data []byte) error {
	type wire Profile
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Fail("SEARCH_INVALID_ARGUMENT", "Search profile contains unknown or invalid fields", 400)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Fail("SEARCH_INVALID_ARGUMENT", "Search profile contains trailing JSON", 400)
	}
	*profile = Profile(value)
	return nil
}

type Settings struct {
	SchemaVersion      int                  `json:"schema_version"`
	Enabled            bool                 `json:"enabled"`
	Profiles           []Profile            `json:"profiles"`
	DefaultProfileID   string               `json:"default_profile_id"`
	FallbackPolicy     string               `json:"fallback_policy"`
	FallbackProfileIDs []string             `json:"fallback_profile_ids"`
	Bindings           map[string]Selection `json:"bindings"`
}
type Model struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Family    string `json:"family"`
	Protocol  string `json:"protocol"`
	BaseURL   string `json:"base_url"`
	NameID    string `json:"model"`
	Enabled   bool   `json:"enabled"`
	MaxTokens int    `json:"max_tokens"`
	APIKey    string `json:"-"`
}
type Result struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Content     string   `json:"content"`
	Snippet     string   `json:"snippet,omitempty"`
	Passages    []string `json:"passages,omitempty"`
	Provider    string   `json:"provider"`
	Engine      string   `json:"engine,omitempty"`
	RetrievedAt string   `json:"retrieved_at"`
	PublishedAt string   `json:"published_at,omitempty"`
	Reading     string   `json:"reading"`
	Truncated   bool     `json:"truncated,omitempty"`
	Redacted    bool     `json:"redacted,omitempty"`
}
type Citation struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Quote string `json:"quote,omitempty"`
}
type Provenance struct {
	RunID       string `json:"run_id"`
	ResultID    string `json:"result_id"`
	Query       string `json:"query"`
	URL         string `json:"url"`
	Provider    string `json:"provider"`
	Engine      string `json:"engine,omitempty"`
	Reading     string `json:"reading"`
	RetrievedAt string `json:"retrieved_at"`
	PublishedAt string `json:"published_at,omitempty"`
	ContentHash string `json:"content_hash"`
	Truncated   bool   `json:"truncated,omitempty"`
}
type Answer struct {
	Text      string     `json:"text"`
	Citations []Citation `json:"citations"`
}
type Usage struct {
	AttemptsReserved  int      `json:"attempts_reserved"`
	SearchCalls       *int     `json:"search_calls"`
	FetchCalls        *int     `json:"fetch_calls"`
	ModelInputTokens  *int     `json:"model_input_tokens"`
	ModelOutputTokens *int     `json:"model_output_tokens"`
	ReportedCost      *float64 `json:"reported_cost"`
	Currency          string   `json:"currency,omitempty"`
	CostStatus        string   `json:"cost_status"`
}
type Response struct {
	Results     []Result `json:"results"`
	Answer      *Answer  `json:"answer,omitempty"`
	Usage       Usage    `json:"usage"`
	Warnings    []string `json:"warnings"`
	Engine      string   `json:"engine,omitempty"`
	SearchState string   `json:"search_state"`
	TestVerdict string   `json:"test_verdict"`
}

// Find returns one stable configuration reference without silently picking another service.
func (settings Settings) Find(id string) (Profile, error) {
	for _, profile := range settings.Profiles {
		if profile.ID == id {
			return profile, nil
		}
	}
	return Profile{}, Fail("SEARCH_PROFILE_UNAVAILABLE", "The selected search profile no longer exists", 422)
}

// EmptyResponse distinguishes unknown supplier billing from a known zero.
func EmptyResponse() Response {
	return Response{Results: []Result{}, Warnings: []string{}, Usage: Usage{CostStatus: "unknown"}, SearchState: "unknown", TestVerdict: "inconclusive"}
}

// Canonical marshals typed public metadata for fingerprints and durable snapshots.
func Canonical(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("Cannot serialize search metadata: %v", err))
	}
	return string(data)
}
