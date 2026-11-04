package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPlanningConversationUsesOutlineHistory checks actual requests through discussion, outline, and follow-up tasks.
func TestPlanningConversationUsesOutlineHistory(t *testing.T) {
	for _, shared := range []bool{true, false} {
		t.Run(fmt.Sprintf("shared-profile=%t", shared), func(t *testing.T) {
			engine := testEngine(t)
			project := invoke[Project](t, engine, "create_project", map[string]any{"title": "A lighthouse mystery", "genre": "Mystery", "premise": "A keeper protects her sister."})
			var mutex sync.Mutex
			requests := []map[string]any{}
			replies := []string{"The sister should hold the missing clue.", "1. The letter arrives.\n2. The sisters follow the tide.", "Give the sister a choice that changes the ending."}
			server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
					writer.WriteHeader(400)
					return
				}
				mutex.Lock()
				index := len(requests)
				requests = append(requests, body)
				mutex.Unlock()
				writer.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(writer, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"PRIVATE_PLAN_%d\"}}]}\n\n%s", index, streamPayload(replies[index]))
			})
			config := providerPresets()[7].Config
			config.BaseURL = server.URL
			settings := invoke[settingsResponse](t, engine, "get_model_settings", nil).Settings
			settings.Profiles = []ModelProfile{{ID: "writer", Name: "Writer", Enabled: true, Config: config}, {ID: "outliner", Name: "Outliner", Enabled: true, Config: config}}
			settings.DefaultProfile = "writer"
			if !shared {
				settings.Routes["outline"] = "outliner"
			}
			invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": settings})
			args := map[string]any{"project_id": project.ID, "chapter_id": "", "action": "ask", "instruction": "Discuss a mystery in which the keeper protects her sister."}
			first := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, first.Status == "done", first.Error)
			args["action"], args["instruction"] = "outline", "Generate a chapter outline from our discussion."
			args["history"] = []Message{{Role: "user", Content: "Discuss a mystery in which the keeper protects her sister."}, {Role: "assistant", Content: first.Text, SourceJobID: first.ID}}
			outline := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, outline.Status == "done", outline.Error)
			args["action"], args["instruction"] = "ask", "How can we improve this outline?"
			args["history"] = []Message{{Role: "user", Content: "Generate a chapter outline from our discussion."}, {Role: "assistant", Content: outline.Text, SourceJobID: outline.ID}}
			followup := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, followup.Status == "done", followup.Error)
			mutex.Lock()
			defer mutex.Unlock()
			assert(t, len(requests) == 3 && strings.Contains(jsonText(requests[1]), first.Text) && strings.Contains(jsonText(requests[2]), "The letter arrives"), "Planning discussion or outline was missing from the request")
			assert(t, strings.Contains(jsonText(requests[1]), "PRIVATE_PLAN_0") == shared && strings.Contains(jsonText(requests[2]), "PRIVATE_PLAN_1") == shared, "Private planning history crossed profile boundaries or was lost")
			runs := invoke[[]json.RawMessage](t, engine, "get_runs", map[string]any{"project_id": project.ID})
			assert(t, !strings.Contains(jsonText(runs), "PRIVATE_PLAN_"), "Public planning history exposed private context")
		})
	}
}

// TestEnglishPromptsAndLegacyViewpoints checks English defaults and forward-compatible saved viewpoints.
func TestEnglishPromptsAndLegacyViewpoints(t *testing.T) {
	han := regexp.MustCompile(`[\p{Han}]`)
	project := testProject()
	for _, style := range writingPresets() {
		project.WritingStyle = style.ID
		for _, action := range []string{"ask", "outline", "architecture", "continue", "rewrite", "review"} {
			prompt, err := prepareAI(&project, AIRequest{ProjectID: project.ID, ChapterID: project.Chapters[0].ID, Action: action, Instruction: "Develop the story."}, 24000)
			assert(t, err == nil && !han.MatchString(jsonText(prompt.Messages)), "Application-owned planning prompts must be English")
		}
	}
	assert(t, !han.MatchString(extractionSystem+memorySystem+DefaultWikiPurpose), "Wiki instructions must be English")
	engine := testEngine(t)
	legacy := "\u7b2c\u4e09\u4eba\u79f0\u6709\u9650\u89c6\u89d2"
	saved := invoke[Project](t, engine, "create_project", map[string]any{"title": "Legacy viewpoint", "genre": "Mystery", "premise": "An old project.", "narrative_pov": legacy})
	assert(t, saved.NarrativePov == legacy, "A saved legacy viewpoint was rewritten")
	prompt := invoke[Prompt](t, engine, "prepare_ai", map[string]any{"project_id": saved.ID, "chapter_id": "", "action": "outline", "instruction": "Create an outline."})
	assert(t, strings.Contains(jsonText(prompt.Messages), "Third-person limited") && !han.MatchString(jsonText(prompt.Messages)), "A legacy viewpoint did not receive an English prompt label")
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

// TestWikiCancelAtCommitBoundary checks that accepted cancellation prevents Wiki publication at the commit boundary.
func TestWikiCancelAtCommitBoundary(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	staged := cloneProject(project)
	ingestCandidate(t, &staged)
	responses := []string{jsonText(map[string]any{"facts": []Candidate{testCandidate()}}), jsonText(MemorySection{Summary: "Finds a letter", EvidenceIDs: []string{staged.Facts[0].ID}, Questions: []string{}})}
	entered, release := make(chan struct{}), make(chan struct{})
	var released sync.Once
	defer released.Do(func() { close(release) })
	var calls atomic.Int32
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		index := int(calls.Add(1)) - 1
		if index == 1 {
			close(entered)
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
		}
		fmt.Fprint(writer, streamPayload(responses[index]))
	})
	config := defaultProvider()
	config.Model, config.BaseURL = "mock", server.URL+"/v1"
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "wiki", "instruction": ""})
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("Stage two was not reached")
	}
	engine.mu.Lock()
	locked := true
	defer func() {
		if locked {
			engine.mu.Unlock()
		}
	}()
	released.Do(func() { close(release) })
	waitForJobMutex(t, "runJob")
	response := invoke[map[string]bool](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
	assert(t, response["cancelled"], "Cancellation before commit was not accepted")
	engine.mu.Unlock()
	locked = false
	done := finishJob(t, engine, job)
	latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, done.Status == "cancelled" && latest.Revision == 0 && len(latest.Facts) == 0 && len(latest.Compilations) == 0, "Knowledge was committed after cancellation was accepted")
	runs := invoke[[]Job](t, engine, "get_runs", map[string]any{"project_id": project.ID})
	assert(t, len(runs) == 1 && runs[0].Status == "cancelled", "Cancellation history disagrees with task status")
}

// TestCancelAfterStreamBeforeCompletion checks that accepted cancellation determines status after streaming has returned.
func TestCancelAfterStreamBeforeCompletion(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	entered, release := make(chan struct{}), make(chan struct{})
	var released sync.Once
	defer released.Do(func() { close(release) })
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-request.Context().Done():
			return
		}
		fmt.Fprint(writer, streamPayload("The generated text is complete."))
	})
	config := defaultProvider()
	config.Model, config.BaseURL = "mock", server.URL+"/v1"
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "continue", "instruction": "Continue"})
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("The model did not receive the request")
	}
	engine.mu.Lock()
	locked := true
	defer func() {
		if locked {
			engine.mu.Unlock()
		}
	}()
	released.Do(func() { close(release) })
	waitForJobMutex(t, "executeJob")
	response := invoke[map[string]bool](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
	assert(t, response["cancelled"], "An unfinished generation could not be cancelled")
	engine.mu.Unlock()
	locked = false
	done := finishJob(t, engine, job)
	assert(t, done.Status == "cancelled" && done.Text == "The generated text is complete.", "Cancellation was overwritten by success or received text was lost")
	response = invoke[map[string]bool](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
	assert(t, !response["cancelled"], "A finished task accepted a new cancellation")
}

// TestWikiCommitRejectsLateCancel checks that cancellation after commit begins is rejected without changing the result.
func TestWikiCommitRejectsLateCancel(t *testing.T) {
	engine, other, project, _ := sharedModelEngines(t)
	staged := cloneProject(project)
	ingestCandidate(t, &staged)
	responses := []string{jsonText(map[string]any{"facts": []Candidate{testCandidate()}}), jsonText(MemorySection{Summary: "Finds a letter", EvidenceIDs: []string{staged.Facts[0].ID}, Questions: []string{}})}
	entered, release := make(chan struct{}), make(chan struct{})
	var released sync.Once
	defer released.Do(func() { close(release) })
	var calls atomic.Int32
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		index := int(calls.Add(1)) - 1
		if index == 1 {
			close(entered)
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
		}
		fmt.Fprint(writer, streamPayload(responses[index]))
	})
	config := defaultProvider()
	config.Model, config.BaseURL = "mock", server.URL+"/v1"
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "wiki", "instruction": ""})
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("Stage two was not reached")
	}
	connection, err := other.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer connection.ExecContext(context.Background(), "ROLLBACK")
	released.Do(func() { close(release) })
	deadline := time.Now().Add(3 * time.Second)
	committing := false
	for time.Now().Before(deadline) {
		engine.jobsMu.Lock()
		committing = engine.jobs[job.ID].committing
		engine.jobsMu.Unlock()
		if committing {
			break
		}
		time.Sleep(time.Millisecond)
	}
	assert(t, committing, "Commit stage was not reached")
	response := invoke[map[string]bool](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
	assert(t, !response["cancelled"], "A committing task incorrectly accepted cancellation")
	if _, err := connection.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	done := finishJob(t, engine, job)
	latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, done.Status == "done" && len(latest.Facts) == 1 && len(latest.Compilations) == 1, "An unaccepted cancellation changed the commit result")
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

// TestModelRefreshPreservesOnlyLocalMatchingSecrets checks metadata refresh while keys remain scoped to the local matching endpoint.
func TestModelRefreshPreservesOnlyLocalMatchingSecrets(t *testing.T) {
	first, second, project, settings := sharedModelEngines(t)
	var oldCalls, newCalls atomic.Int32
	oldServer := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		oldCalls.Add(1)
		fmt.Fprint(writer, streamPayload("Old endpoint complete"))
	})
	newServer := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		newCalls.Add(1)
		if request.Header.Get("Authorization") != "" {
			t.Error("A stale endpoint key or another process's key was sent")
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		fmt.Fprint(writer, streamPayload("Current endpoint complete"))
	})
	settings.Profiles[0].Config.BaseURL = oldServer.URL + "/v1"
	saved := invoke[settingsResponse](t, first, "save_model_settings", map[string]any{"settings": settings, "key_updates": map[string]SecretUpdate{"writer": {Action: "replace", Value: "first-process-key"}}})
	view := invoke[settingsResponse](t, second, "get_model_settings", map[string]any{})
	assert(t, !view.KeyStatus["writer"], "Persistent metadata exposed another process's key")
	saved = invoke[settingsResponse](t, second, "save_model_settings", map[string]any{"settings": saved.Settings, "key_updates": map[string]SecretUpdate{"writer": {Action: "replace", Value: "second-process-key"}}})
	firstView := invoke[settingsResponse](t, first, "get_model_settings", map[string]any{})
	assert(t, firstView.KeyStatus["writer"] && first.models.Profiles[0].Config.APIKey == "first-process-key", "Refreshing the same endpoint lost or mixed a process-local key")
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

// TestPersistenceAndConflictingSave checks disk persistence, version history, and stale revision rejection.
func TestPersistenceAndConflictingSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "writing.sqlite3")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	project := testProject()
	old := cloneProject(project)
	if err := store.Insert(&project); err != nil {
		t.Fatal(err)
	}
	project.Chapters[0].SetContent("New title", "New chapter text", "", "draft", nil)
	if err := store.Save(&project, 0); err != nil {
		t.Fatal(err)
	}
	assert(t, store.Save(&old, 0) != nil, "A stale revision was accepted")
	store.Close()
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.Get(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	assert(t, loaded.Chapters[0].Body == "New chapter text" && loaded.Chapters[0].Versions[0].Body == old.Chapters[0].Body, "The chapter or its version was not saved")
}

// TestVersionRestoreIsNonDestructive checks that restoration retains the prior version and identical saves add no version.
func TestVersionRestoreIsNonDestructive(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	args := map[string]any{"project_id": project.ID, "revision": 0, "chapter_id": project.Chapters[0].ID, "title": "The Letter", "body": "Revised chapter text", "synopsis": "", "status": "draft"}
	saved := invoke[Project](t, engine, "save_chapter", args)
	restored := invoke[Project](t, engine, "restore_version", map[string]any{"project_id": project.ID, "revision": 1, "chapter_id": project.Chapters[0].ID, "version_id": saved.Chapters[0].Versions[0].ID})
	assert(t, restored.Chapters[0].Body == project.Chapters[0].Body && len(restored.Chapters[0].Versions) == 2 && restored.Chapters[0].Versions[1].Body == "Revised chapter text", "Restoring a version lost history")
	args["revision"] = 2
	args["body"] = project.Chapters[0].Body
	again := invoke[Project](t, engine, "save_chapter", args)
	assert(t, len(again.Chapters[0].Versions) == 2, "Identical text created duplicate versions")
}

// TestChunkingCoversUnicodeTail checks complete reconstruction of long Unicode and emoji text.
func TestChunkingCoversUnicodeTail(t *testing.T) {
	text := strings.Repeat("\u4e2d\u6587\u6bb5\u843d。", 1600) + "\u6700\u540e\u7684\u5173\u952e\u7ebf\u7d22🙂"
	parts := chunks(text, 6000, 150)
	assert(t, len(parts) > 1 && strings.HasSuffix(parts[len(parts)-1], "\u6700\u540e\u7684\u5173\u952e\u7ebf\u7d22🙂"), "The tail was lost")
	restored := parts[0]
	for _, part := range parts[1:] {
		restored += string([]rune(part)[150:])
	}
	assert(t, restored == text, "Chunks could not reconstruct the full text")
	assert(t, len(chunks(text, 0, 0)) == 0, "Invalid chunk parameters were accepted")
}

// TestExtractionRejectsInvalidEvidence rejects fabricated quotes, invalid kinds, and malformed analysis.
func TestExtractionRejectsInvalidEvidence(t *testing.T) {
	source := "Lin Wu found a letter."
	valid := jsonText(map[string]any{"facts": []Candidate{testCandidate()}})
	facts, err := parseCandidates(valid, source)
	assert(t, err == nil && len(facts) == 1, "A valid fact was not parsed")
	for _, raw := range []string{"I think the character is happy", strings.ReplaceAll(valid, "explicit", "certain"), strings.ReplaceAll(valid, "Lin Wu found a letter.", "A quote absent from the source."), `{"facts":null}`} {
		_, err := parseCandidates(raw, source)
		assert(t, err != nil, "Invalid analysis was accepted")
	}
	_, err = parseCandidates("```json\n"+valid+"\n```", source)
	assert(t, err == nil, "A valid fenced JSON block was rejected")
}

// TestWikiIsIdempotentAndPreservesReview checks that repeated compilation preserves deduplication and author review.
func TestWikiIsIdempotentAndPreservesReview(t *testing.T) {
	project := testProject()
	ingestCandidate(t, &project)
	project.Facts[0].Status = "rejected"
	count, err := ingest(&project, project.Chapters[0].ID, hashText(project.Chapters[0].Body), []Candidate{testCandidate()}, "mock")
	assert(t, err == nil && count == 0 && project.Facts[0].Status == "rejected", "Repeated compilation changed review status")
}

// TestStaleEvidenceCannotBeConfirmed checks that changed sources invalidate evidence confirmation and publication.
func TestStaleEvidenceCannotBeConfirmed(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	oldHash := hashText(project.Chapters[0].Body)
	ingestCandidate(t, &project)
	project.Chapters[0].Body += "The chapter has changed."
	assert(t, !factCurrent(&project, &project.Facts[0]), "Old evidence remained current")
	_, err := ingest(&project, project.Chapters[0].ID, oldHash, []Candidate{testCandidate()}, "mock")
	assert(t, err != nil, "A stale asynchronous result was accepted")
	insertProject(t, engine, project)
	reject(t, engine, "review_fact", map[string]any{"project_id": project.ID, "revision": 0, "fact_id": project.Facts[0].ID, "status": "confirmed"})
}

// TestRetrievalFiltersFuturePendingAndStale excludes future, pending, and stale facts from retrieval.
func TestRetrievalFiltersFuturePendingAndStale(t *testing.T) {
	project := ExampleProject()
	for i := range project.Facts {
		project.Facts[i].Status = "confirmed"
	}
	items := retrieve(&project, "Lin Wu", 1, 30)
	assert(t, hasKind(items, "wiki"), "Missing valid evidence")
	for _, fact := range project.Facts {
		if fact.Chapter > 1 {
			for _, item := range items {
				assert(t, item.ID != "W:"+fact.ID, "Future facts leaked into retrieval")
			}
		}
	}
	for i := range project.Facts {
		project.Facts[i].Status = "pending"
	}
	assert(t, !hasKind(retrieve(&project, "Lin Wu", 3, 30), "wiki"), "Pending facts were retrieved")
}

// TestContextBudgetRefusesSilentTruncation rejects over-budget context and retains complete text when the budget permits.
func TestContextBudgetRefusesSilentTruncation(t *testing.T) {
	project := testProject()
	project.Chapters[0].Body = strings.Repeat("\u4e2d\u6587\u6b63\u6587", 4000)
	request := AIRequest{ProjectID: project.ID, ChapterID: project.Chapters[0].ID, Action: "review", Instruction: "Review consistency", History: []Message{}}
	_, err := prepareAI(&project, request, 4000)
	assert(t, err != nil && strings.Contains(err.Error(), "exceeds"), "Over-budget context was silently truncated")
	prompt, err := prepareAI(&project, request, 24000)
	assert(t, err == nil && strings.Contains(prompt.Messages[len(prompt.Messages)-1].Content, project.Chapters[0].Body), "The prompt did not include the complete chapter")
}

// TestAliasesAndRenamesKeepIdentity checks identity-preserving renames and unique aliases.
func TestAliasesAndRenamesKeepIdentity(t *testing.T) {
	engine := testEngine(t)
	project := ExampleProject()
	insertProject(t, engine, project)
	entity := project.Entities[0]
	entity.Name = "Lin Xiaowu"
	entity.Aliases = []string{"Lin Wu"}
	next := invoke[Project](t, engine, "save_entity", map[string]any{"project_id": project.ID, "revision": 0, "entity": entity})
	assert(t, next.Facts[0].Entity == "Lin Xiaowu", "Existing facts did not follow the entity rename")
	other := project.Entities[1]
	other.Aliases = []string{"Lin Wu"}
	reject(t, engine, "save_entity", map[string]any{"project_id": project.ID, "revision": 1, "entity": other})
}

// TestLegacyImportPreservesOriginals checks legacy text, versions, and archives while retaining original file bytes.
func TestLegacyImportPreservesOriginals(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"chapters", ".versions", "\u89d2\u8272\u5e93/\u4e3b\u8981\u89d2\u8272"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"chapters/chapter_10.txt": "\u7b2c10\u7ae0 \u56de\u5f52\n\u539f\u59cb\u6b63\u6587", "chapters/chapter_2.txt": "\u7b2c\u4e8c\u7ae0\u5185\u5bb9", "Novel_architecture.txt": "\u65e7\u8bbe\u5b9a", "foreshadowing.json": `{"foreshadowings":[]}`, ".versions/chapter_10_v1.json": `{"metadata":{"created_at":"2025-01-01"},"content":"\u5386\u53f2\u6b63\u6587"}`, "\u89d2\u8272\u5e93/\u4e3b\u8981\u89d2\u8272/\u6797\u96fe.txt": "\u89d2\u8272\u8d44\u6599"}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	project, err := importLegacy(root)
	if err != nil {
		t.Fatal(err)
	}
	assert(t, len(project.Chapters) == 2 && project.Chapters[0].Number == 2 && project.Chapters[1].Number == 10 && project.Chapters[1].Versions[0].Body == "\u5386\u53f2\u6b63\u6587", "\u65e7\u7ae0\u8282\u6216\u7248\u672c\u672a\u4fdd\u7559")
	assert(t, len(project.Facts) == 0 && len(project.Sources) == 3, "\u65e7\u673a\u5668\u7ed3\u8bba\u88ab\u9519\u8bef\u63d0\u5347\u4e3a\u4e8b\u5b9e")
	for name, text := range files {
		after, err := os.ReadFile(filepath.Join(root, name))
		assert(t, err == nil && string(after) == text, "\u5bfc\u5165\u6539\u53d8\u4e86\u539f\u6587\u4ef6")
	}
}

// TestBackupRoundtripAndInvalidArchive restores a separate project and rejects malformed imports atomically.
func TestBackupRoundtripAndInvalidArchive(t *testing.T) {
	engine := testEngine(t)
	project := ExampleProject()
	insertProject(t, engine, project)
	exported := invoke[map[string]string](t, engine, "export", map[string]any{"project_id": project.ID, "format": "archive"})
	imported := invoke[Project](t, engine, "import_archive", map[string]any{"content": exported["content"]})
	assert(t, imported.ID != project.ID && reflect.DeepEqual(imported.Chapters, project.Chapters) && reflect.DeepEqual(imported.Facts, project.Facts), "Backup round-trip changed content")
	reject(t, engine, "import_archive", map[string]any{"content": "{}"})
	projects, err := engine.store.List()
	assert(t, err == nil && len(projects) == 2, "An invalid backup created a project")
}

// TestProviderURLsPreservePaths retains custom version paths and rejects unsafe endpoint credentials or schemes.
func TestProviderURLsPreservePaths(t *testing.T) {
	config := defaultProvider()
	config.Model = "test"
	config.BaseURL = "https://example.org/custom/v4/"
	endpoint, err := validateConfig(config)
	assert(t, err == nil && endpoint == "https://example.org/custom/v4/chat/completions", "The path was rewritten")
	config.Protocol = "ollama"
	config.BaseURL = "http://localhost:11434/api"
	endpoint, err = validateConfig(config)
	assert(t, err == nil && endpoint == "http://localhost:11434/api/chat", "Incorrect Ollama endpoint")
	for _, address := range []string{"https://secret@example.org", "http://untrusted.example.org", "https://example.org/?key=x", "https://example.org/#token"} {
		config.BaseURL = address
		_, err := validateConfig(config)
		assert(t, err != nil, "An unsafe endpoint was accepted")
	}
}

// TestStreamProtocolsAndTruncation checks Unicode protocol parsing, completion markers, truncation, and service errors.
func TestStreamProtocolsAndTruncation(t *testing.T) {
	text, _, err := parseLine("data: {\"choices\":[{\"delta\":{\"content\":\"\u4e2d\u6587\"}}]}\r", "openai")
	assert(t, err == nil && text == "\u4e2d\u6587", "SSE Unicode text parsing failed")
	_, done, err := parseLine("data: [DONE]", "openai")
	assert(t, err == nil && done, "Completion marker was not recognized")
	text, done, err = parseLine(`{"message":{"content":"Text"},"done":true}`, "ollama")
	assert(t, err == nil && done && text == "Text", "NDJSON parsing failed")
	_, _, err = parseLine(`data: {"choices":[{"finish_reason":"length"}]}`, "openai")
	assert(t, err != nil, "Truncation was treated as successful completion")
	_, _, err = parseLine(`data: {"error":"private service detail"}`, "openai")
	assert(t, err != nil && !strings.Contains(err.Error(), "private"), "Private provider response content was exposed")
}

// TestCredentialsAreNotPersistedOrReturned checks that API and SQLite views omit process-local credentials.
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

// TestRenamedEntityDoesNotDuplicateFacts checks that a rename retaining an alias creates no duplicate fact.
func TestRenamedEntityDoesNotDuplicateFacts(t *testing.T) {
	project := testProject()
	ingestCandidate(t, &project)
	project.Entities[0].Name = "Lin Xiaowu"
	project.Entities[0].Aliases = []string{"Lin Wu"}
	project.Facts[0].Entity = "Lin Xiaowu"
	count, err := ingest(&project, project.Chapters[0].ID, hashText(project.Chapters[0].Body), []Candidate{testCandidate()}, "mock")
	assert(t, err == nil && count == 0, "Renaming inserted duplicate facts")
}

// TestOldProjectSchemaLoadsWithStoryDefaults checks defaults for historical projects missing newer fields.
func TestOldProjectSchemaLoadsWithStoryDefaults(t *testing.T) {
	var value map[string]json.RawMessage
	_ = decodeJSON(rawMessage(testProject()), &value)
	delete(value, "wiki_purpose")
	delete(value, "compilations")
	var project Project
	err := decodeJSON(rawMessage(value), &project)
	assert(t, err == nil && project.WikiPurpose != "" && len(project.Compilations) == 0 && len(project.Chapters) == 1, "Legacy format compatibility failed")
}

// TestMemoryRejectsFabricatedReferences rejects memories with fabricated evidence IDs or absent support.
func TestMemoryRejectsFabricatedReferences(t *testing.T) {
	project, _ := memoryProject(t)
	for _, ids := range [][]string{{"missing"}, {}} {
		_, err := parseMemory(jsonText(map[string]any{"summary": "Claim", "evidence_ids": ids, "questions": []string{}}), project.Facts)
		assert(t, err != nil, "A fabricated memory was accepted")
	}
	_, err := parseMemory(jsonText(project.Compilations[0].Sections[0]), project.Facts)
	assert(t, err == nil, "A valid memory was rejected")
}

// TestOnlyReviewedMemoryEntersContext requires approved memory and evidence before adding memory to generation context.
func TestOnlyReviewedMemoryEntersContext(t *testing.T) {
	project, _ := memoryProject(t)
	assert(t, !hasKind(retrieve(&project, "Key memory", 1, 30), "memory"), "Pending memory was retrieved")
	project.Compilations[0].Status = "confirmed"
	assert(t, !hasKind(retrieve(&project, "Key memory", 1, 30), "memory"), "Memory with unreviewed evidence was retrieved")
	project.Facts[0].Status = "confirmed"
	assert(t, hasKind(retrieve(&project, "Key memory", 1, 30), "memory"), "Confirmed memory was not retrieved")
	assert(t, !hasKind(retrieve(&project, "Key memory", 0, 30), "memory"), "Future memory was retrieved")
}

// TestCompilationCacheChecksAllDependencies checks source, rules, model, and evidence dependencies in compilation caching.
func TestCompilationCacheChecksAllDependencies(t *testing.T) {
	project, config := memoryProject(t)
	source := project.Chapters[0].ID
	fingerprint, _ := compilationFingerprint(&project, source, config)
	assert(t, cachedCompilation(&project, source, fingerprint) != nil, "A valid cache entry was missed")
	changed := cloneProject(project)
	changed.Chapters[0].Body += "New clue"
	assert(t, cachedCompilation(&changed, source, fingerprint) == nil, "A source change did not invalidate the cache")
	changed = cloneProject(project)
	changed.WikiPurpose = "Focus on character knowledge"
	assert(t, cachedCompilation(&changed, source, fingerprint) == nil, "A rule change did not invalidate the cache")
	changed = cloneProject(project)
	changed.Facts = []Fact{}
	assert(t, cachedCompilation(&changed, source, fingerprint) == nil, "Missing evidence did not invalidate the cache")
	config.Model = "another-model"
	next, _ := compilationFingerprint(&project, source, config)
	assert(t, cachedCompilation(&project, source, next) == nil, "A model change did not invalidate the cache")
}

// TestMemoryReviewRequiresCurrentApprovedEvidence requires current, author-approved evidence before confirming memory.
func TestMemoryReviewRequiresCurrentApprovedEvidence(t *testing.T) {
	engine := testEngine(t)
	project, _ := memoryProject(t)
	insertProject(t, engine, project)
	args := map[string]any{"project_id": project.ID, "revision": 0, "memory_id": project.Compilations[0].ID, "status": "confirmed"}
	reject(t, engine, "review_memory", args)
	invoke[Project](t, engine, "review_fact", map[string]any{"project_id": project.ID, "revision": 0, "fact_id": project.Facts[0].ID, "status": "confirmed"})
	args["revision"] = 1
	approved := invoke[Project](t, engine, "review_memory", args)
	assert(t, approved.Compilations[0].Status == "confirmed", "Memory confirmation did not take effect")
	invoke[Project](t, engine, "save_wiki_purpose", map[string]any{"project_id": project.ID, "revision": 2, "purpose": "Track information concealed by characters"})
	args["revision"] = 3
	reject(t, engine, "review_memory", args)
}

// TestRelationshipRetrievalObeysReviewAndChapterScope keeps relationship expansion within reviewed evidence and chapter scope.
func TestRelationshipRetrievalObeysReviewAndChapterScope(t *testing.T) {
	project := NewProject("Graph test", "", " ")
	bodies := []string{"Mara knows Theo and hands over the key.", "Theo reads the sailing log alone.", "Mara exchanges letters with Iris."}
	for index, body := range bodies {
		chapter := NewChapter(uint32(index+1), "Chapter")
		chapter.Body = body
		project.Chapters = append(project.Chapters, chapter)
	}
	for index, item := range [][3]string{{"Mara", "Theo", "Hands over the key"}, {"Theo", "", "Reads the log"}, {"Mara", "Iris", "Exchanges letters"}} {
		chapter := project.Chapters[index]
		_, err := ingest(&project, chapter.ID, hashText(chapter.Body), []Candidate{{Entity: item[0], RelatedEntity: item[1], Claim: item[2], Quote: chapter.Body, Kind: "relationship", Epistemic: "explicit"}}, "mock")
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range project.Facts {
		project.Facts[i].Status = "confirmed"
	}
	items := retrieve(&project, "Mara", 2, 30)
	found := false
	for _, item := range items {
		found = found || item.Reason == "Character relationship" && strings.Contains(item.Text, "Reads the log")
		assert(t, !strings.Contains(item.Text, "Exchanges letters"), "Retrieval expanded into future evidence")
	}
	assert(t, found, "Relationship expansion was missing")
	project.Facts[0].Status = "pending"
	for _, item := range retrieve(&project, "Mara", 2, 30) {
		assert(t, !(item.Kind == "wiki" && strings.Contains(item.Text, "Reads the log")), "Retrieval expanded through a pending relationship")
	}
}

// TestStoryLintReportsStructuralGaps distinguishes stale sources, broken links, and planned author entries.
func TestStoryLintReportsStructuralGaps(t *testing.T) {
	project, _ := memoryProject(t)
	project.Facts[0].RelatedEntity = "Missing character"
	project.Chapters[0].Body = "The source text has changed"
	kinds := map[string]bool{}
	for _, finding := range lintWiki(&project) {
		kinds[finding.Kind] = true
		if finding.Kind == "unreferenced_entity" {
			assert(t, finding.Severity == "info", "Author planning was treated as an error")
		}
	}
	assert(t, kinds["broken_relation"] && kinds["stale_evidence"] && kinds["stale_memory"], "Knowledge checks missed structural issues")
}

// TestSimultaneousLocationsAreReviewHints keeps multiple-location notices subject to author interpretation.
func TestSimultaneousLocationsAreReviewHints(t *testing.T) {
	project, _ := memoryProject(t)
	other := project.Facts[0]
	other.ID = newID()
	other.Location = "Coast"
	other.StoryTime = "That night"
	other.Status = "confirmed"
	project.Facts[0].Location = "Lighthouse"
	project.Facts[0].StoryTime = "That night"
	project.Facts[0].Status = "confirmed"
	project.Facts = append(project.Facts, other)
	found := false
	for _, finding := range lintWiki(&project) {
		found = found || finding.Kind == "possible_time_conflict" && finding.Severity == "review"
	}
	assert(t, found, "Time or location review notice was missing")
}

// TestWikiExportContainsStoryStructure checks Wiki indexes, memory, source IDs, and compilation history in exports.
func TestWikiExportContainsStoryStructure(t *testing.T) {
	project, _ := memoryProject(t)
	markdown := renderWiki(&project)
	for _, expected := range []string{"Tracking focus", "Entry index", "Story memory", "Compilation history", "pending", project.Chapters[0].ID, "Lin Wu"} {
		assert(t, strings.Contains(markdown, expected), "Wiki export is missing "+expected)
	}
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

// TestWikiCacheDependsOnBothModels checks cache invalidation after changing only the memory compilation model.
func TestWikiCacheDependsOnBothModels(t *testing.T) {
	project := ExampleProject()
	extraction, synthesis := testProfile("extract").Config, testProfile("compile").Config
	first, err := modelsFingerprint(&project, project.Chapters[0].ID, extraction, extraction)
	if err != nil {
		t.Fatal(err)
	}
	second, err := modelsFingerprint(&project, project.Chapters[0].ID, extraction, synthesis)
	assert(t, err == nil && first != second, "The compilation model was absent from the cache identity")
}

// TestGraphFiltersChaptersReviewAndStaleSources checks chapter, review, and source freshness boundaries in graph projections.
func TestGraphFiltersChaptersReviewAndStaleSources(t *testing.T) {
	project := ExampleProject()
	graph := buildGraph(&project, 1, false)
	chapters := 0
	ids := map[string]bool{}
	for _, node := range graph.Nodes {
		ids[node.ID] = true
		if node.Kind == "chapter" {
			chapters++
		}
	}
	assert(t, chapters == 1, "The graph exposed future sources")
	for _, edge := range graph.Edges {
		assert(t, edge.Status == "confirmed" && ids[edge.Source] && ids[edge.Target], "The graph contained pending or dangling edges")
	}
	all := buildGraph(&project, 3, true)
	pending := false
	for _, edge := range all.Edges {
		pending = pending || edge.Status == "pending"
	}
	assert(t, pending, "Pending evidence layer was missing")
	project.Chapters[0].Body += "Revision"
	stale := buildGraph(&project, 3, true)
	assert(t, stale.OmittedStale == 2, "Incorrect stale evidence count")
	for _, node := range stale.Nodes {
		assert(t, node.SourceID == nil || *node.SourceID != project.Chapters[0].ID, "A stale source remained in the graph")
	}
}

// TestGraphIdentitySurvivesAliasesAndReportsMissingTargets checks stable graph identity through aliases and unresolved targets.
func TestGraphIdentitySurvivesAliasesAndReportsMissingTargets(t *testing.T) {
	project := ExampleProject()
	initial := buildGraph(&project, 3, false)
	project.Entities[0].Aliases = append(project.Entities[0].Aliases, "Lin Wu")
	project.Entities[0].Name = "Lin Xiaowu"
	renamed := buildGraph(&project, 3, false)
	assert(t, reflect.DeepEqual(initial.Edges, renamed.Edges), "Renaming changed edge identity")
	found := false
	for _, node := range renamed.Nodes {
		found = found || node.Label == "Lin Xiaowu" && node.ID == "entity:"+project.Entities[0].ID
	}
	assert(t, found, "Renaming changed node identity")
	project.Facts[2].RelatedEntity = "Uncreated character"
	missing := buildGraph(&project, 3, false)
	assert(t, missing.UnresolvedRelations == 1, "An unresolved relationship was not reported")
	for _, node := range missing.Nodes {
		assert(t, node.Label != "Uncreated character", "A relationship target was fabricated")
	}
}

// TestWritingParametersMigrateAndEnterPrompt checks migrated writing defaults and chapter overrides in outgoing prompts.
func TestWritingParametersMigrateAndEnterPrompt(t *testing.T) {
	project := ExampleProject()
	var old map[string]json.RawMessage
	_ = decodeJSON(rawMessage(project), &old)
	for _, key := range []string{"writing_style", "default_chapter_words", "narrative_pov", "model_overrides"} {
		delete(old, key)
	}
	var migrated Project
	err := decodeJSON(rawMessage(old), &migrated)
	assert(t, err == nil && migrated.DefaultChapterWords == 3000 && chapterTarget(&migrated, &migrated.Chapters[0]) == 3000, "Incorrect writing parameter defaults")
	project.DefaultChapterWords = 4000
	target := uint32(2200)
	project.Chapters[0].TargetWords = &target
	project.WritingStyle = "mystery"
	prompt, err := prepareAI(&project, AIRequest{ProjectID: project.ID, ChapterID: project.Chapters[0].ID, Action: "continue", Instruction: "Advance the investigation", History: []Message{}}, 24000)
	assert(t, err == nil, "Prompt preparation failed")
	user := prompt.Messages[len(prompt.Messages)-1].Content
	for _, part := range []string{"2200", "Mystery", "Respect the viewpoint character's knowledge."} {
		assert(t, strings.Contains(user, part), "Prompt is missing "+part)
	}
}

// TestChapterTargetOverrideAndRestore checks chapter overrides, inheritance, restoration, and rejected lengths.
func TestChapterTargetOverrideAndRestore(t *testing.T) {
	engine := testEngine(t)
	project := ExampleProject()
	insertProject(t, engine, project)
	chapter := project.Chapters[0]
	args := map[string]any{"project_id": project.ID, "revision": 0, "chapter_id": chapter.ID, "title": chapter.Title, "body": chapter.Body, "synopsis": chapter.Synopsis, "status": "draft", "target_words": 4500}
	saved := invoke[Project](t, engine, "save_chapter", args)
	assert(t, saved.Chapters[0].TargetWords != nil && *saved.Chapters[0].TargetWords == 4500, "Chapter length override failed")
	args["revision"] = 1
	args["target_words"] = nil
	inherited := invoke[Project](t, engine, "save_chapter", args)
	assert(t, inherited.Chapters[0].TargetWords == nil, "Chapter length inheritance could not be restored")
	versions := inherited.Chapters[0].Versions
	restored := invoke[Project](t, engine, "restore_version", map[string]any{"project_id": project.ID, "revision": 2, "chapter_id": chapter.ID, "version_id": versions[len(versions)-1].ID})
	assert(t, restored.Chapters[0].TargetWords != nil && *restored.Chapters[0].TargetWords == 4500, "Version restoration lost its length target")
	for _, bad := range []any{0, -1, 50001, 1.5} {
		args["revision"] = 3
		args["target_words"] = bad
		reject(t, engine, "save_chapter", args)
	}
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, current.Chapters[0].Body == chapter.Body && current.Revision == 3, "Invalid values changed the project")
}

// TestGraphAndRetrievalShareIdentityValidation checks shared alias resolution and rejection of duplicate imported entity IDs.
func TestGraphAndRetrievalShareIdentityValidation(t *testing.T) {
	engine := testEngine(t)
	project := ExampleProject()
	project.Entities[1].Aliases = append(project.Entities[1].Aliases, "The keeper")
	project.Facts[2].RelatedEntity = "The keeper"
	project.Facts[1].Claim = "Stops visitors from entering the lighthouse"
	assert(t, buildGraph(&project, 3, false).UnresolvedRelations == 0, "The graph failed to resolve an alias")
	for _, finding := range lintWiki(&project) {
		assert(t, finding.Kind != "broken_relation", "An alias relationship was treated as a broken link")
	}
	found := false
	for _, item := range retrieve(&project, "Lin Wu", 2, 30) {
		found = found || item.Reason == "Character relationship" && strings.Contains(item.Title, "Lu Chuan")
	}
	assert(t, found, "Retrieval failed to resolve an alias")
	project.Entities[1].ID = project.Entities[0].ID
	reject(t, engine, "import_archive", map[string]any{"content": jsonText(map[string]any{"format": "quillweave-1", "project": project})})
	list, err := engine.store.List()
	assert(t, err == nil && len(list) == 0, "An invalid backup left a partial project")
}

// modelServer creates a local fixture server with connection cleanup on test exit.
func modelServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() { server.CloseClientConnections(); server.Close() })
	return server
}

// streamPayload wraps fixture prose in an SSE response.
func streamPayload(text string) string {
	return "data: " + jsonText(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": text}}}}) + "\n\ndata: [DONE]\n\n"
}

// staticModel provides cancellable latency and closes blocked handlers during cleanup.
func staticModel(t *testing.T, payload string, delay time.Duration) *httptest.Server {
	return modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if delay > 0 {
			_, _ = io.Copy(io.Discard, request.Body)
			select {
			case <-time.After(delay):
			case <-request.Context().Done():
				return
			}
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, payload)
	})
}

// sequenceModel returns stage responses in order and records real request counts.
func sequenceModel(t *testing.T, responses []string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	count := &atomic.Int64{}
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		index := int(count.Add(1)) - 1
		text := "{}"
		if index < len(responses) {
			text = responses[index]
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, streamPayload(text))
	})
	return server, count
}

// finishJob waits for a task terminal state with a finite test deadline.
func finishJob(t *testing.T, engine *Engine, job Job) Job {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		current := invoke[Job](t, engine, "get_job", map[string]any{"job_id": job.ID})
		if current.Status != "running" {
			return current
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Task did not finish before the test deadline")
	return Job{}
}

// TestRealHTTPStreamAndCancel checks real HTTP Unicode streaming and cancellation.
func TestRealHTTPStreamAndCancel(t *testing.T) {
	server := staticModel(t, streamPayload("New chapter prose."), 0)
	config := defaultProvider()
	config.Model = "mock"
	config.BaseURL = server.URL + "/v1"
	var received strings.Builder
	output, err := streamModel(context.Background(), config, []Message{{Role: "user", Content: "Continue"}}, func(text string) { received.WriteString(text) })
	assert(t, err == nil && output == "New chapter prose." && received.String() == output, "HTTP stream text disagreed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = streamModel(ctx, config, []Message{}, func(string) {})
	assert(t, err != nil && strings.Contains(err.Error(), "cancelled"), "Cancellation did not take effect")
}

// TestWikiJobRunsEndToEnd checks both Wiki stages, pending review, durable history, and cache reuse.
func TestWikiJobRunsEndToEnd(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	staged := cloneProject(project)
	ingestCandidate(t, &staged)
	memory := jsonText(MemorySection{Summary: "Lin Wu found a letter, beginning the investigation.", EvidenceIDs: []string{staged.Facts[0].ID}, Questions: []string{}})
	server, calls := sequenceModel(t, []string{jsonText(map[string]any{"facts": []Candidate{testCandidate()}}), memory})
	config := defaultProvider()
	config.Model = "mock"
	config.BaseURL = server.URL + "/v1"
	configureModel(t, engine, config)
	args := map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "wiki", "instruction": ""}
	job := invoke[Job](t, engine, "start_ai", args)
	done := finishJob(t, engine, job)
	assert(t, done.Status == "done", "Wiki task failed: "+done.Error)
	updated := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(updated.Facts) == 1 && updated.Facts[0].Status == "pending" && len(updated.Compilations) == 1 && updated.Compilations[0].Status == "pending", "Pending knowledge was incomplete")
	history := invoke[[]Job](t, engine, "get_runs", map[string]any{"project_id": project.ID})
	assert(t, len(history) == 1 && calls.Load() == 2, "Incorrect task history or call count")
	reused := invoke[Job](t, engine, "start_ai", args)
	assert(t, reused.Status == "done" && strings.Contains(reused.Progress, "Reused") && calls.Load() == 2, "The cache was not reused")
}

// TestCancelRunningJobKeepsProjectUnchanged checks project state after cancelling a real waiting request.
func TestCancelRunningJobKeepsProjectUnchanged(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	server := staticModel(t, "data: [DONE]\n\n", 10*time.Second)
	config := defaultProvider()
	config.Model = "mock"
	config.BaseURL = server.URL + "/v1"
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "wiki", "instruction": ""})
	time.Sleep(15 * time.Millisecond)
	invoke[map[string]bool](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
	done := finishJob(t, engine, job)
	assert(t, done.Status == "cancelled", "The running task was not cancelled")
	latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(latest.Facts) == 0 && latest.Revision == 0, "The project was written after cancellation")
}

// TestFailedSecondStageCommitsNothing checks atomic failure when the second compilation stage is invalid.
func TestFailedSecondStageCommitsNothing(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	server, _ := sequenceModel(t, []string{jsonText(map[string]any{"facts": []Candidate{testCandidate()}}), "Not JSON"})
	config := defaultProvider()
	config.Model = "mock"
	config.BaseURL = server.URL + "/v1"
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "wiki", "instruction": ""})
	done := finishJob(t, engine, job)
	assert(t, done.Status == "error", "Invalid memory did not fail")
	latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(latest.Facts) == 0 && len(latest.Compilations) == 0 && latest.Revision == 0, "Failure produced a partial write")
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
	assert(t, calls["writer"] == 1 && calls["extract"] == 1 && calls["compile"] == 1, "The assigned endpoints were not used")
	assert(t, strings.Contains(writerPrompt, "2100") && strings.Contains(writerPrompt, "Mystery"), "Writing parameters were absent from the request")
	view := invoke[settingsResponse](t, engine, "get_model_settings", map[string]any{})
	assert(t, !strings.Contains(jsonText(view), "test-key"), "An API key was returned to the UI")
	latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(latest.Compilations) == 1, "Memory was not saved")
}

// TestJSONContractRejectsNullAndNegative retains null and negative-value rejection across supported JSON schemas.
func TestJSONContractRejectsNullAndNegative(t *testing.T) {
	models := testSettings()
	raw := jsonText(models)
	invalid := strings.Replace(raw, `"routes":{}`, `"routes":{"ask":null}`, 1)
	var decoded ModelSettings
	assert(t, decodeJSON([]byte(invalid), &decoded) != nil, "A null route was treated as inheritance")
	provider := defaultProvider()
	source := jsonText(provider)
	source = strings.Replace(source, `"context_chars":24000`, `"context_chars":-1`, 1)
	assert(t, decodeJSON([]byte(source), &provider) != nil, "A negative context budget was accepted")
	var entity Entity
	assert(t, decodeJSON([]byte(`{"id":"","name":"Character","kind":"character","aliases":[null],"description":""}`), &entity) != nil, "A null alias became an empty string")
	project := testProject()
	var fields map[string]json.RawMessage
	_ = decodeJSON(rawMessage(project), &fields)
	delete(fields, "chapters")
	var missing Project
	assert(t, decodeJSON(rawMessage(fields), &missing) != nil, "A missing legacy required field was accepted")
}

// TestConcurrentRevisionWrites allows one successful write when two engines save the same revision.
func TestConcurrentRevisionWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.sqlite3")
	first, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	project := testProject()
	insertProject(t, first, project)
	var group sync.WaitGroup
	var successes atomic.Int32
	start := make(chan struct{})
	for index := 0; index < 12; index++ {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			engine := first
			if index%2 == 1 {
				engine = second
			}
			_, err := engine.Dispatch("save_chapter", rawMessage(map[string]any{"project_id": project.ID, "revision": 0, "chapter_id": project.Chapters[0].ID, "title": "Concurrent save", "body": fmt.Sprintf("Write %d", index), "synopsis": "", "status": "draft"}))
			if err == nil {
				successes.Add(1)
			}
		}()
	}
	close(start)
	group.Wait()
	assert(t, successes.Load() == 1, "Multiple stale revisions were committed")
	latest := invoke[Project](t, first, "get_project", map[string]any{"project_id": project.ID})
	assert(t, latest.Revision == 1 && len(latest.Chapters[0].Versions) == 1, "Concurrent writes damaged history")
}

// TestJobLimitsAndShutdown checks per-project exclusion, the global task limit, and shutdown cancellation.
func TestJobLimitsAndShutdown(t *testing.T) {
	engine := testEngine(t)
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		<-request.Context().Done()
	})
	config := defaultProvider()
	config.Model = "mock"
	config.BaseURL = server.URL + "/v1"
	configureModel(t, engine, config)
	projects := []Project{}
	jobs := []Job{}
	for index := 0; index < 5; index++ {
		project := testProject()
		insertProject(t, engine, project)
		projects = append(projects, project)
		if index < 4 {
			jobs = append(jobs, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "continue", "instruction": "Wait"}))
		}
	}
	for _, project := range []Project{projects[0], projects[4]} {
		reject(t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "continue", "instruction": "Wait"})
	}
	assert(t, engine.Close() == nil, "Shutdown cleanup failed")
	for _, job := range jobs {
		done := invoke[Job](t, engine, "get_job", map[string]any{"job_id": job.ID})
		assert(t, done.Status == "cancelled", "Tasks remained active after shutdown")
	}
}

// TestWikiRevalidatesRulesBeforeCommit checks tracking-rule freshness before Wiki publication.
func TestWikiRevalidatesRulesBeforeCommit(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	staged := cloneProject(project)
	ingestCandidate(t, &staged)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		index := calls.Add(1)
		text := jsonText(map[string]any{"facts": []Candidate{testCandidate()}})
		if index == 2 {
			close(entered)
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
			text = jsonText(MemorySection{Summary: "Finds a letter", EvidenceIDs: []string{staged.Facts[0].ID}, Questions: []string{}})
		}
		fmt.Fprint(writer, streamPayload(text))
	})
	config := defaultProvider()
	config.Model = "mock"
	config.BaseURL = server.URL + "/v1"
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "wiki", "instruction": ""})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Stage two was not reached")
	}
	invoke[Project](t, engine, "save_wiki_purpose", map[string]any{"project_id": project.ID, "revision": 0, "purpose": "New tracking focus"})
	close(release)
	done := finishJob(t, engine, job)
	assert(t, done.Status == "error" && strings.Contains(done.Error, "Tracking focus changed"), "Analysis used outdated tracking rules")
	latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, latest.Revision == 1 && len(latest.Facts) == 0 && len(latest.Compilations) == 0, "Concurrent revision left partial knowledge")
}

// TestStreamingByteBoundariesAndRedirects checks Unicode byte boundaries and credential-safe redirect rejection.
func TestStreamingByteBoundariesAndRedirects(t *testing.T) {
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		for _, value := range []byte(streamPayload("\u4e2d\u6587🙂\u8de8\u5757")) {
			_, _ = writer.Write([]byte{value})
			writer.(http.Flusher).Flush()
		}
	})
	config := defaultProvider()
	config.Model = "mock"
	config.BaseURL = server.URL
	output, err := streamModel(context.Background(), config, []Message{{Role: "user", Content: "Test"}}, func(string) {})
	assert(t, err == nil && output == "\u4e2d\u6587🙂\u8de8\u5757", "UTF-8 network boundary handling failed")
	var leaked atomic.Bool
	target := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		leaked.Store(true)
		fmt.Fprint(writer, streamPayload("Unexpected request"))
	})
	redirect := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	})
	config.BaseURL = redirect.URL
	config.APIKey = "synthetic-key"
	_, err = streamModel(context.Background(), config, []Message{}, func(string) {})
	assert(t, err != nil && strings.Contains(err.Error(), "302") && !leaked.Load(), "A redirect exposed the model request")
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
