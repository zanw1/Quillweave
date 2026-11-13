//go:build quillweave_whitebox

package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
	project.Chapters[0].Synopsis = "Continue the chosen visit to the lighthouse."
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
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "ask", "instruction": "Continue"})
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
