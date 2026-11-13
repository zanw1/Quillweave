//go:build quillweave_whitebox

package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestModelRefreshPreservesMatchingEncryptedSecrets checks shared credential refresh while keys remain scoped to a matching endpoint.
func TestModelRefreshPreservesMatchingEncryptedSecrets(t *testing.T) {
	first, second, project, settings := sharedModelEngines(t)
	var oldCalls, newCalls atomic.Int32
	oldServer := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		oldCalls.Add(1)
		fmt.Fprint(writer, streamPayload("Old endpoint complete"))
	})
	newServer := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		newCalls.Add(1)
		if request.Header.Get("Authorization") != "Bearer new-first-process-key" {
			t.Error("The current endpoint did not receive its saved key")
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		fmt.Fprint(writer, streamPayload("Current endpoint complete"))
	})
	settings.Profiles[0].Config.BaseURL = oldServer.URL + "/v1"
	saved := invoke[settingsResponse](t, first, "save_model_settings", map[string]any{"settings": settings, "key_updates": map[string]SecretUpdate{"writer": {Action: "replace", Value: "first-process-key"}}})
	view := invoke[settingsResponse](t, second, "get_model_settings", map[string]any{})
	assert(t, view.KeyStatus["writer"] && view.Settings.Profiles[0].Config.APIKey == "", "The shared encrypted key was missing or exposed through the API")
	saved = invoke[settingsResponse](t, second, "save_model_settings", map[string]any{"settings": saved.Settings, "key_updates": map[string]SecretUpdate{"writer": {Action: "replace", Value: "second-process-key"}}})
	firstView := invoke[settingsResponse](t, first, "get_model_settings", map[string]any{})
	assert(t, firstView.KeyStatus["writer"] && first.models.Profiles[0].Config.APIKey == "second-process-key", "Refreshing the same endpoint did not load the current encrypted key")
	saved.Settings.Profiles[0].Config.BaseURL = newServer.URL + "/v1"
	saved.Settings.Profiles[0].Config.ContextChars = 4000
	updated := invoke[settingsResponse](t, first, "save_model_settings", map[string]any{"settings": saved.Settings, "key_updates": map[string]SecretUpdate{"writer": {Action: "replace", Value: "new-first-process-key"}}})
	_, err := second.Dispatch("prepare_ai", rawMessage(map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "ask", "instruction": strings.Repeat("x", 5000)}))
	assert(t, err != nil && strings.Contains(err.Error(), "4000"), "Context preview used an outdated budget")
	job := invoke[Job](t, second, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "ask", "instruction": "Continue"})
	done := finishJob(t, second, job)
	assert(t, done.Status == "done" && done.Text == "Current endpoint complete" && oldCalls.Load() == 0 && newCalls.Load() == 1, "Generation used an outdated endpoint")
	updated.Settings.Profiles[0].Config.BaseURL = oldServer.URL + "/v1"
	updated = invoke[settingsResponse](t, first, "save_model_settings", map[string]any{"settings": updated.Settings})
	invoke[map[string]any](t, second, "test_model", map[string]any{"profile_id": "writer"})
	assert(t, oldCalls.Load() == 1, "Connection testing used an outdated endpoint")
	updated.Settings.Profiles[0].Enabled = false
	updated.Settings.DefaultProfile = ""
	invoke[settingsResponse](t, first, "save_model_settings", map[string]any{"settings": updated.Settings})
	reject(t, second, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "ask", "instruction": "Use a disabled profile"})
}

// TestCredentialsAreNotPersistedOrReturned checks that public API and model metadata omit plaintext credentials.
func TestCredentialsAreNotPersistedOrReturned(t *testing.T) {
	engine := testEngine(t)
	config := defaultProvider()
	config.Model = "mock"
	config.APIKey = "test-key-not-real"
	configureModel(t, engine, config)
	view := invoke[settingsResponse](t, engine, "get_model_settings", map[string]any{})
	stored, err := engine.store.ModelSettings()
	assert(t, err == nil && view.KeyStatus["default"] && view.Settings.Profiles[0].Config.APIKey == "" && stored.Profiles[0].Config.APIKey == "", "An API key was persisted or returned to the UI")
}

// TestReadsLegacySingleModelSettings checks real legacy SQLite single-model settings.
func TestReadsLegacySingleModelSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	config := defaultProvider()
	config.Model = "legacy-model"
	config.ContextChars = 33000
	_, err = db.Exec("CREATE TABLE settings(id INTEGER PRIMARY KEY,document TEXT NOT NULL)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO settings VALUES(1,?)", jsonText(config))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	models, err := store.ModelSettings()
	assert(t, err == nil && models.DefaultProfile == "default" && models.Profiles[0].Config.Model == "legacy-model" && models.Profiles[0].Config.ContextChars == 33000, "Legacy model parameters were not retained")
}

// TestTaskRoutingRespectsProjectOverride checks project overrides, global task routing, and default profile precedence.
func TestTaskRoutingRespectsProjectOverride(t *testing.T) {
	models := testSettings()
	project := NewProject("Test", "", "")
	models.Routes["review"] = "reviewer"
	selected, err := models.Select(&project, "review")
	assert(t, err == nil && selected.ProfileID == "reviewer", "Task routing failed")
	selected, err = models.Select(&project, "continue")
	assert(t, err == nil && selected.ProfileID == "writer", "Default routing failed")
	project.ModelOverrides["review"] = "writer"
	selected, err = models.Select(&project, "review")
	assert(t, err == nil && selected.ProfileID == "writer", "Project routing override failed")
}

// TestDisabledOrMissingProfilesDoNotFallBack checks explicit errors for disabled or missing profile references.
func TestDisabledOrMissingProfilesDoNotFallBack(t *testing.T) {
	models := testSettings()
	project := NewProject("Test", "", "")
	project.ModelOverrides["review"] = "missing"
	_, err := models.Select(&project, "review")
	assert(t, err != nil, "A missing reference silently fell back")
	project.ModelOverrides["review"] = "reviewer"
	models.Profiles[1].Enabled = false
	_, err = models.Select(&project, "review")
	assert(t, err != nil, "A disabled model silently fell back")
}

// TestSecretsAreScopedToProfileAndEndpoint checks key isolation between profiles and after endpoint changes.
func TestSecretsAreScopedToProfileAndEndpoint(t *testing.T) {
	current := testSettings()
	current.Profiles[0].Config.APIKey = "writer-test-key"
	current.Profiles[1].Config.APIKey = "review-test-key"
	next := current.WithoutSecrets()
	next.Profiles[0].Config.BaseURL = "https://new.example.org/v1"
	result, err := updateModels(current, next, map[string]SecretUpdate{})
	assert(t, err == nil && result.Profiles[0].Config.APIKey == "" && result.Profiles[1].Config.APIKey == "review-test-key", "Endpoint changes leaked a key")
	updated, err := updateModels(result, result.WithoutSecrets(), map[string]SecretUpdate{"writer": {Action: "replace", Value: "replacement-test-key"}, "reviewer": {Action: "clear"}})
	assert(t, err == nil && updated.Profiles[0].Config.APIKey == "replacement-test-key" && updated.Profiles[1].Config.APIKey == "", "API key operation failed")
}

// TestModelDeleteAndSaveConflictsAreRejected rejects deletion of referenced models and stale settings saves.
func TestModelDeleteAndSaveConflictsAreRejected(t *testing.T) {
	engine := testEngine(t)
	view := invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": testSettings()})
	project := NewProject("Project with a model assignment", "", "")
	project.ModelOverrides["review"] = "reviewer"
	insertProject(t, engine, project)
	proposed := view.Settings
	proposed.Profiles = proposed.Profiles[:1]
	reject(t, engine, "save_model_settings", map[string]any{"settings": proposed})
	reject(t, engine, "save_model_settings", map[string]any{"settings": testSettings()})
	current := invoke[settingsResponse](t, engine, "get_model_settings", map[string]any{})
	assert(t, len(current.Settings.Profiles) == 2, "A failed save changed the configuration")
}

// TestActualRequestsUseEachAssignedModel checks model IDs, bearer keys, and prompts at three assigned endpoints.
func TestActualRequestsUseEachAssignedModel(t *testing.T) {
	engine := testEngine(t)
	project := NewProject("Multi-model acceptance", "Mystery", "")
	project.WritingStyle = "mystery"
	chapter := NewChapter(1, "The Letter")
	chapter.Body = "Lin Wu read the letter and decided to visit the lighthouse alone."
	target := uint32(2100)
	chapter.TargetWords = &target
	project.Chapters = append(project.Chapters, chapter)
	insertProject(t, engine, project)
	var mutex sync.Mutex
	calls := map[string]int{}
	writerPrompt := ""
	models := testSettings()
	models.Profiles = []ModelProfile{}
	updates := map[string]SecretUpdate{}
	for _, stage := range []string{"writer", "extract", "compile"} {
		stage := stage
		server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
			var body struct {
				Model    string    `json:"model"`
				Messages []Message `json:"messages"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				writer.WriteHeader(400)
				return
			}
			if body.Model != stage+"-model" || request.Header.Get("Authorization") != "Bearer "+stage+"-test-key" {
				t.Errorf("Incorrect model or key routing: %s", stage)
				writer.WriteHeader(400)
				return
			}
			mutex.Lock()
			calls[stage]++
			if stage == "writer" {
				writerPrompt = jsonText(body.Messages)
			}
			mutex.Unlock()
			text := "She followed the shore toward the lighthouse."
			if stage == "writer" && strings.Contains(jsonText(body.Messages), "[Writing composition stage]") {
				text = writingCompositionFixture()
			}
			if stage == "extract" {
				text = jsonText(map[string]any{"facts": []Candidate{{Entity: "Lin Wu", Kind: "event", Claim: "Decides to visit the lighthouse", Quote: chapter.Body, Epistemic: "explicit"}}})
			}
			if stage == "compile" {
				input := body.Messages[1].Content
				start := strings.Index(input, "<evidence>\n")
				end := strings.Index(input, "\n</evidence>")
				if start < 0 || end < 0 {
					t.Error("The evidence prompt was missing")
					writer.WriteHeader(400)
					return
				}
				var evidence []map[string]any
				if err := json.Unmarshal([]byte(input[start+len("<evidence>\n"):end]), &evidence); err != nil || len(evidence) == 0 {
					t.Error("Invalid evidence JSON")
					writer.WriteHeader(400)
					return
				}
				text = jsonText(map[string]any{"summary": "Lin Wu chose a course of action after reading the letter.", "evidence_ids": []any{evidence[0]["id"]}, "questions": []string{}})
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, streamPayload(text))
		})
		profile := testProfile(stage)
		profile.Config.BaseURL = server.URL + "/v1"
		models.Profiles = append(models.Profiles, profile)
		updates[stage] = SecretUpdate{Action: "replace", Value: stage + "-test-key"}
	}
	models.Routes["wiki_extract"] = "extract"
	models.Routes["wiki_compile"] = "compile"
	invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": models, "key_updates": updates})
	for _, action := range []string{"continue", "wiki"} {
		job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": chapter.ID, "action": action, "instruction": "Continue writing"})
		done := finishJob(t, engine, job)
		assert(t, done.Status == "done", "Multi-model task failed: "+done.Error)
	}
	mutex.Lock()
	defer mutex.Unlock()
	assert(t, calls["writer"] == 3 && calls["extract"] == 1 && calls["compile"] == 1, "Composition, writing and review did not use the same assigned model")
	assert(t, strings.Contains(writerPrompt, "2100") && strings.Contains(writerPrompt, "Mystery"), "Writing parameters were absent from the request")
	view := invoke[settingsResponse](t, engine, "get_model_settings", map[string]any{})
	assert(t, !strings.Contains(jsonText(view), "test-key"), "An API key was returned to the UI")
	latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(latest.Compilations) == 1, "Memory was not saved")
}

// TestSecretUpdatesIgnoreUnusedValue checks that preserve and clear ignore unused replacement values.
func TestSecretUpdatesIgnoreUnusedValue(t *testing.T) {
	current := testSettings()
	current.Profiles[0].Config.APIKey = "synthetic-key"
	for _, action := range []string{"preserve", "clear"} {
		var update SecretUpdate
		if err := decodeJSON([]byte(`{"action":"`+action+`","value":null}`), &update); err != nil {
			t.Fatal(err)
		}
		next, err := updateModels(current, current.WithoutSecrets(), map[string]SecretUpdate{"writer": update})
		if err != nil {
			t.Fatal(err)
		}
		expected := "synthetic-key"
		if action == "clear" {
			expected = ""
		}
		assert(t, next.Profiles[0].Config.APIKey == expected, "Extra fields changed the API key operation")
	}
	var replacement SecretUpdate
	assert(t, decodeJSON([]byte(`{"action":"replace","value":null}`), &replacement) != nil, "A replacement key accepted null")
}

// TestModelSettingsReloadAcrossEngines checks that reopening conflicted settings reads current metadata and permits retry.
func TestModelSettingsReloadAcrossEngines(t *testing.T) {
	first, second, _, previous := sharedModelEngines(t)
	changed := previous.WithoutSecrets()
	changed.Profiles[0].Name = "Updated profile name"
	updated := invoke[settingsResponse](t, first, "save_model_settings", map[string]any{"settings": changed})
	reject(t, second, "save_model_settings", map[string]any{"settings": previous})
	opened := invoke[settingsResponse](t, second, "get_model_settings", map[string]any{})
	assert(t, opened.Settings.Revision == updated.Settings.Revision && opened.Settings.Profiles[0].Name == "Updated profile name", "Reopening settings returned stale cached data")
	opened.Settings.Profiles[0].Name = "Profile after retry"
	saved := invoke[settingsResponse](t, second, "save_model_settings", map[string]any{"settings": opened.Settings})
	latest := invoke[settingsResponse](t, first, "get_model_settings", map[string]any{})
	assert(t, saved.Settings.Revision == updated.Settings.Revision+1 && latest.Settings.Profiles[0].Name == "Profile after retry", "Saving failed after conflict recovery")
}

// TestDeletedModelCannotBeBoundAcrossEngines checks deletion followed by assignment and verifies rollback leaves later writes usable.
func TestDeletedModelCannotBeBoundAcrossEngines(t *testing.T) {
	first, second, project, settings := sharedModelEngines(t)
	settings.Profiles = settings.Profiles[:1]
	current := invoke[settingsResponse](t, first, "save_model_settings", map[string]any{"settings": settings})
	result, err := second.Dispatch("set_project_models", rawMessage(map[string]any{"project_id": project.ID, "revision": 0, "overrides": map[string]string{"ask": "reviewer"}}))
	assert(t, result == nil && err != nil, "A rejected assignment returned an uncommitted project")
	unchanged := invoke[Project](t, second, "get_project", map[string]any{"project_id": project.ID})
	assert(t, unchanged.Revision == 0 && len(unchanged.ModelOverrides) == 0, "The rejected operation changed the project")
	bound := invoke[Project](t, second, "set_project_models", map[string]any{"project_id": project.ID, "revision": 0, "overrides": map[string]string{"ask": "writer"}})
	assert(t, bound.Revision == 1 && bound.ModelOverrides["ask"] == "writer", "A valid assignment failed after rollback")
	current.Settings.Profiles[0].Name = "The transaction remains usable"
	invoke[settingsResponse](t, first, "save_model_settings", map[string]any{"settings": current.Settings})
	// Cross-environment backups retain their references for later explicit reassignment.
	archive := cloneProject(project)
	archive.ModelOverrides = map[string]string{"ask": "another-environment"}
	imported := invoke[Project](t, first, "import_archive", map[string]any{"content": jsonText(map[string]any{"format": "quillweave-1", "project": archive})})
	assert(t, imported.ModelOverrides["ask"] == "another-environment", "Backup import rewrote model references")
	invoke[Project](t, first, "set_project_models", map[string]any{"project_id": imported.ID, "revision": 0, "overrides": map[string]string{}})
}

// TestModelRemovalAndBindingAreAtomic checks concurrent deletion or disabling against assignment without dangling references.
func TestModelRemovalAndBindingAreAtomic(t *testing.T) {
	for _, disable := range []bool{false, true} {
		for attempt := 0; attempt < 8; attempt++ {
			t.Run(fmt.Sprintf("disable=%t/attempt=%d", disable, attempt), func(t *testing.T) {
				first, second, project, settings := sharedModelEngines(t)
				if disable {
					settings.Profiles[1].Enabled = false
				} else {
					settings.Profiles = settings.Profiles[:1]
				}
				start := make(chan struct{})
				results := make(chan error, 2)
				go func() {
					<-start
					_, err := first.Dispatch("save_model_settings", rawMessage(map[string]any{"settings": settings}))
					results <- err
				}()
				go func() {
					<-start
					_, err := second.Dispatch("set_project_models", rawMessage(map[string]any{"project_id": project.ID, "revision": 0, "overrides": map[string]string{"ask": "reviewer"}}))
					results <- err
				}()
				close(start)
				left, right := <-results, <-results
				assert(t, (left == nil) != (right == nil), "Concurrent deletion and assignment did not have exactly one success")
				models := invoke[settingsResponse](t, first, "get_model_settings", map[string]any{}).Settings
				latest := invoke[Project](t, second, "get_project", map[string]any{"project_id": project.ID})
				assert(t, models.ValidateRoutes(latest.ModelOverrides) == nil, "Concurrent operations left an invalid reference")
			})
		}
	}
}
