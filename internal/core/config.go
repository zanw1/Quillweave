package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

var Tasks = []string{"ask", "continue", "rewrite", "architecture", "outline", "review", "wiki_extract", "wiki_compile", "writing_composition", "writing_review"}

type SecretUpdate struct {
	Action string `json:"action"`
	Value  string `json:"value"`
}
type SelectedModel struct {
	ProfileID   string
	ProfileName string
	Config      ProviderConfig
}

// UnmarshalJSON accepts explicit key operations and requires a replacement value.
func (update *SecretUpdate) UnmarshalJSON(data []byte) error {
	var value struct {
		Action string `json:"action"`
	}
	if err := decodeRecord(data, &value, "action"); err != nil {
		return err
	}
	if !contains([]string{"preserve", "clear", "replace"}, value.Action) {
		return errors.New("Invalid API key operation")
	}
	if value.Action == "replace" {
		type replacement SecretUpdate
		var next replacement
		if err := decodeRecord(data, &next, "action", "value"); err != nil {
			return err
		}
		*update = SecretUpdate(next)
		return nil
	}
	*update = SecretUpdate{Action: value.Action}
	return nil
}

// modelsFromLegacy upgrades legacy model parameters to a stable default profile.
func modelsFromLegacy(config ProviderConfig) ModelSettings {
	enabled := strings.TrimSpace(config.Model) != ""
	fallback := ""
	if enabled {
		fallback = "default"
	}
	return ModelSettings{SchemaVersion: 2, Profiles: []ModelProfile{{ID: "default", Name: "Legacy default profile", Enabled: enabled, Config: config}}, DefaultProfile: fallback, Routes: map[string]string{}}
}

// defaultModels provides model settings for offline writing.
func defaultModels() ModelSettings {
	models := modelsFromLegacy(defaultProvider())
	models.Profiles[0].Name = "Default profile"
	return models
}

// parseModels reads both historical formats and reports corrupted configurations.
func parseModels(raw string) (ModelSettings, error) {
	var value map[string]json.RawMessage
	if err := decodeJSON([]byte(raw), &value); err != nil {
		return ModelSettings{}, errors.New("The model configuration is corrupted")
	}
	if _, exists := value["profiles"]; exists {
		var models ModelSettings
		if err := decodeJSON([]byte(raw), &models); err != nil {
			return models, errors.New("The model configuration is incomplete")
		}
		if models.SchemaVersion != 2 {
			return models, errors.New("Unsupported model configuration version")
		}
		return models, nil
	}
	var config ProviderConfig
	if err := decodeJSON([]byte(raw), &config); err != nil {
		return ModelSettings{}, errors.New("The legacy model configuration is incomplete")
	}
	return modelsFromLegacy(config), nil
}

// WithoutSecrets copies profiles and routes while clearing credentials in the serialized view.
func (models ModelSettings) WithoutSecrets() ModelSettings {
	result := models
	result.Profiles = append([]ModelProfile{}, models.Profiles...)
	result.Routes = map[string]string{}
	for key, value := range models.Routes {
		result.Routes[key] = value
	}
	for i := range result.Profiles {
		result.Profiles[i].Config.APIKey = ""
	}
	return result
}

// profile resolves an enabled profile and reports invalid references.
func (models ModelSettings) profile(id string) (ModelProfile, error) {
	for _, profile := range models.Profiles {
		if profile.ID == id {
			if !profile.Enabled {
				return ModelProfile{}, fmt.Errorf("Model profile \"%s\" is disabled. Select another profile", profile.Name)
			}
			return profile, nil
		}
	}
	return ModelProfile{}, errors.New("No usable model is assigned. Add and select a profile in Model settings")
}

// Validate checks profile limits, unique names and IDs, and task references.
func (models ModelSettings) Validate() error {
	if models.SchemaVersion != 2 || len(models.Profiles) > 40 {
		return errors.New("Invalid model configuration version or more than 40 profiles")
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, profile := range models.Profiles {
		if profile.ID == "" || len(profile.ID) > 120 || ids[profile.ID] {
			return errors.New("Model profile IDs must be nonempty and unique")
		}
		ids[profile.ID] = true
		name := lowerText(strings.TrimSpace(profile.Name))
		if name == "" || charCount(profile.Name) > 80 || names[name] {
			return errors.New("Model profile names must be unique and contain 1–80 characters")
		}
		names[name] = true
		if profile.Enabled {
			if _, err := validateConfig(profile.Config); err != nil {
				return fmt.Errorf("%s：%s", profile.Name, err)
			}
		}
	}
	if models.DefaultProfile != "" {
		if _, err := models.profile(models.DefaultProfile); err != nil {
			return err
		}
	}
	return models.ValidateRoutes(models.Routes)
}

// ValidateRoutes validates task references in key order, with empty values inheriting routing.
func (models ModelSettings) ValidateRoutes(routes map[string]string) error {
	keys := make([]string, 0, len(routes))
	for key := range routes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, task := range keys {
		if !contains(Tasks, task) {
			return fmt.Errorf("Unknown model task: %s", task)
		}
		if routes[task] != "" {
			if _, err := models.profile(routes[task]); err != nil {
				return err
			}
		}
	}
	return nil
}

// Select applies project overrides, task routing, and the default profile in that order.
func (models ModelSettings) Select(project *Project, task string) (SelectedModel, error) {
	if !contains(Tasks, task) {
		return SelectedModel{}, errors.New("Unsupported model task")
	}
	id := project.ModelOverrides[task]
	if id == "" {
		id = models.Routes[task]
	}
	if id == "" {
		id = models.DefaultProfile
	}
	profile, err := models.profile(id)
	if err != nil {
		return SelectedModel{}, err
	}
	if _, err := validateConfig(profile.Config); err != nil {
		return SelectedModel{}, err
	}
	return SelectedModel{ProfileID: profile.ID, ProfileName: profile.Name, Config: profile.Config}, nil
}

// keyFor reuses a loaded credential only for the same profile ID, protocol, and endpoint.
func (models ModelSettings) keyFor(profile ModelProfile) string {
	for _, old := range models.Profiles {
		if old.ID == profile.ID && old.Config.Protocol == profile.Config.Protocol && strings.TrimRight(old.Config.BaseURL, "/") == strings.TrimRight(profile.Config.BaseURL, "/") {
			return old.Config.APIKey
		}
	}
	return ""
}

// updateModels handles preserve, clear, and replace operations and clears keys after endpoint changes.
func updateModels(current, next ModelSettings, updates map[string]SecretUpdate) (ModelSettings, error) {
	if next.Revision != current.Revision {
		return ModelSettings{}, errors.New("Model settings changed. Reload them before saving. Your current edits are retained")
	}
	if err := next.Validate(); err != nil {
		return ModelSettings{}, err
	}
	for id := range updates {
		found := false
		for _, profile := range next.Profiles {
			if profile.ID == id {
				found = true
				break
			}
		}
		if !found {
			return ModelSettings{}, errors.New("The API key operation references a missing model profile")
		}
	}
	for i := range next.Profiles {
		profile := &next.Profiles[i]
		if profile.Config.APIKey != "" {
			return ModelSettings{}, errors.New("Use the explicit replace operation to update an API key")
		}
		update := updates[profile.ID]
		switch update.Action {
		case "clear":
			profile.Config.APIKey = ""
		case "replace":
			if strings.TrimSpace(update.Value) == "" {
				return ModelSettings{}, errors.New("A replacement key cannot be empty. Use the clear operation to remove a key")
			}
			profile.Config.APIKey = strings.TrimSpace(update.Value)
		default:
			profile.Config.APIKey = current.keyFor(*profile)
		}
	}
	if next.Revision == math.MaxUint64 {
		return ModelSettings{}, errors.New("Model settings revision is out of range")
	}
	next.Revision++
	return next, nil
}
