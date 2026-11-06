package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	websearch "quillweave/internal/search"
	"strings"
	"time"
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
	HindsightURL   string           `json:"hindsight_url"`
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
	keys := map[string]bool{"hindsight": settings.HindsightKey != ""}
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

// externalJSON makes a bounded cancellable request and prevents credentials from following redirects.
func externalJSON(ctx context.Context, method, address string, body any, headers map[string]string) (map[string]any, error) {
	if err := validateEndpoint(address); err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewBufferString(jsonText(body))
	}
	request, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Quillweave/3 story research")
	for k, v := range headers {
		request.Header.Set(k, v)
	}
	client := http.Client{Timeout: 20 * time.Second, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) > 3 || next.URL.Host != via[0].URL.Host {
			return errors.New("Cross-host redirect refused; configure the final endpoint")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("External service could not be reached")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2_000_001))
	if err != nil {
		return nil, err
	}
	if len(raw) > 2_000_000 {
		return nil, errors.New("External response exceeds 2 MB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("External service returned HTTP %d", response.StatusCode)
	}
	var value map[string]any
	if err := decodeJSON(raw, &value); err != nil {
		return nil, errors.New("External service returned invalid JSON")
	}
	return value, nil
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

// externalMemory uses an explicitly configured Hindsight bank isolated by story and branch.
func externalMemory(ctx context.Context, settings IntegrationSettings, project *Project, sessionID, operation, query string, ids []string) (map[string]any, error) {
	if operation != "retain_external" && (strings.TrimSpace(query) == "" || charCount(query) > 500) {
		return nil, errors.New("Memory query must contain 1–500 characters")
	}
	if settings.HindsightURL == "" {
		return nil, errors.New("Configure a Hindsight endpoint first")
	}
	bank := "quillweave-" + project.ID
	if sessionID != "" {
		bank += "-" + sessionID
	}
	path := "/v1/default/banks/" + url.PathEscape(bank) + "/memories/recall"
	body := map[string]any{"query": query, "budget": "low"}
	switch operation {
	case "retain_external":
		items := []map[string]any{}
		size := 0
		for _, id := range ids {
			for _, m := range project.Studio.Memories {
				if m.ID == id && m.Status == "confirmed" && memoryCurrent(project, m) {
					size += charCount(m.Content)
					items = append(items, map[string]any{"content": m.Content, "document_id": "quillweave-" + m.ID, "context": "Author-confirmed " + m.Kind + ". Source: " + m.SourceID + ". Story time: " + m.StoryTime})
				}
			}
		}
		if len(items) == 0 || size > 20000 {
			return nil, errors.New("Select confirmed memories within 20,000 characters")
		}
		path = "/v1/default/banks/" + url.PathEscape(bank) + "/memories"
		body = map[string]any{"items": items, "async": false}
	case "reflect_external":
		path = "/v1/default/banks/" + url.PathEscape(bank) + "/reflect"
		body["query"] = query + "\n" + localText(project, "Answer in English and preserve quoted sources.", "请使用简体中文回答，保留引用原文，并区分事实与推断。")
	case "recall_external":
	default:
		return nil, errors.New("Unsupported memory operation")
	}
	headers := map[string]string{}
	if settings.HindsightKey != "" {
		headers["Authorization"] = "Bearer " + settings.HindsightKey
	}
	return externalJSON(ctx, "POST", strings.TrimRight(settings.HindsightURL, "/")+path, body, headers)
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
