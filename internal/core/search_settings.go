package core

import (
	"database/sql"
	"errors"
	"fmt"
	websearch "quillweave/internal/search"
	"sort"
	"strings"
)

type searchSettingsView struct {
	Revision          uint64             `json:"revision"`
	ModelRevision     uint64             `json:"model_revision"`
	Settings          websearch.Settings `json:"settings"`
	KeyStatus         map[string]bool    `json:"key_status"`
	Models            []websearch.Model  `json:"models"`
	CapabilityVersion string             `json:"capability_version"`
}
type searchMutation struct {
	ExpectedRevision uint64            `json:"expected_revision"`
	ModelRevision    uint64            `json:"model_revision"`
	Operation        string            `json:"operation"`
	Profile          websearch.Profile `json:"profile"`
	ProfileID        string            `json:"profile_id"`
	Policy           *struct {
		Enabled            bool     `json:"enabled"`
		DefaultProfileID   string   `json:"default_profile_id"`
		FallbackPolicy     string   `json:"fallback_policy"`
		FallbackProfileIDs []string `json:"fallback_profile_ids"`
	} `json:"policy"`
	ModelProfileID string                  `json:"model_profile_id"`
	Binding        websearch.Selection     `json:"binding"`
	KeyUpdates     map[string]SecretUpdate `json:"key_updates"`
	UILanguage     string                  `json:"ui_language"`
}

// searchModels exposes only public connection metadata, retaining keys exclusively in the Engine cache.
func searchModels(models ModelSettings) []websearch.Model {
	result := []websearch.Model{}
	for _, profile := range models.Profiles {
		result = append(result, websearch.Model{ID: profile.ID, Name: profile.Name, Family: websearch.ModelFamily(profile.Config.ProviderID, profile.Config.BaseURL), Protocol: profile.Config.Protocol, BaseURL: profile.Config.BaseURL, NameID: profile.Config.Model, Enabled: profile.Enabled, MaxTokens: int(profile.Config.MaxTokens)})
	}
	return result
}

// validateSearchModelRefs checks logical references in the same transaction as metadata commits.
func validateSearchModelRefs(settings websearch.Settings, models ModelSettings) error {
	for modelID := range settings.Bindings {
		if _, err := models.profile(modelID); err != nil {
			return websearch.Fail("SEARCH_PROFILE_UNAVAILABLE", "A search binding references a missing model connection", 422)
		}
	}
	for _, profile := range settings.Profiles {
		if profile.ModelProfileID == "" {
			continue
		}
		model, err := models.profile(profile.ModelProfileID)
		if err != nil {
			return websearch.Fail("SEARCH_PROFILE_UNAVAILABLE", "The research model connection no longer exists", 422)
		}
		family := websearch.ModelFamily(model.Config.ProviderID, model.Config.BaseURL)
		if family != "" && family != profile.Family {
			return websearch.Fail("SEARCH_CAPABILITY_UNSUPPORTED", "Choose a model connection from the same search service", 422)
		}
		if profile.CredentialSource == "model_ref" && !sameSearchBase(profile.Endpoint, model.Config.BaseURL) {
			return websearch.Fail("SEARCH_CREDENTIAL_DESTINATION", "Reused credentials require the same service endpoint and account region", 422)
		}
	}
	return nil
}

// sameSearchBase compares normalized API prefixes rather than accepting a matching hostname alone.
func sameSearchBase(first, second string) bool {
	a, errA := websearch.ModelEndpoint(first, "responses")
	b, errB := websearch.ModelEndpoint(second, "responses")
	return errA == nil && errB == nil && strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// currentSearchView reads model and search metadata from one snapshot before reporting process-local presence.
func (engine *Engine) currentSearchView() (searchSettingsView, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return engine.searchViewLocked()
}

// searchViewLocked builds a redacted settings view; the caller holds Engine.mu.
func (engine *Engine) searchViewLocked() (searchSettingsView, error) {
	if engine.closed {
		return searchSettingsView{}, errors.New("The application is closing")
	}
	var settings websearch.Settings
	var revision uint64
	var models ModelSettings
	err := engine.store.withReadSnapshot(func(db documentDB) error {
		var err error
		settings, revision, err = readSearchSettings(db)
		if err != nil {
			return err
		}
		models, err = readModelSettings(db)
		return err
	})
	if err != nil {
		return searchSettingsView{}, err
	}
	for index := range models.Profiles {
		models.Profiles[index].Config.APIKey = engine.models.keyFor(models.Profiles[index])
	}
	engine.models = models
	keys := map[string]bool{}
	for _, profile := range settings.Profiles {
		key, _, err := engine.searchCredentialLocked(profile, models)
		keys[profile.ID] = err == nil && key != ""
	}
	return searchSettingsView{Revision: revision, ModelRevision: models.Revision, Settings: settings, KeyStatus: keys, Models: searchModels(models), CapabilityVersion: websearch.CapabilityVersion}, nil
}

// searchCredentialLocked resolves a live secret under its destination identity; it never serializes the value.
func (engine *Engine) searchCredentialLocked(profile websearch.Profile, models ModelSettings) (string, uint64, error) {
	if engine.searchKeys == nil {
		engine.searchKeys = map[string]searchSecret{}
	}
	if profile.CredentialSource == "model_ref" {
		model, err := models.profile(profile.ModelProfileID)
		if err != nil || !model.Enabled || !sameSearchBase(profile.Endpoint, model.Config.BaseURL) {
			return "", 0, websearch.Fail("SEARCH_CREDENTIAL_REQUIRED", "The referenced model credential is unavailable or has another destination", 422)
		}
		key := model.Config.APIKey
		identity := profile.Identity() + "|" + model.Config.Protocol + "|" + model.Config.BaseURL
		id := "model-ref:" + profile.ID
		previous := engine.searchKeys[id]
		if previous.Value != key || previous.Identity != identity {
			previous = searchSecret{Value: key, Identity: identity, Generation: previous.Generation + 1}
			engine.searchKeys[id] = previous
		}
		return key, previous.Generation, nil
	}
	current := engine.searchKeys[profile.ID]
	if current.Identity != profile.Identity() {
		return "", current.Generation, nil
	}
	return current.Value, current.Generation, nil
}

// mutateSearchSettings atomically validates and commits one explicit metadata operation and separate secret intent.
func (engine *Engine) mutateSearchSettings(mutation searchMutation) (searchSettingsView, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return searchSettingsView{}, errors.New("The application is closing")
	}
	nextKeys := map[string]searchSecret{}
	for id, key := range engine.searchKeys {
		nextKeys[id] = key
	}
	changed := map[string]bool{}
	err := engine.store.withWriteTransaction(func(db *sql.Conn) error {
		settings, revision, err := readSearchSettings(db)
		if err != nil {
			return err
		}
		if revision != mutation.ExpectedRevision {
			return websearch.Fail("SEARCH_REVISION_CONFLICT", "Search settings changed. Reload before saving; your edits are retained", 409)
		}
		models, err := readModelSettings(db)
		if err != nil {
			return err
		}
		if models.Revision != mutation.ModelRevision {
			return websearch.Fail("SEARCH_REVISION_CONFLICT", "Model connections changed. Reload before saving; your edits are retained", 409)
		}
		switch mutation.Operation {
		case "upsert_profile":
			profile := mutation.Profile
			profile.Name = strings.TrimSpace(profile.Name)
			index := -1
			for i, item := range settings.Profiles {
				if item.ID == profile.ID {
					index = i
					break
				}
			}
			if index < 0 && profile.LegacyID != "" {
				return websearch.Fail("SEARCH_INVALID_ARGUMENT", "Legacy profile identifiers are server-managed", 400)
			}
			if index >= 0 {
				previous := settings.Profiles[index]
				if previous.Enabled && !profile.Enabled {
					changed[profile.ID] = true
				}
				if profile.LegacyID != previous.LegacyID {
					return websearch.Fail("SEARCH_INVALID_ARGUMENT", "Legacy identifiers cannot be changed", 400)
				}
				if previous.LegacyID != "" && previous.Family != profile.Family {
					return websearch.Fail("SEARCH_INVALID_ARGUMENT", "Create a new profile to change a legacy service type", 400)
				}
				if profile.Identity() != previous.Identity() {
					changed[profile.ID] = true
					old := nextKeys[profile.ID]
					nextKeys[profile.ID] = searchSecret{Identity: profile.Identity(), Generation: old.Generation + 1}
				}
				settings.Profiles[index] = profile
			} else {
				settings.Profiles = append(settings.Profiles, profile)
			}
		case "delete_profile":
			if settings.DefaultProfileID == mutation.ProfileID || contains(settings.FallbackProfileIDs, mutation.ProfileID) {
				return websearch.Fail("SEARCH_REFERENCE_IN_USE", "This search profile is still the default or a fallback", 409)
			}
			for _, binding := range settings.Bindings {
				if binding.ProfileID == mutation.ProfileID {
					return websearch.Fail("SEARCH_REFERENCE_IN_USE", "A model still references this search profile", 409)
				}
			}
			var active int
			if err := db.QueryRowContext(background(), "SELECT COUNT(*) FROM search_runs r WHERE status IN ('queued','running') AND (profile_id=? OR EXISTS(SELECT 1 FROM json_each(r.document,'$.effective_profile.fallbacks') f WHERE json_extract(f.value,'$.id')=?))", mutation.ProfileID, mutation.ProfileID).Scan(&active); err != nil {
				return err
			}
			if active > 0 {
				return websearch.Fail("SEARCH_REFERENCE_IN_USE", "Cancel the active search before deleting its profile", 409)
			}
			index := -1
			for i, item := range settings.Profiles {
				if item.ID == mutation.ProfileID {
					index = i
					break
				}
			}
			if index < 0 {
				return websearch.Fail("SEARCH_PROFILE_UNAVAILABLE", "Search profile no longer exists", 422)
			}
			settings.Profiles = append(settings.Profiles[:index], settings.Profiles[index+1:]...)
			delete(nextKeys, mutation.ProfileID)
			delete(nextKeys, "model-ref:"+mutation.ProfileID)
			changed[mutation.ProfileID] = true
		case "set_policy":
			if mutation.Policy == nil {
				return websearch.Fail("SEARCH_INVALID_ARGUMENT", "Missing search policy", 400)
			}
			settings.Enabled = mutation.Policy.Enabled
			settings.DefaultProfileID = mutation.Policy.DefaultProfileID
			settings.FallbackPolicy = mutation.Policy.FallbackPolicy
			settings.FallbackProfileIDs = mutation.Policy.FallbackProfileIDs
		case "set_binding":
			if _, err := models.profile(mutation.ModelProfileID); err != nil {
				return err
			}
			if err := websearch.ValidateSelection(mutation.Binding); err != nil {
				return err
			}
			if settings.Bindings == nil {
				settings.Bindings = map[string]websearch.Selection{}
			}
			if mutation.Binding.Mode == "inherit" {
				delete(settings.Bindings, mutation.ModelProfileID)
			} else {
				settings.Bindings[mutation.ModelProfileID] = mutation.Binding
			}
		default:
			return websearch.Fail("SEARCH_INVALID_ARGUMENT", "Unsupported search settings operation", 400)
		}
		if err := settings.Validate(); err != nil {
			return err
		}
		if err := validateSearchModelRefs(settings, models); err != nil {
			return err
		}
		for id, update := range mutation.KeyUpdates {
			if mutation.Operation != "upsert_profile" || id != mutation.Profile.ID {
				return websearch.Fail("SEARCH_INVALID_ARGUMENT", "Key updates must belong to the edited search profile", 400)
			}
			profile, err := settings.Find(id)
			if err != nil {
				return err
			}
			if profile.CredentialSource == "model_ref" && update.Action == "replace" {
				return websearch.Fail("SEARCH_INVALID_ARGUMENT", "A referenced model key cannot be replaced through search settings", 400)
			}
			old := nextKeys[id]
			switch update.Action {
			case "replace":
				if strings.TrimSpace(update.Value) == "" {
					return websearch.Fail("SEARCH_INVALID_ARGUMENT", "A replacement key cannot be blank; use clear", 400)
				}
				nextKeys[id] = searchSecret{Value: strings.TrimSpace(update.Value), Identity: profile.Identity(), Generation: old.Generation + 1}
				changed[id] = true
			case "clear":
				nextKeys[id] = searchSecret{Identity: profile.Identity(), Generation: old.Generation + 1}
				changed[id] = true
			case "preserve":
			default:
				return websearch.Fail("SEARCH_INVALID_ARGUMENT", "Invalid key update operation", 400)
			}
		}
		_, err = writeSearchSettings(db, settings, revision)
		return err
	})
	if err != nil {
		return searchSettingsView{}, err
	}
	engine.searchKeys = nextKeys
	engine.cancelSearchProfiles(changed)
	return engine.searchViewLocked()
}

// disableSearch changes only the kill switch and is idempotent across stale forms.
func (engine *Engine) disableSearch() (searchSettingsView, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	err := engine.store.withWriteTransaction(func(db *sql.Conn) error {
		settings, revision, err := readSearchSettings(db)
		if err != nil {
			return err
		}
		if !settings.Enabled {
			return nil
		}
		settings.Enabled = false
		_, err = writeSearchSettings(db, settings, revision)
		return err
	})
	if err != nil {
		return searchSettingsView{}, err
	}
	engine.cancelSearchProfiles(nil)
	return engine.searchViewLocked()
}

// searchProfilePage provides bounded, stable pagination instead of sending an unbounded settings table.
func (engine *Engine) searchProfilePage(page, size int, query, family string) (any, error) {
	if page < 1 || page > 1_000_000 || (size != 5 && size != 10 && size != 20 && size != 50) || charCount(query) > 80 {
		return nil, websearch.Fail("SEARCH_INVALID_ARGUMENT", "Invalid search pagination or filter", 400)
	}
	view, err := engine.currentSearchView()
	if err != nil {
		return nil, err
	}
	rows := []websearch.Profile{}
	for _, profile := range view.Settings.Profiles {
		if (family == "" || family == "all" || profile.Family == family) && strings.Contains(strings.ToLower(profile.Name), strings.ToLower(query)) {
			rows = append(rows, profile)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name == rows[j].Name {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Name < rows[j].Name
	})
	total := len(rows)
	start := min(total, (page-1)*size)
	end := min(total, start+size)
	return map[string]any{"rows": rows[start:end], "total": total, "pageNo": page, "pageSize": size, "revision": view.Revision, "key_status": view.KeyStatus}, nil
}

// validateSearchModelChanges prevents existing model deletion/disable operations from stranding search references.
func validateSearchModelChanges(db documentDB, next ModelSettings) error {
	settings, _, err := readSearchSettings(db)
	if err != nil {
		return err
	}
	for modelID := range settings.Bindings {
		if _, err := next.profile(modelID); err != nil {
			return fmt.Errorf("A search binding still references model %s", modelID)
		}
	}
	for _, profile := range settings.Profiles {
		if profile.ModelProfileID == "" {
			continue
		}
		model, err := next.profile(profile.ModelProfileID)
		if err != nil || (!model.Enabled && profile.Enabled) {
			return websearch.Fail("SEARCH_REFERENCE_IN_USE", "A search configuration still references this model connection", 409)
		}
	}
	return validateSearchModelRefs(settings, next)
}

// invalidateSearchModelsLocked revokes active use and historical test validity after a linked model edit; Engine.mu is held.
func (engine *Engine) invalidateSearchModelsLocked(previous, next ModelSettings, updates map[string]SecretUpdate) {
	settings, _, err := readSearchSettings(engine.store.db)
	if err != nil {
		engine.cancelSearchProfiles(nil)
		return
	}
	changed := map[string]bool{}
	for _, profile := range settings.Profiles {
		if profile.ModelProfileID == "" {
			continue
		}
		before, oldErr := previous.profile(profile.ModelProfileID)
		after, newErr := next.profile(profile.ModelProfileID)
		update := updates[profile.ModelProfileID]
		if oldErr == nil && newErr == nil && jsonText(before) == jsonText(after) && update.Action != "replace" && update.Action != "clear" {
			continue
		}
		changed[profile.ID] = true
		if profile.CredentialSource == "model_ref" {
			id := "model-ref:" + profile.ID
			old := engine.searchKeys[id]
			identity := profile.Identity() + "|" + after.Config.Protocol + "|" + after.Config.BaseURL
			engine.searchKeys[id] = searchSecret{Value: after.Config.APIKey, Identity: identity, Generation: old.Generation + 1}
		}
	}
	engine.cancelSearchProfiles(changed)
}
