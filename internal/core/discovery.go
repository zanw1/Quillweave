package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// DiscoveredModel preserves an upstream model ID alongside optional capability metadata.
type DiscoveredModel struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Limits ModelLimits `json:"limits"`
}

// discoverModels snapshots credentials briefly, then fetches without holding the engine or database lock.
func (engine *Engine) discoverModels(raw json.RawMessage) (any, error) {
	var input struct {
		ProfileID string         `json:"profile_id"`
		Config    ProviderConfig `json:"config"`
		KeyUpdate *SecretUpdate  `json:"key_update"`
	}
	if err := decodeJSON(raw, &input); err != nil {
		return nil, errors.New("The model discovery request is incomplete")
	}
	if input.Config.APIKey != "" {
		return nil, errors.New("Use the explicit replace operation to update an API key")
	}
	engine.mu.Lock()
	if engine.closed {
		engine.mu.Unlock()
		return nil, errors.New("The application is closing")
	}
	if err := engine.refreshModelsLocked(); err != nil {
		engine.mu.Unlock()
		return nil, err
	}
	input.Config.APIKey = engine.models.keyFor(ModelProfile{ID: input.ProfileID, Config: input.Config})
	ctx := engine.ctx
	engine.workers.Add(1)
	engine.mu.Unlock()
	defer engine.workers.Done()
	if input.KeyUpdate != nil {
		switch input.KeyUpdate.Action {
		case "clear":
			input.Config.APIKey = ""
		case "replace":
			input.Config.APIKey = strings.TrimSpace(input.KeyUpdate.Value)
			if input.Config.APIKey == "" {
				return nil, errors.New("A replacement key cannot be empty. Use the clear operation to remove a key")
			}
		}
	}
	models, err := listProviderModels(ctx, input.Config)
	if err != nil {
		return nil, err
	}
	return map[string]any{"models": models, "checked_at": now()}, nil
}

// discoveryEndpoint retains gateway paths while removing generation-specific endpoint suffixes.
func discoveryEndpoint(config ProviderConfig) (string, error) {
	base, err := validatedAPIBase(config.BaseURL)
	if err != nil {
		return "", err
	}
	for _, suffix := range []string{"/chat/completions", "/responses", "/messages", "/interactions", "/api/chat"} {
		base = strings.TrimSuffix(base, suffix)
	}
	if at := strings.LastIndex(base, "/models/"); at >= 0 && (strings.HasSuffix(base, ":generateContent") || strings.HasSuffix(base, ":streamGenerateContent")) {
		base = base[:at]
	}
	switch config.Protocol {
	case "openai", "responses":
		return strings.TrimSuffix(base, "/models") + "/models", nil
	case "anthropic":
		base = strings.TrimSuffix(base, "/models")
		if !strings.HasSuffix(base, "/v1") {
			base += "/v1"
		}
		return base + "/models", nil
	case "gemini", "gemini_generate":
		base = strings.TrimSuffix(base, "/models")
		if !strings.HasSuffix(base, "/v1beta") && !strings.HasSuffix(base, "/v1") {
			base += "/v1beta"
		}
		return base + "/models", nil
	case "ollama":
		base = strings.TrimSuffix(strings.TrimSuffix(base, "/api/tags"), "/v1")
		base = strings.TrimSuffix(base, "/api")
		return base + "/api/tags", nil
	default:
		return "", errors.New("Select a supported API protocol")
	}
}

// discoveryHeaders uses each native protocol's authentication without putting keys in URLs.
func discoveryHeaders(config ProviderConfig) http.Header {
	headers := http.Header{"Accept": []string{"application/json"}}
	if config.APIKey != "" {
		headers.Set("Authorization", "Bearer "+config.APIKey)
	}
	switch config.Protocol {
	case "gemini", "gemini_generate":
		headers.Del("Authorization")
		if config.APIKey != "" {
			headers.Set("x-goog-api-key", config.APIKey)
		}
	case "anthropic":
		headers.Set("anthropic-version", anthropicVersion(config))
		if anthropicVersion(config) == "2023-06-01" {
			headers.Del("Authorization")
			if config.APIKey != "" {
				headers.Set("x-api-key", config.APIKey)
			}
		}
	}
	return headers
}

// readModelPage bounds response size and avoids returning provider bodies that may echo credentials.
func readModelPage(ctx context.Context, client *http.Client, address string, headers http.Header) (map[string]json.RawMessage, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("Invalid API URL")
	}
	request.Header = headers.Clone()
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Model discovery was cancelled or timed out")
		}
		return nil, errors.New("Unable to fetch models. Check the API URL and network")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed {
		return nil, errors.New("This endpoint does not support model discovery. Enter a model ID manually")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Model discovery returned HTTP %d. Check the endpoint, key, and account permissions", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4_000_001))
	if ctx.Err() != nil {
		return nil, errors.New("Model discovery was cancelled or timed out")
	}
	if err != nil || len(data) > 4_000_000 {
		return nil, errors.New("The model list response exceeds the size limit or could not be read")
	}
	var page map[string]json.RawMessage
	if json.Unmarshal(data, &page) != nil || page == nil {
		return nil, errors.New("The endpoint returned an invalid model list")
	}
	return page, nil
}

// decodeModelRows tolerates malformed individual entries while requiring a real list envelope.
func decodeModelRows(page map[string]json.RawMessage, config ProviderConfig) ([]DiscoveredModel, error) {
	key := "data"
	if config.Protocol == "ollama" || strings.HasPrefix(config.Protocol, "gemini") {
		key = "models"
	}
	var rows []json.RawMessage
	if data, ok := page[key]; !ok || string(data) == "null" || json.Unmarshal(data, &rows) != nil {
		return nil, errors.New("The endpoint returned an invalid model list")
	}
	models := []DiscoveredModel{}
	for _, raw := range rows {
		var row struct {
			ID               string   `json:"id"`
			Name             string   `json:"name"`
			DisplayName      string   `json:"displayName"`
			ClaudeName       string   `json:"display_name"`
			InputTokenLimit  uint64   `json:"inputTokenLimit"`
			OutputTokenLimit uint32   `json:"outputTokenLimit"`
			ContextWindow    uint64   `json:"context_window"`
			ContextLength    uint64   `json:"context_length"`
			MaxOutputTokens  uint32   `json:"max_output_tokens"`
			Methods          []string `json:"supportedGenerationMethods"`
			OutputModalities []string `json:"output_modalities"`
			Architecture     struct {
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			TopProvider struct {
				MaxCompletionTokens uint32 `json:"max_completion_tokens"`
			} `json:"top_provider"`
		}
		if json.Unmarshal(raw, &row) != nil {
			continue
		}
		id, name := row.ID, row.Name
		if len(row.OutputModalities) == 0 {
			row.OutputModalities = row.Architecture.OutputModalities
		}
		if strings.HasPrefix(config.Protocol, "gemini") {
			id, name = strings.TrimPrefix(row.Name, "models/"), row.DisplayName
			if len(row.Methods) > 0 && !contains(row.Methods, "generateContent") {
				continue
			}
		} else if config.Protocol == "ollama" {
			id = row.Name
		} else if row.ClaudeName != "" {
			name = row.ClaudeName
		}
		if strings.TrimSpace(id) == "" || len(id) > 1024 || strings.ContainsAny(id, "\r\n\x00") || !textGenerationModel(id, row.OutputModalities) {
			continue
		}
		if name == "" {
			name = id
		}
		metadata, _ := lookupModel(config.ProviderID, id)
		limits := metadata.Limits
		if row.ContextWindow > 0 {
			limits.ContextWindowTokens = row.ContextWindow
		}
		if row.ContextLength > 0 {
			limits.ContextWindowTokens = row.ContextLength
		}
		if row.MaxOutputTokens > 0 {
			limits.MaxOutputTokens = row.MaxOutputTokens
		}
		if row.TopProvider.MaxCompletionTokens > 0 {
			limits.MaxOutputTokens = row.TopProvider.MaxCompletionTokens
		}
		if row.InputTokenLimit > 0 {
			limits.MaxInputTokens = row.InputTokenLimit
			limits.ContextWindowTokens = 0
		}
		if row.OutputTokenLimit > 0 {
			limits.MaxOutputTokens = row.OutputTokenLimit
		}
		models = append(models, DiscoveredModel{ID: id, Name: name, Limits: limits})
	}
	return models, nil
}

// textGenerationModel excludes known non-text endpoints without rejecting unfamiliar chat model names.
func textGenerationModel(id string, modalities []string) bool {
	if len(modalities) > 0 {
		return contains(modalities, "text")
	}
	id = strings.ToLower(id)
	for _, marker := range []string{"embedding", "rerank", "whisper", "tts", "transcribe", "realtime", "moderation", "dall-e", "gpt-image", "sora", "-audio"} {
		if strings.Contains(id, marker) {
			return false
		}
	}
	return true
}

// listProviderModels handles native pagination, deduplication, cancellation, and safe redirects.
func listProviderModels(parent context.Context, config ProviderConfig) ([]DiscoveredModel, error) {
	endpoint, err := discoveryEndpoint(config)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	headers := discoveryHeaders(config)
	models, ids, cursors := []DiscoveredModel{}, map[string]bool{}, map[string]bool{}
	cursor := ""
	for pageNumber := 0; pageNumber < 30; pageNumber++ {
		address, _ := url.Parse(endpoint)
		query := address.Query()
		if strings.HasPrefix(config.Protocol, "gemini") {
			query.Set("pageSize", "1000")
			if cursor != "" {
				query.Set("pageToken", cursor)
			}
		} else if config.Protocol == "anthropic" {
			query.Set("limit", "1000")
			if cursor != "" {
				query.Set("after_id", cursor)
			}
		}
		address.RawQuery = query.Encode()
		page, err := readModelPage(ctx, client, address.String(), headers)
		if err != nil {
			return nil, err
		}
		rows, err := decodeModelRows(page, config)
		if err != nil {
			return nil, err
		}
		for _, model := range rows {
			if !ids[model.ID] {
				ids[model.ID] = true
				models = append(models, model)
			}
		}
		if len(models) > 10000 {
			return nil, errors.New("The model list exceeds the application resource limit")
		}
		cursor = ""
		if strings.HasPrefix(config.Protocol, "gemini") {
			if token, exists := page["nextPageToken"]; exists && string(token) != "null" {
				if json.Unmarshal(token, &cursor) != nil {
					return nil, errors.New("The model list has an invalid pagination cursor")
				}
			}
		} else if config.Protocol == "anthropic" {
			var more bool
			if flag, exists := page["has_more"]; exists && json.Unmarshal(flag, &more) != nil {
				return nil, errors.New("The model list has an invalid pagination cursor")
			}
			if more {
				_ = json.Unmarshal(page["last_id"], &cursor)
				if cursor == "" {
					return nil, errors.New("The model list has an invalid pagination cursor")
				}
			}
		}
		if cursor == "" {
			sort.Slice(models, func(i, j int) bool { return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID) })
			return models, nil
		}
		if cursors[cursor] {
			return nil, errors.New("The model list repeated a pagination cursor")
		}
		cursors[cursor] = true
	}
	return nil, errors.New("The model list has too many pages")
}
