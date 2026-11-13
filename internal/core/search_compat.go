package core

import (
	"database/sql"
	"errors"
	websearch "quillweave/internal/search"
	"strings"
)

// refreshSearchCompatLocked projects the one search aggregate into the legacy six-provider view.
func (engine *Engine) refreshSearchCompatLocked() error {
	if err := engine.refreshModelsLocked(); err != nil {
		return err
	}
	if err := engine.refreshSearchCredentialsLocked(engine.store.db); err != nil {
		return err
	}
	raw, err := engine.store.IntegrationSettings()
	if err != nil {
		return err
	}
	settings, revision, err := readSearchSettings(engine.store.db)
	if err != nil {
		return err
	}
	view := raw.WithoutSecrets()
	view.SearchRevision = revision
	view.SearchEnabled = settings.Enabled
	view.Providers = []SearchProvider{}
	view.MaxSearches = 2
	for _, profile := range settings.Profiles {
		if profile.LegacyID == "" {
			continue
		}
		key, _, _ := engine.searchCredentialLocked(profile, engine.models)
		view.Providers = append(view.Providers, SearchProvider{ID: profile.LegacyID, Kind: profile.Family, Name: profile.Name, Endpoint: profile.Endpoint, Enabled: profile.Enabled, APIKey: key})
		if len(view.Providers) == 1 {
			view.MaxSearches = profile.Limits.MaxSearches
		}
	}
	if profile, err := settings.Find(settings.DefaultProfileID); err == nil {
		view.MaxSearches = profile.Limits.MaxSearches
	}
	engine.integrations = view
	return nil
}

// legacySearchMetadata compares the public fields of the retained legacy search view.
func legacySearchMetadata(settings IntegrationSettings) string {
	return jsonText(struct {
		Providers []SearchProvider
		Enabled   bool
		Max       int
	}{settings.WithoutSecrets().Providers, settings.SearchEnabled, settings.MaxSearches})
}

// saveSearchIntegrations translates old saves transactionally without restoring the retired provider column.
func (engine *Engine) saveSearchIntegrations(input IntegrationSettings, updates map[string]SecretUpdate) (any, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return nil, errors.New("The application is closing")
	}
	if err := engine.refreshSearchCompatLocked(); err != nil {
		return nil, err
	}
	currentView := engine.integrations
	if input.Revision != currentView.Revision || input.SearchRevision != currentView.SearchRevision {
		return nil, websearch.Fail("SEARCH_REVISION_CONFLICT", "Integration settings changed. Reload before saving", 409)
	}
	if input.MaxSearches < 1 || input.MaxSearches > 5 || len(input.Providers) > 20 {
		return nil, errors.New("Search budgets must be between 1 and 5")
	}
	var nextKeys map[string]searchSecret
	changed := map[string]bool{}
	err := engine.store.withWriteTransaction(func(db *sql.Conn) error {
		raw, err := readIntegrations(db)
		if err != nil {
			return err
		}
		settings, revision, err := readSearchSettings(db)
		if err != nil {
			return err
		}
		if raw.Revision != input.Revision || revision != input.SearchRevision {
			return websearch.Fail("SEARCH_REVISION_CONFLICT", "Integration settings changed. Reload before saving", 409)
		}
		nextKeys, err = engine.store.readCredentials(db, "search")
		if err != nil {
			return err
		}
		metadataChanged := legacySearchMetadata(input) != legacySearchMetadata(currentView)
		if metadataChanged {
			preserved := []websearch.Profile{}
			old := map[string]websearch.Profile{}
			for _, profile := range settings.Profiles {
				if profile.LegacyID == "" {
					preserved = append(preserved, profile)
				} else {
					old[profile.LegacyID] = profile
				}
			}
			legacy := []websearch.Profile{}
			enabled := []string{}
			seen := map[string]bool{}
			for _, provider := range input.Providers {
				if seen[provider.ID] || provider.ID == "" || !contains([]string{"tavily", "bocha", "exa", "serper", "searxng", "wikipedia"}, provider.Kind) {
					return errors.New("Invalid or duplicate search provider")
				}
				seen[provider.ID] = true
				profile, exists := old[provider.ID]
				if !exists {
					profile = websearch.DefaultProfile(provider.Kind)
					profile.ID = "legacy:" + provider.ID
					profile.LegacyID = provider.ID
				}
				previousIdentity := profile.Identity()
				profile.Family = provider.Kind
				profile.Mode = "search"
				profile.Endpoint = provider.Endpoint
				profile.Name = provider.Name
				if strings.TrimSpace(profile.Name) == "" {
					profile.Name = provider.ID
				}
				profile.Enabled = provider.Enabled
				profile.Limits.MaxSearches = input.MaxSearches
				if provider.Kind == "bocha" {
					profile.Region = "cn"
				} else {
					profile.Region = "global"
				}
				if profile.Identity() != previousIdentity {
					oldKey := nextKeys[profile.ID]
					nextKeys[profile.ID] = searchSecret{Identity: profile.Identity(), Generation: oldKey.Generation + 1}
					changed[profile.ID] = true
				}
				legacy = append(legacy, profile)
				if profile.Enabled {
					enabled = append(enabled, profile.ID)
				}
			}
			oldDefault, _ := settings.Find(settings.DefaultProfileID)
			settings.Profiles = append(legacy, preserved...)
			settings.Enabled = input.SearchEnabled
			if oldDefault.LegacyID != "" || settings.DefaultProfileID == "" {
				settings.DefaultProfileID = ""
				settings.FallbackProfileIDs = []string{}
				settings.FallbackPolicy = "legacy"
				if len(enabled) > 0 {
					settings.DefaultProfileID = enabled[0]
					settings.FallbackProfileIDs = enabled[1:min(len(enabled), 5)]
				}
			} else {
				for index := range settings.Profiles {
					if settings.Profiles[index].ID == settings.DefaultProfileID {
						settings.Profiles[index].Limits.MaxSearches = input.MaxSearches
					}
				}
			}
		}
		for id, update := range updates {
			if id == "hindsight" {
				continue
			}
			var profile *websearch.Profile
			for i := range settings.Profiles {
				if settings.Profiles[i].LegacyID == id {
					profile = &settings.Profiles[i]
					break
				}
			}
			if profile == nil {
				return errors.New("Key update references a missing search provider")
			}
			previous := nextKeys[profile.ID]
			switch update.Action {
			case "replace":
				if strings.TrimSpace(update.Value) == "" {
					return errors.New("A replacement key cannot be blank")
				}
				nextKeys[profile.ID] = searchSecret{Value: strings.TrimSpace(update.Value), Identity: profile.Identity(), Generation: previous.Generation + 1}
				changed[profile.ID] = true
			case "clear":
				nextKeys[profile.ID] = searchSecret{Identity: profile.Identity(), Generation: previous.Generation + 1}
				changed[profile.ID] = true
			case "preserve":
			default:
				return errors.New("Invalid credential operation")
			}
		}
		if err := settings.Validate(); err != nil {
			return err
		}
		if metadataChanged || len(changed) > 0 {
			if _, err := writeSearchSettings(db, settings, revision); err != nil {
				return err
			}
		}
		raw.Revision++
		raw.SearchRevision = 0
		raw.Providers = []SearchProvider{}
		raw.SearchEnabled = false
		raw.HindsightURL = ""
		raw.HindsightKey = ""
		if err := engine.store.saveSearchCredentials(db, settings, nextKeys); err != nil {
			return err
		}
		_, err = db.ExecContext(background(), "UPDATE integrations SET document=? WHERE id=1", jsonText(raw))
		return err
	})
	if err != nil {
		return nil, err
	}
	engine.cancelSearchProfiles(changed)
	if err := engine.refreshSearchCompatLocked(); err != nil {
		return nil, err
	}
	return integrationView(engine.integrations), nil
}
