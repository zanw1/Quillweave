//go:build quillweave_whitebox

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestProjectCardsKeepCountsAcrossEditsAndLegacyMetadata exercises the summary API contract.
func TestProjectCardsKeepCountsAcrossEditsAndLegacyMetadata(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	project.Chapters[0].Body = "中文\t a\n😀\u3000b"
	insertProject(t, engine, project)
	for _, legacy := range []bool{false, true} {
		if legacy {
			_, err := engine.store.db.Exec("UPDATE projects SET document=json_remove(document,'$._list_counts') WHERE id=?", project.ID)
			assert(t, err == nil, "Cannot construct a legacy metadata record")
		}
		cards := invoke[[]projectCard](t, engine, "list_projects", nil)
		assert(t, len(cards) == 1 && cards[0].ChapterCount == 1 && cards[0].WordCount == 5, "Project counts differ across cached and legacy metadata")
	}
	chapter := project.Chapters[0]
	project = invoke[Project](t, engine, "save_chapter", map[string]any{"project_id": project.ID, "revision": project.Revision, "chapter_id": chapter.ID, "title": chapter.Title, "body": "updated", "synopsis": "", "status": "draft", "target_words": nil})
	cards := invoke[[]projectCard](t, engine, "list_projects", nil)
	assert(t, cards[0].WordCount == 7 && cards[0].ChapterCount == 1, "Saving a chapter did not refresh the summary")
	var metadata string
	assert(t, engine.store.db.QueryRow("SELECT document FROM projects WHERE id=?", project.ID).Scan(&metadata) == nil && strings.Contains(metadata, "_list_counts"), "A normal save did not rebuild the summary cache")
	loaded := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, loaded.Chapters[0].Body == "updated", "The compact summary changed the full-project API")
}

// TestChapterDeletionRoundTrip exercises the public review path and exact source restoration.
func TestChapterDeletionRoundTrip(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%t", empty), func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			chapter := &project.Chapters[0]
			if empty {
				chapter.Body, chapter.Versions = "", []Version{}
			} else {
				chapter.SetContent("Revised", "A sword rests by the door.", "A quiet scene", "draft", nil)
			}
			id := chapter.ID
			project.Chapters = append(project.Chapters, NewChapter(3, "Keep chapter three"))
			project.Facts = []Fact{{ID: "source-fact", SourceID: id, SourceHash: hashText(chapter.Body), Kind: "event", Entity: "Lin Wu", Quote: chapter.Body, Claim: "A source event", Status: "pending"}}
			project.Compilations = []WikiCompilation{{ID: "compiled", SourceID: id, SourceHash: hashText(chapter.Body), Status: "pending", Sections: []MemorySection{}}}
			project.Studio.Memories = []StoryMemory{{ID: "old-memory", Kind: "fact", Title: "Old observation", Content: "An old observation", Status: "superseded", SourceID: id}, {ID: "new-memory", Kind: "setting", Title: "Later observation", Content: "A later observation", Status: "confirmed", Supersedes: "old-memory"}}
			project.Studio.Locations = []WorldLocation{{ID: "yard", Name: "Yard", Kind: "place", EntityIDs: []string{}, PlanIDs: []string{}, ChapterIDs: []string{id}}}
			original := cloneProject(project)
			insertProject(t, engine, project)
			cs := invoke[ChangeSet](t, engine, "stage_changeset", map[string]any{"project_id": project.ID, "changeset": ChangeSet{Source: "author", Summary: "Delete one chapter", Operations: []ChangeOperation{{TargetKind: TargetChapter, TargetID: id, OpType: OpDelete, AfterValue: json.RawMessage("null")}}}})
			preview := invoke[struct {
				ChangeSet ChangeSet `json:"changeset"`
			}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": cs.ID, "ui_language": "zh-CN"}).ChangeSet
			assert(t, len(preview.Impacts) >= 3, "The deletion preview omitted derived records")
			assert(t, strings.Contains(preview.Impacts[0].Title, "个历史版本") || strings.Contains(preview.Impacts[0].Title, "条来源证据"), "The deletion preview ignored the current interface language")
			args := map[string]any{"project_id": project.ID, "changeset_id": cs.ID, "revision": preview.PreviewRevision, "review_token": preview.ReviewToken}
			deleted := invoke[struct {
				Project Project `json:"project"`
			}](t, engine, "apply_changeset", args).Project
			assert(t, len(deleted.Chapters) == 1 && deleted.Chapters[0].Number == 3, "Deletion renumbered another chapter")
			assert(t, len(deleted.Facts) == 0 && len(deleted.Compilations) == 0 && len(deleted.Studio.Memories) == 1 && deleted.Studio.Memories[0].Supersedes == "", "Deletion retained invalid source references")
			assert(t, len(deleted.Studio.Locations[0].ChapterIDs) == 0, "The deleted chapter remained linked on the map")
			engine.jobs["retry-writer"] = &Job{ID: "retry-writer", ProjectID: project.ID, Status: "running"}
			repeated := invoke[struct {
				Project Project `json:"project"`
			}](t, engine, "apply_changeset", args).Project
			assert(t, repeated.Revision == deleted.Revision, "A completed deletion retry lost its idempotent receipt")
			engine.jobs["retry-writer"].Status = "done"
			var indexed int
			assert(t, engine.store.db.QueryRow("SELECT count(*) FROM story_fts WHERE project_id=? AND part_id=?", project.ID, "chapter:"+id).Scan(&indexed) == nil && indexed == 0, "Deleted text remains searchable")
			restored := invoke[struct {
				Project Project `json:"project"`
			}](t, engine, "undo_changeset", map[string]any{"project_id": project.ID, "changeset_id": cs.ID, "revision": deleted.Revision}).Project
			assert(t, jsonText(restored.Chapters) == jsonText(original.Chapters) && jsonText(restored.Facts) == jsonText(original.Facts) && jsonText(restored.Compilations) == jsonText(original.Compilations), "Undo lost manuscript history or evidence ordering")
			assert(t, jsonText(restored.Studio.Memories) == jsonText(original.Studio.Memories) && jsonText(restored.Studio.Locations) == jsonText(original.Studio.Locations), "Undo lost memory links or map references")
		})
	}
}

// TestChapterDeletionRejectsStaleAndActiveWrites protects current prose while review is open.
func TestChapterDeletionRejectsStaleAndActiveWrites(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	cs := invoke[ChangeSet](t, engine, "stage_changeset", map[string]any{"project_id": project.ID, "changeset": ChangeSet{Source: "author", Operations: []ChangeOperation{{TargetKind: TargetChapter, TargetID: project.Chapters[0].ID, OpType: OpDelete}}}})
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": cs.ID}).ChangeSet
	args := map[string]any{"project_id": project.ID, "changeset_id": cs.ID, "revision": project.Revision, "review_token": preview.ReviewToken}
	job := &Job{ID: "active-writer", ProjectID: project.ID, Status: "running"}
	engine.jobs[job.ID] = job
	assert(t, engine.store.SaveRun(job.ID, project.ID, storedJob(job)) == nil, "Cannot persist the active task")
	reject(t, engine, "apply_changeset", args)
	job.Status = "done"
	assert(t, engine.store.SaveRun(job.ID, project.ID, storedJob(job)) == nil, "Cannot finish the task")
	project = invoke[Project](t, engine, "new_chapter", map[string]any{"project_id": project.ID, "revision": project.Revision, "title": "A later edit"})
	reject(t, engine, "apply_changeset", args)
	loaded := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(loaded.Chapters) == 2, "A rejected deletion changed the story")
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
