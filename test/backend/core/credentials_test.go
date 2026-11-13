//go:build quillweave_whitebox

package core

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	websearch "quillweave/internal/search"
	"runtime"
	"strings"
	"testing"
)

// TestEncryptedCredentialsSurviveRestart exercises restart, real request authentication, redaction and on-disk encryption.
func TestEncryptedCredentialsSurviveRestart(t *testing.T) {
	t.Setenv("QUILLWEAVE_API_KEY", "")
	engine := testEngine(t)
	key := "CREDENTIAL_RESTART_SECRET_6e82"
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert(t, r.Header.Get("Authorization") == "Bearer "+key, "The reopened engine did not authenticate with its saved key")
		fmt.Fprint(w, streamPayload("Restored credentials work."))
	})
	config := defaultProvider()
	config.Model, config.BaseURL, config.APIKey = "credential-fixture", server.URL, key
	configureModel(t, engine, config)
	project := testProject()
	insertProject(t, engine, project)
	archive := invoke[map[string]string](t, engine, "export", map[string]any{"project_id": project.ID, "format": "archive"})
	assert(t, !strings.Contains(archive["content"], key) && !strings.Contains(archive["content"], `"credentials"`), "The project archive included credentials")
	view := invoke[settingsResponse](t, engine, "get_model_settings", nil)
	assert(t, view.KeyStatus["default"] && !strings.Contains(jsonText(view), key), "The public view lost or exposed the saved key")
	var encrypted []byte
	assert(t, engine.store.db.QueryRow("SELECT encrypted FROM credentials WHERE scope='model' AND profile_id='default'").Scan(&encrypted) == nil && len(encrypted) > len(key) && !bytes.Contains(encrypted, []byte(key)), "The database did not contain authenticated ciphertext")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		contents, err := os.ReadFile(engine.store.path + suffix)
		assert(t, err == nil || os.IsNotExist(err), fmt.Sprint(err))
		assert(t, !bytes.Contains(contents, []byte(key)), "A database or journal file contained the plaintext API key")
	}
	info, err := os.Stat(engine.store.path + ".key")
	assert(t, err == nil && info.Size() == 32, "The installation master key was not saved separately")
	if runtime.GOOS != "windows" {
		assert(t, info.Mode().Perm() == 0600, "The master key permissions allow other users to read it")
	}
	assert(t, engine.Close() == nil, "Could not close the first engine")
	reopened, err := NewEngine(engine.store.path)
	assert(t, err == nil, fmt.Sprint(err))
	defer reopened.Close()
	view = invoke[settingsResponse](t, reopened, "get_model_settings", nil)
	assert(t, view.KeyStatus["default"] && reopened.models.Profiles[0].Config.APIKey == key, "Restart lost the encrypted model key")
	invoke[map[string]any](t, reopened, "test_model", map[string]any{"profile_id": "default"})
}

// TestEncryptedCredentialMutations verifies ten supported save, clear, isolation and rollback scenarios.
func TestEncryptedCredentialMutations(t *testing.T) {
	for _, scenario := range []string{"preserve", "replace", "clear", "endpoint", "protocol", "delete", "recreate", "blank replacement", "stale revision", "transaction failure"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("QUILLWEAVE_API_KEY", "")
			engine := testEngine(t)
			config := defaultProvider()
			config.Model, config.APIKey = "credential-fixture", "ORIGINAL_ENCRYPTED_SECRET"
			configureModel(t, engine, config)
			view := invoke[settingsResponse](t, engine, "get_model_settings", nil)
			before := view.Settings
			beforeName := view.Settings.Profiles[0].Name
			view.Settings.Profiles[0].Name = "Updated profile"
			updates := map[string]SecretUpdate{}
			want := config.APIKey
			valid := true
			switch scenario {
			case "replace":
				want = "REPLACEMENT_ENCRYPTED_SECRET"
				updates["default"] = SecretUpdate{Action: "replace", Value: want}
			case "clear":
				want = ""
				updates["default"] = SecretUpdate{Action: "clear"}
			case "endpoint":
				want = ""
				view.Settings.Profiles[0].Config.BaseURL = "https://changed.example/v1"
			case "protocol":
				want = ""
				view.Settings.Profiles[0].Config.Protocol = "responses"
			case "delete", "recreate":
				want = ""
				view.Settings.Profiles = []ModelProfile{}
				view.Settings.DefaultProfile = ""
			case "blank replacement":
				valid = false
				updates["default"] = SecretUpdate{Action: "replace", Value: "  "}
			case "stale revision":
				valid = false
				view.Settings.Revision--
			case "transaction failure":
				valid = false
				_, err := engine.store.db.Exec("CREATE TRIGGER reject_credential_write BEFORE INSERT ON credentials BEGIN SELECT RAISE(ABORT,'simulated credential failure'); END")
				assert(t, err == nil, fmt.Sprint(err))
				updates["default"] = SecretUpdate{Action: "replace", Value: "FAILED_REPLACEMENT_SECRET"}
			}
			args := map[string]any{"settings": view.Settings, "key_updates": updates}
			if valid {
				view = invoke[settingsResponse](t, engine, "save_model_settings", args)
			} else {
				reject(t, engine, "save_model_settings", args)
				actual := invoke[settingsResponse](t, engine, "get_model_settings", nil)
				assert(t, actual.Settings.Revision == before.Revision && actual.Settings.Profiles[0].Name == beforeName, "A failed credential save partially committed configuration")
			}
			if scenario == "recreate" {
				recreated := modelsFromLegacy(config).WithoutSecrets()
				recreated.Revision = view.Settings.Revision
				invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": recreated})
			}
			peer, err := NewEngine(engine.store.path)
			assert(t, err == nil, fmt.Sprint(err))
			defer peer.Close()
			actual := invoke[settingsResponse](t, peer, "get_model_settings", nil)
			assert(t, actual.KeyStatus["default"] == (want != ""), "The reopened credential presence differs from the confirmed save")
			if len(peer.models.Profiles) > 0 {
				assert(t, peer.models.Profiles[0].Config.APIKey == want, "A saved key was lost, resurrected or sent to another destination")
			}
		})
	}
}

// TestEncryptedCredentialIntegrity rejects missing keys, altered ciphertext and swapped identity without destroying saved records.
func TestEncryptedCredentialIntegrity(t *testing.T) {
	for _, scenario := range []string{"missing master", "invalid master", "wrong master", "tampered ciphertext", "swapped identity", "swapped profile", "swapped scope", "unknown version", "truncated ciphertext"} {
		t.Run(scenario, func(t *testing.T) {
			engine := testEngine(t)
			config := defaultProvider()
			config.Model, config.APIKey = "credential-fixture", "INTEGRITY_TEST_SECRET"
			configureModel(t, engine, config)
			var before []byte
			assert(t, engine.store.db.QueryRow("SELECT encrypted FROM credentials WHERE scope='model' AND profile_id='default'").Scan(&before) == nil, "Missing saved ciphertext")
			var err error
			switch scenario {
			case "missing master":
				err = os.Remove(engine.store.path + ".key")
			case "invalid master":
				err = os.WriteFile(engine.store.path+".key", []byte("invalid"), 0600)
			case "wrong master":
				key := make([]byte, 32)
				_, err = rand.Read(key)
				if err == nil {
					err = os.WriteFile(engine.store.path+".key", key, 0600)
				}
			case "swapped identity":
				_, err = engine.store.db.Exec("UPDATE credentials SET identity='another-destination' WHERE scope='model'")
			case "swapped profile":
				_, err = engine.store.db.Exec("UPDATE credentials SET profile_id='another-profile' WHERE scope='model'")
			case "swapped scope":
				_, err = engine.store.db.Exec("UPDATE credentials SET scope='search' WHERE scope='model'")
			default:
				changed := bytes.Clone(before)
				switch scenario {
				case "tampered ciphertext":
					changed[len(changed)-1] ^= 1
				case "unknown version":
					changed[0] = 99
				case "truncated ciphertext":
					changed = changed[:2]
				}
				_, err = engine.store.db.Exec("UPDATE credentials SET encrypted=? WHERE scope='model'", changed)
			}
			assert(t, err == nil, fmt.Sprint(err))
			if scenario == "swapped scope" {
				reject(t, engine, "get_search_settings", nil)
			} else {
				reject(t, engine, "get_model_settings", nil)
			}
			reopened, err := NewEngine(engine.store.path)
			if reopened != nil {
				reopened.Close()
			}
			assert(t, err != nil && !strings.Contains(err.Error(), config.APIKey), "Invalid encrypted state was silently accepted or leaked a key")
			var count int
			assert(t, engine.store.db.QueryRow("SELECT COUNT(*) FROM credentials WHERE length(encrypted)>0").Scan(&count) == nil && count == 1, "A decryption failure discarded the saved credential")
			if scenario == "missing master" {
				_, err := os.Stat(engine.store.path + ".key")
				assert(t, os.IsNotExist(err), "A missing master key was silently replaced")
			}
		})
	}
}

// TestEncryptedSearchCredentialsSurviveRestart authenticates an actual search after reopening and verifies durable clear.
func TestEncryptedSearchCredentialsSurviveRestart(t *testing.T) {
	engine := testEngine(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert(t, r.Header.Get("Authorization") == "Bearer SEARCH_FIXTURE_KEY", "The restarted search did not use its encrypted credential")
		fmt.Fprint(w, `{"results":[{"url":"https://example.org/evidence","snippet":"restored search"}]}`)
	}))
	defer server.Close()
	profile := configureSearchFixture(t, engine, "deepseek", server.URL)
	assert(t, engine.Close() == nil, "Could not close the first search engine")
	peer, err := NewEngine(engine.store.path)
	assert(t, err == nil, fmt.Sprint(err))
	defer peer.Close()
	view := invoke[searchSettingsView](t, peer, "get_search_settings", nil)
	assert(t, view.KeyStatus[profile.ID] && !strings.Contains(jsonText(view), "SEARCH_FIXTURE_KEY"), "The search view lost or exposed its key after restart")
	input := searchStart{RequestID: newID(), Purpose: "test", Operation: "search", Query: "history", Selection: websearch.Selection{Mode: "profile", ProfileID: profile.ID}}
	done := awaitSearchFixture(t, peer, invoke[searchRun](t, peer, "start_search_run", input))
	assert(t, done.Status == "succeeded" && !strings.Contains(jsonText(done), "SEARCH_FIXTURE_KEY"), "The restarted search failed or recorded the secret")
	view = invoke[searchSettingsView](t, peer, "mutate_search_settings", searchMutation{Operation: "upsert_profile", ExpectedRevision: view.Revision, ModelRevision: view.ModelRevision, Profile: profile, KeyUpdates: map[string]SecretUpdate{profile.ID: {Action: "clear"}}})
	assert(t, !view.KeyStatus[profile.ID], "The search key clear was not reflected in its view")
	third, err := NewEngine(engine.store.path)
	assert(t, err == nil, fmt.Sprint(err))
	defer third.Close()
	reject(t, third, "start_search_run", searchStart{RequestID: newID(), Purpose: "test", Operation: "search", Query: "history", Selection: input.Selection})
}

// TestEncryptedCredentialEnvironmentAndEmptyStartup keeps empty installations file-free and makes saved clears override environment fallback.
func TestEncryptedCredentialEnvironmentAndEmptyStartup(t *testing.T) {
	t.Setenv("QUILLWEAVE_API_KEY", "ENVIRONMENT_TEST_SECRET")
	engine := testEngine(t)
	config := defaultProvider()
	config.Model = "environment-model"
	_, err := engine.store.db.Exec("INSERT INTO settings(id,document) VALUES(1,?)", jsonText(modelsFromLegacy(config)))
	assert(t, err == nil, fmt.Sprint(err))
	_, err = os.Stat(engine.store.path + ".key")
	assert(t, os.IsNotExist(err), "A metadata-only installation created a needless master key file")
	view := invoke[settingsResponse](t, engine, "get_model_settings", nil)
	assert(t, view.KeyStatus[view.Settings.DefaultProfile], "The default environment credential was lost")
	invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": view.Settings, "key_updates": map[string]SecretUpdate{view.Settings.DefaultProfile: {Action: "clear"}}})
	peer, err := NewEngine(engine.store.path)
	assert(t, err == nil, fmt.Sprint(err))
	defer peer.Close()
	actual := invoke[settingsResponse](t, peer, "get_model_settings", nil)
	assert(t, !actual.KeyStatus[view.Settings.DefaultProfile], "A cleared saved key was resurrected by the startup environment")
	files, err := filepath.Glob(filepath.Join(filepath.Dir(engine.store.path), ".credential-key-*"))
	assert(t, err == nil && len(files) == 0, "Credential creation left temporary key material behind")
}

// TestEncryptedCredentialConcurrentCreationAndNonce serializes first saves and verifies fresh encryption after replacing the same key twice.
func TestEncryptedCredentialConcurrentCreationAndNonce(t *testing.T) {
	t.Setenv("QUILLWEAVE_API_KEY", "")
	first := testEngine(t)
	config := defaultProvider()
	config.Model = "concurrent-credential-fixture"
	configureModel(t, first, config)
	second, err := NewEngine(first.store.path)
	assert(t, err == nil, fmt.Sprint(err))
	defer second.Close()
	view := invoke[settingsResponse](t, first, "get_model_settings", nil)
	type outcome struct {
		key string
		err error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for i, engine := range []*Engine{first, second} {
		go func(engine *Engine, key string) {
			<-start
			_, err := engine.Dispatch("save_model_settings", rawMessage(map[string]any{"settings": view.Settings, "key_updates": map[string]SecretUpdate{"default": {Action: "replace", Value: key}}}))
			results <- outcome{key: key, err: err}
		}(engine, fmt.Sprintf("CONCURRENT_CREDENTIAL_%d", i))
	}
	close(start)
	winner := ""
	for range 2 {
		result := <-results
		if result.err == nil {
			assert(t, winner == "", "Both stale credential saves were committed")
			winner = result.key
		} else {
			assert(t, strings.Contains(result.err.Error(), "changed"), "A concurrent save failed outside revision protection")
		}
	}
	assert(t, winner != "", "Neither concurrent credential save succeeded")
	current := invoke[settingsResponse](t, first, "get_model_settings", nil)
	assert(t, first.models.Profiles[0].Config.APIKey == winner, "The master key did not decrypt the winning transaction")
	var before []byte
	assert(t, first.store.db.QueryRow("SELECT encrypted FROM credentials WHERE scope='model' AND profile_id='default'").Scan(&before) == nil, "Missing winning ciphertext")
	for _, value := range []string{"INTERMEDIATE_CREDENTIAL", winner} {
		current = invoke[settingsResponse](t, first, "save_model_settings", map[string]any{"settings": current.Settings, "key_updates": map[string]SecretUpdate{"default": {Action: "replace", Value: value}}})
	}
	var after []byte
	assert(t, first.store.db.QueryRow("SELECT encrypted FROM credentials WHERE scope='model' AND profile_id='default'").Scan(&after) == nil && !bytes.Equal(before, after), "Encrypting a repeated value reused its ciphertext or nonce")
	invoke[settingsResponse](t, second, "get_model_settings", nil)
	assert(t, second.models.Profiles[0].Config.APIKey == winner, "A live peer did not restore the newly encrypted credential")
	files, err := filepath.Glob(filepath.Join(filepath.Dir(first.store.path), ".credential-key-*"))
	assert(t, err == nil && len(files) == 0, "Concurrent first saves left temporary master keys behind")
}
