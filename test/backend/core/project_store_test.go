//go:build quillweave_whitebox

package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

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

// TestDeleteProjectRemovesOnlyOwnedData checks aggregate deletion, history cleanup, and surviving settings.
func TestDeleteProjectRemovesOnlyOwnedData(t *testing.T) {
	engine := testEngine(t)
	project, other := ExampleProject(), testProject()
	insertProject(t, engine, project)
	insertProject(t, engine, other)
	settings := jsonText(invoke[settingsResponse](t, engine, "get_model_settings", nil))
	for _, item := range []Project{project, other} {
		job := &Job{ID: newID(), ProjectID: item.ID, Status: "done", Text: "Retained task output"}
		engine.jobs[job.ID] = job
		if err := engine.store.SaveRun(job.ID, item.ID, storedJob(job)); err != nil {
			t.Fatal(err)
		}
	}
	result := invoke[map[string]bool](t, engine, "delete_project", map[string]any{"project_id": project.ID, "revision": project.Revision})
	assert(t, result["deleted"], "Deletion did not report success")
	reject(t, engine, "get_project", map[string]any{"project_id": project.ID})
	projects := invoke[[]map[string]any](t, engine, "list_projects", nil)
	assert(t, len(projects) == 1 && projects[0]["id"] == other.ID, "Deletion affected another project")
	assert(t, jsonText(invoke[Project](t, engine, "get_project", map[string]any{"project_id": other.ID})) == jsonText(other), "Another project's content changed")
	assert(t, len(invoke[[]Job](t, engine, "get_runs", map[string]any{"project_id": project.ID})) == 0, "Deleted history remains")
	assert(t, len(invoke[[]Job](t, engine, "get_runs", map[string]any{"project_id": other.ID})) == 1, "Another project's history was deleted")
	assert(t, settings == jsonText(invoke[settingsResponse](t, engine, "get_model_settings", nil)), "Deletion changed model settings")
	for _, job := range engine.jobs {
		assert(t, job.ProjectID != project.ID, "Deleted project remains in the job cache")
	}
	assert(t, engine.store.SaveRun(newID(), project.ID, `{"status":"done"}`) != nil, "A late task recreated orphan history")
	reject(t, engine, "delete_project", map[string]any{"project_id": project.ID, "revision": project.Revision})
}

// TestDeleteProjectRejectsInvalidArguments checks malformed and stale requests retain the entire story.
func TestDeleteProjectRejectsInvalidArguments(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	cases := []map[string]any{
		{},
		{"project_id": project.ID},
		{"project_id": project.ID, "revision": nil},
		{"project_id": project.ID, "revision": -1},
		{"project_id": project.ID, "revision": 0.5},
		{"project_id": project.ID, "revision": "0"},
		{"project_id": project.ID, "revision": project.Revision + 1},
		{"project_id": 17, "revision": project.Revision},
		{"project_id": "", "revision": project.Revision},
		{"project_id": "missing", "revision": project.Revision},
	}
	for _, args := range cases {
		reject(t, engine, "delete_project", args)
		assert(t, jsonText(invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})) == jsonText(project), "A rejected deletion changed the project")
	}
}

// TestDeleteProjectWaitsForRunningTasks checks active, cancelled-but-unfinished, and committing jobs.
func TestDeleteProjectWaitsForRunningTasks(t *testing.T) {
	for _, committing := range []bool{false, true} {
		t.Run(fmt.Sprintf("committing=%t", committing), func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			insertProject(t, engine, project)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			job := &Job{ID: newID(), ProjectID: project.ID, Status: "running", ctx: ctx, cancel: cancel, committing: committing}
			engine.jobs[job.ID] = job
			args := map[string]any{"project_id": project.ID, "revision": project.Revision}
			reject(t, engine, "delete_project", args)
			assert(t, ctx.Err() == nil, "A delete attempt cancelled a running task")
			cancel()
			reject(t, engine, "delete_project", args)
			assert(t, len(invoke[[]map[string]any](t, engine, "list_projects", nil)) == 1, "An unfinished task lost its project")
			job.Status = "cancelled"
			invoke[map[string]bool](t, engine, "delete_project", args)
			reject(t, engine, "get_job", map[string]any{"job_id": job.ID})
		})
	}
}

// TestDeleteProjectRollsBackHistory checks a failing project delete also restores its task records.
func TestDeleteProjectRollsBackHistory(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	if err := engine.store.SaveRun("kept-run", project.ID, `{"status":"done","text":"keep me"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.store.db.Exec("CREATE TRIGGER reject_project_delete BEFORE DELETE ON projects BEGIN SELECT RAISE(ABORT, 'simulated delete failure'); END"); err != nil {
		t.Fatal(err)
	}
	reject(t, engine, "delete_project", map[string]any{"project_id": project.ID, "revision": project.Revision})
	assert(t, jsonText(invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})) == jsonText(project), "A failed deletion changed the story")
	assert(t, len(invoke[[]Job](t, engine, "get_runs", map[string]any{"project_id": project.ID})) == 1, "A failed deletion lost its task history")
	if _, err := engine.store.db.Exec("DROP TRIGGER reject_project_delete"); err != nil {
		t.Fatal(err)
	}
	invoke[map[string]bool](t, engine, "delete_project", map[string]any{"project_id": project.ID, "revision": project.Revision})
}

// TestProjectDeleteSerializesWithConcurrentEdits checks two instances cannot delete newly saved work.
func TestProjectDeleteSerializesWithConcurrentEdits(t *testing.T) {
	for attempt := 0; attempt < 8; attempt++ {
		t.Run(fmt.Sprint(attempt), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "concurrent.sqlite3")
			first, err := NewEngine(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = first.Close() })
			second, err := NewEngine(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = second.Close() })
			project := testProject()
			insertProject(t, first, project)
			if err := first.store.SaveRun("history", project.ID, `{"status":"done"}`); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() {
				<-start
				_, err := first.Dispatch("delete_project", rawMessage(map[string]any{"project_id": project.ID, "revision": project.Revision}))
				results <- err
			}()
			go func() {
				<-start
				next := cloneProject(project)
				next.Title = "A newly saved title"
				results <- second.store.Save(&next, project.Revision)
			}()
			close(start)
			one, two := <-results, <-results
			assert(t, (one == nil) != (two == nil), "An edit and a stale deletion both succeeded, or both failed")
			remaining, err := first.store.Get(project.ID)
			runs, runErr := first.store.Runs(project.ID)
			assert(t, runErr == nil, "Task history could not be checked")
			if err == nil {
				assert(t, remaining.Title == "A newly saved title" && remaining.Revision == project.Revision+1 && len(runs) == 1, "A rejected delete affected the concurrent save")
			} else {
				assert(t, len(runs) == 0, "Deleting a project left its task history behind")
			}
		})
	}
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
