package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	websearch "quillweave/internal/search"
	"strings"
)

type SearchProvider struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Enabled  bool   `json:"enabled"`
	APIKey   string `json:"api_key,omitempty"`
	profile  *websearch.Profile
}
type IntegrationSettings struct {
	Revision       uint64           `json:"revision"`
	SearchRevision uint64           `json:"search_revision"`
	Providers      []SearchProvider `json:"providers"`
	SearchEnabled  bool             `json:"search_enabled"`
	MaxSearches    int              `json:"max_searches"`
	HindsightURL   string           `json:"hindsight_url,omitempty"`
	HindsightKey   string           `json:"hindsight_key,omitempty"`
}
type ResearchResult = websearch.Result
type ResearchResponse struct {
	Results     []ResearchResult  `json:"results"`
	Warnings    []string          `json:"warnings"`
	Provider    string            `json:"provider"`
	Attempts    int               `json:"attempts"`
	RunID       string            `json:"run_id,omitempty"`
	Answer      *websearch.Answer `json:"answer,omitempty"`
	SearchState string            `json:"search_state,omitempty"`
	failure     error
}

// defaultIntegrations exposes optional providers without issuing network requests on startup.
func defaultIntegrations() IntegrationSettings {
	return IntegrationSettings{MaxSearches: 2, Providers: []SearchProvider{
		{ID: "tavily", Name: "Tavily", Kind: "tavily", Endpoint: "https://api.tavily.com/search"},
		{ID: "bocha", Name: "博查", Kind: "bocha", Endpoint: "https://api.bocha.cn/v1/web-search"},
		{ID: "exa", Name: "Exa", Kind: "exa", Endpoint: "https://api.exa.ai/search"},
		{ID: "serper", Name: "Serper", Kind: "serper", Endpoint: "https://google.serper.dev/search"},
		{ID: "searxng", Name: "SearXNG", Kind: "searxng", Endpoint: ""},
		{ID: "wikipedia", Name: "Wikipedia", Kind: "wikipedia", Endpoint: "https://zh.wikipedia.org/w/api.php", Enabled: true},
	}}
}

// WithoutSecrets redacts all integration keys from settings views and persisted documents.
func (settings IntegrationSettings) WithoutSecrets() IntegrationSettings {
	settings.Providers = append([]SearchProvider{}, settings.Providers...)
	for i := range settings.Providers {
		settings.Providers[i].APIKey = ""
	}
	settings.HindsightURL = ""
	settings.HindsightKey = ""
	return settings
}

// IntegrationSettings loads optional integration metadata, never credentials, from the local database.
func (store *Store) IntegrationSettings() (IntegrationSettings, error) {
	return readIntegrations(store.db)
}

// readIntegrations reads metadata through the same connection as a settings comparison or snapshot.
func readIntegrations(db documentDB) (IntegrationSettings, error) {
	var raw string
	settings := defaultIntegrations()
	err := db.QueryRowContext(context.Background(), "SELECT document FROM integrations WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	err = decodeJSON([]byte(raw), &settings)
	return settings.WithoutSecrets(), err
}

// refreshIntegrationsLocked merges shared metadata with only matching process-local credentials.
func (engine *Engine) refreshIntegrationsLocked() error {
	return engine.refreshSearchCompatLocked()
}

// integrationView tells the UI which credentials exist without returning secret material.
func integrationView(settings IntegrationSettings) any {
	keys := map[string]bool{}
	for _, p := range settings.Providers {
		keys[p.ID] = p.APIKey != ""
	}
	return map[string]any{"settings": settings.WithoutSecrets(), "key_status": keys}
}

// validateEndpoint accepts explicit local gateways but rejects embedded credentials and unsupported schemes.
func validateEndpoint(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return errors.New("Enter an HTTP or HTTPS endpoint without embedded credentials")
	}
	return nil
}

// saveIntegrations applies revision and credential operations atomically to an immutable settings snapshot.
func (engine *Engine) saveIntegrations(settings IntegrationSettings, keys map[string]SecretUpdate) (any, error) {
	return engine.saveSearchIntegrations(settings, keys)
}

// searchProvider normalizes provider-specific result schemas into attributable source cards.
func searchProvider(ctx context.Context, provider SearchProvider, query, language string) ([]ResearchResult, error) {
	profile := websearch.DefaultProfile(provider.Kind)
	profile.ID = provider.ID
	profile.Name = provider.Name
	if profile.Name == "" {
		profile.Name = provider.ID
	}
	profile.Endpoint = provider.Endpoint
	if provider.profile != nil {
		profile = *provider.profile
	}
	response, err := websearch.Execute(ctx, websearch.ExecuteRequest{Profile: profile, Key: provider.APIKey, Operation: "search", Query: query, Language: language, Remaining: 1})
	return response.Results, err
}

// runResearch tries configured providers in order and preserves fallback failures for the author.
func runResearch(ctx context.Context, settings IntegrationSettings, query, language, only string) (ResearchResponse, error) {
	result := ResearchResponse{Results: []ResearchResult{}, Warnings: []string{}}
	if strings.TrimSpace(query) == "" || charCount(query) > 500 {
		return result, errors.New("Search query must contain 1–500 characters")
	}
	if !settings.SearchEnabled && only == "" {
		return result, errors.New("Enable web research in integration settings first")
	}
	attempts := 0
	for _, provider := range settings.Providers {
		if (only == "" && !provider.Enabled) || (only != "" && provider.ID != only) {
			continue
		}
		if attempts >= settings.MaxSearches {
			break
		}
		attempts++
		result.Attempts = attempts
		results, err := searchProvider(ctx, provider, query, language)
		if err != nil {
			result.failure = err
			result.Warnings = append(result.Warnings, provider.Name+": "+err.Error())
			continue
		}
		if len(results) == 0 {
			result.Warnings = append(result.Warnings, provider.Name+": no results")
			continue
		}
		result.Results = results
		result.Provider = provider.Name
		return result, nil
	}
	if attempts == 0 {
		return result, errors.New("No configured search provider is enabled")
	}
	return result, nil
}

// decodeSettings accepts the explicit settings envelope without persisting a raw API key field.
func decodeSettings(raw json.RawMessage) (IntegrationSettings, map[string]SecretUpdate, error) {
	var request struct {
		Settings IntegrationSettings     `json:"settings"`
		Keys     map[string]SecretUpdate `json:"key_updates"`
	}
	err := decodeJSON(raw, &request)
	return request.Settings, request.Keys, err
}
