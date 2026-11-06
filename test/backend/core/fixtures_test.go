//go:build quillweave_whitebox

package core

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// invoke uses the real command dispatcher and JSON boundary to check returned values.
func invoke[T any](t *testing.T, engine *Engine, command string, args any) T {
	t.Helper()
	value, err := engine.Dispatch(command, rawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", command, err)
	}
	var result T
	if err := decodeJSON(rawMessage(value), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// reject checks rejection through the command interface.
func reject(t *testing.T, engine *Engine, command string, args any) {
	t.Helper()
	if _, err := engine.Dispatch(command, rawMessage(args)); err == nil {
		t.Fatalf("%s should reject the input", command)
	}
}

// assert provides a focused semantic assertion message.
func assert(t *testing.T, ok bool, message string) {
	t.Helper()
	if !ok {
		t.Fatal(message)
	}
}

// testProject creates an English sample for storage and model workflow tests.
func testProject() Project {
	project := NewProject("Test project", "Mystery", "Investigate the letter")
	chapter := NewChapter(1, "The Letter")
	chapter.Body = "Lin Wu found a letter. She decided to visit the lighthouse."
	project.Chapters = append(project.Chapters, chapter)
	return project
}

// testCandidate provides an event with a verbatim source quote.
func testCandidate() Candidate {
	return Candidate{Entity: "Lin Wu", Kind: "event", Claim: "Finds a letter", Quote: "Lin Wu found a letter.", Epistemic: "explicit"}
}

// insertProject installs fixture data in an isolated database.
func insertProject(t *testing.T, engine *Engine, project Project) {
	t.Helper()
	if err := engine.store.Insert(&project); err != nil {
		t.Fatal(err)
	}
}

// ingestCandidate prepares pending evidence through the compilation implementation.
func ingestCandidate(t *testing.T, project *Project) {
	t.Helper()
	chapter := project.Chapters[0]
	if _, err := ingest(project, chapter.ID, hashText(chapter.Body), []Candidate{testCandidate()}, "mock"); err != nil {
		t.Fatal(err)
	}
}

// memoryProject creates a pending story memory with complete dependencies.
func memoryProject(t *testing.T) (Project, ProviderConfig) {
	t.Helper()
	project := testProject()
	ingestCandidate(t, &project)
	config := defaultProvider()
	config.Model = "mock"
	fingerprint, err := compilationFingerprint(&project, project.Chapters[0].ID, config)
	if err != nil {
		t.Fatal(err)
	}
	project.Compilations = append(project.Compilations, WikiCompilation{ID: newID(), SourceID: project.Chapters[0].ID, SourceHash: hashText(project.Chapters[0].Body), RulesHash: rulesHash(&project), Fingerprint: fingerprint, CompilerVersion: CompilerVersion, Model: "mock", Chunks: 1, Sections: []MemorySection{{Summary: "Key memory: Lin Wu found a letter.", EvidenceIDs: []string{project.Facts[0].ID}, Questions: []string{}}}, Status: "pending", CreatedAt: now()})
	return project, config
}

// hasKind finds categories in actual retrieval or review output.
func hasKind(items []ContextItem, kind string) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

// testProfile creates a usable profile with fixture credentials.
func testProfile(id string) ModelProfile {
	config := defaultProvider()
	config.Model = id + "-model"
	return ModelProfile{ID: id, Name: id, Enabled: true, Config: config}
}

// testSettings provides stable references for model task routing.
func testSettings() ModelSettings {
	return ModelSettings{SchemaVersion: 2, Profiles: []ModelProfile{testProfile("writer"), testProfile("reviewer")}, DefaultProfile: "writer", Routes: map[string]string{}}
}

type settingsResponse struct {
	Settings  ModelSettings   `json:"settings"`
	KeyStatus map[string]bool `json:"key_status"`
}

// configureModel configures a model through the production save command and explicit key operation.
func configureModel(t *testing.T, engine *Engine, config ProviderConfig) {
	t.Helper()
	current := invoke[settingsResponse](t, engine, "get_model_settings", map[string]any{})
	models := modelsFromLegacy(config).WithoutSecrets()
	models.Revision = current.Settings.Revision
	update := SecretUpdate{Action: "clear"}
	if config.APIKey != "" {
		update = SecretUpdate{Action: "replace", Value: config.APIKey}
	}
	invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": models, "key_updates": map[string]SecretUpdate{"default": update}})
}

// testEngine creates a temporary database and closes workers before cleanup.
func testEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := NewEngine(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Error(err)
		}
	})
	return engine
}

// waitForJobMutex observes the real worker stack to schedule tests at the model-return lock boundary.
func waitForJobMutex(t *testing.T, function string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		buffer := make([]byte, 1<<20)
		stack := string(buffer[:runtime.Stack(buffer, true)])
		for _, goroutine := range strings.Split(stack, "\n\n") {
			if strings.Contains(goroutine, "(*Engine)."+function+"(") && strings.Contains(goroutine, "sync.(*Mutex).Lock(") {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Task did not reach the expected lock wait")
}

// sharedModelEngines creates two engines using one temporary database for cross-instance tests.
func sharedModelEngines(t *testing.T) (*Engine, *Engine, Project, ModelSettings) {
	t.Helper()
	t.Setenv("QUILLWEAVE_API_KEY", "")
	path := filepath.Join(t.TempDir(), "shared-models.sqlite3")
	first, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close() })
	project := testProject()
	insertProject(t, first, project)
	settings := invoke[settingsResponse](t, first, "save_model_settings", map[string]any{"settings": testSettings()}).Settings
	second, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close() })
	return first, second, project, settings
}
