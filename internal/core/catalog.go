package core

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// These are application resource guards, separate from a model's advertised limits.
const maxOutputBudget = 2_097_152
const maxContextCharacters = 8_000_000

// ModelLimits records token capacities independently of the local character budget.
type ModelLimits struct {
	ContextWindowTokens uint64 `json:"context_window_tokens,omitempty"`
	MaxInputTokens      uint64 `json:"max_input_tokens,omitempty"`
	MaxOutputTokens     uint32 `json:"max_output_tokens,omitempty"`
}

// ModelMetadata is portable catalog information; ID is never substituted into an API request.
type ModelMetadata struct {
	ID                   string      `json:"id"`
	Name                 string      `json:"name"`
	Limits               ModelLimits `json:"limits"`
	TemperatureSupported *bool       `json:"temperature_supported,omitempty"`
	ReasoningEfforts     []string    `json:"reasoning_efforts,omitempty"`
}

type modelCatalogSnapshot struct {
	Source  string                   `json:"source"`
	Version string                   `json:"version"`
	Models  map[string]ModelMetadata `json:"models"`
	Aliases map[string]string        `json:"aliases"`
}

//go:embed model_catalog.json
var modelCatalogJSON []byte

var bundledModelCatalog = readBundledModelCatalog()

// readBundledModelCatalog loads the version-pinned, MIT-licensed data without network access.
func readBundledModelCatalog() modelCatalogSnapshot {
	var snapshot modelCatalogSnapshot
	if err := json.Unmarshal(modelCatalogJSON, &snapshot); err != nil {
		panic("invalid bundled model catalog: " + err.Error())
	}
	return snapshot
}

// modelCatalogKey normalizes punctuation only for metadata lookup, preserving model sizes.
func modelCatalogKey(id string) string {
	return strings.NewReplacer(".", "-", "_", "-").Replace(strings.ToLower(strings.TrimSpace(id)))
}

// lookupModel resolves provider aliases before exact canonical metadata, retaining the caller's API ID.
func lookupModel(provider, id string) (ModelMetadata, bool) {
	key := modelCatalogKey(id)
	if canonical, found := bundledModelCatalog.Aliases[provider+"/"+id]; found {
		key = canonical
	} else if provider == "openrouter" {
		if _, model, found := strings.Cut(id, "/"); found {
			key = modelCatalogKey(model)
		}
	}
	metadata, found := bundledModelCatalog.Models[key]
	return metadata, found
}

// effectiveModelLimits applies explicit deployment limits over bundled metadata.
func effectiveModelLimits(config ProviderConfig) ModelLimits {
	metadata, _ := lookupModel(config.ProviderID, config.Model)
	limits := metadata.Limits
	if custom := config.ModelLimits; custom != nil {
		if custom.ContextWindowTokens != 0 {
			limits.ContextWindowTokens = custom.ContextWindowTokens
		}
		if custom.MaxInputTokens != 0 {
			limits.MaxInputTokens = custom.MaxInputTokens
		}
		if custom.MaxOutputTokens != 0 {
			limits.MaxOutputTokens = custom.MaxOutputTokens
		}
	}
	return limits
}

// validateModelBudgets reports each invalid field separately and checks known model output capacities.
func validateModelBudgets(config ProviderConfig) error {
	if config.MaxTokens == 0 || config.MaxTokens > maxOutputBudget {
		return fmt.Errorf("Output budget must be 1–%d tokens (application resource limit)", maxOutputBudget)
	}
	if config.ContextChars == 0 || config.ContextChars > maxContextCharacters {
		return fmt.Errorf("Context character budget must be 1–%d characters (application resource limit)", maxContextCharacters)
	}
	if config.TimeoutSeconds < 10 || config.TimeoutSeconds > 3600 {
		return errors.New("Timeout must be 10–3,600 seconds")
	}
	limits := effectiveModelLimits(config)
	if limits.MaxOutputTokens != 0 && config.MaxTokens > limits.MaxOutputTokens {
		return fmt.Errorf("Output budget exceeds this model's %d-token output limit", limits.MaxOutputTokens)
	}
	if limits.ContextWindowTokens != 0 && uint64(config.MaxTokens) > limits.ContextWindowTokens {
		return errors.New("Output budget exceeds the model context window")
	}
	return nil
}

// providerCatalog supplies the same limits and capability data used by backend validation.
func providerCatalog() map[string]any {
	return map[string]any{
		"checked_at": ProviderDocsDate, "presets": providerPresets(),
		"model_metadata": bundledModelCatalog.Models, "model_aliases": bundledModelCatalog.Aliases,
		"catalog_source": bundledModelCatalog.Source, "catalog_version": bundledModelCatalog.Version,
		"resource_limits": map[string]any{"max_output_tokens": maxOutputBudget, "max_context_chars": maxContextCharacters},
	}
}
