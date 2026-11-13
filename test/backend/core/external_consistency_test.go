//go:build quillweave_whitebox

package core

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// confirmExternalCandidate uses the desktop's exact preview token and returns the saved project.
func confirmExternalCandidate(t *testing.T, engine *Engine, projectID, candidateID string) Project {
	t.Helper()
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": projectID, "changeset_id": candidateID})
	result := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "apply_changeset", map[string]any{"project_id": projectID, "changeset_id": candidateID, "revision": preview.ChangeSet.PreviewRevision, "review_token": preview.ChangeSet.ReviewToken})
	return result.Project
}

// TestExternalRecoveryConflicts preserves later edits until each conflict receives an explicit author choice.
func TestExternalRecoveryConflicts(t *testing.T) {
	engine, service, project := externalFixture(t)
	view := externalReviewedDraft(t, service, WritingBeginInput{RequestID: "revert-writing", ChapterID: project.Chapters[0].ID, Instruction: "Continue"})
	view = externalInvoke[externalWritingView](t, service, "writing_finish", externalMutation(view, "revert-finish"))
	applied := confirmExternalCandidate(t, engine, project.ID, view.CandidateID)
	modified := applied
	modified.Chapters[0].SetContent(modified.Chapters[0].Title, "Later author edit", "", "draft", nil)
	if err := engine.store.Save(&modified, applied.Revision); err != nil {
		t.Fatal(err)
	}
	recovery := externalInvoke[ChangeSet](t, service, "prepare_revert", CandidateMutation{RequestID: "recover", CandidateID: view.CandidateID})
	assert(t, recovery.RevertsChangeSetID == view.CandidateID && len(recovery.RevertConflicts) > 0, "Recovery omitted the original identity or later-edit conflict")
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": recovery.ID})
	_, err := engine.Dispatch("apply_changeset", rawMessage(map[string]any{"project_id": project.ID, "changeset_id": recovery.ID, "revision": preview.ChangeSet.PreviewRevision, "review_token": preview.ChangeSet.ReviewToken}))
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "REVERT_CONFLICT", "Unresolved recovery overwrote a later edit")
	var effects []ChangeEffect
	_ = decodeJSON(recovery.Operations[0].AfterValue, &effects)
	for i := range effects {
		effects[i].Resolution = "restore"
	}
	recovery.Operations[0].AfterValue = rawMessage(effects)
	preview = invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": recovery.ID, "operations": recovery.Operations})
	restored := confirmExternalCandidate(t, engine, project.ID, preview.ChangeSet.ID)
	assert(t, restored.Chapters[0].Body == project.Chapters[0].Body && len(restored.Studio.Memories) == 0, "Recovery did not restore prose and derived memory together")
	invoke[any](t, engine, "undo_changeset", map[string]any{"project_id": project.ID, "changeset_id": recovery.ID, "revision": restored.Revision})
	undone := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, undone.Chapters[0].Body == "Later author edit", "Undo of recovery lost the author's later edit")
}

// TestExternalArchiveRestoresSessions verifies v3 streaming backup, source attribution and renewed evidence.
func TestExternalArchiveRestoresSessions(t *testing.T) {
	engine, service, project := externalFixture(t)
	view := externalReviewedDraft(t, service, WritingBeginInput{RequestID: "archive-writing", ChapterID: project.Chapters[0].ID, Instruction: "Continue"})
	exported := invoke[map[string]string](t, engine, "export", map[string]any{"project_id": project.ID, "format": "archive"})
	assert(t, strings.Contains(exported["content"], "quillweave-4") && strings.Contains(exported["content"], view.ID), "Backup omitted external writing")
	imported := invoke[Project](t, engine, "import_archive", map[string]any{"content": exported["content"]})
	var raw string
	if err := engine.store.db.QueryRow("SELECT document FROM writing_sessions WHERE project_id=?", imported.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var session ExternalWritingSession
	_ = decodeJSON([]byte(raw), &session)
	assert(t, session.ID != view.ID && session.Phase == "stale" && session.Run.Draft == view.Draft, "Restored writing lost its draft or retained an active old identity")
	reader, err := OpenExternalService(engine.store.path, ExternalOptions{ProjectID: imported.ID, Client: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	refreshed := externalInvoke[externalWritingView](t, reader, "writing_refresh", WritingMutation{WritingSessionID: session.ID, SessionRevision: session.Revision, RequestID: "restore-refresh"})
	assert(t, refreshed.Phase == "awaiting_review" && refreshed.Draft == view.Draft, "Restored session could not refresh its evidence")
	refreshed = externalInvoke[externalWritingView](t, reader, "writing_submit_review", WritingReviewInput{WritingMutation: externalMutation(refreshed, "restored-review"), DraftHash: refreshed.DraftHash, EvidenceVersion: refreshed.EvidenceVersion, Review: WritingReview{Summary: "Reviewed against restored evidence", Issues: []WritingIssue{}}})
	stored, err := readExternalWritingSession(reader.engine.store.db, imported.ID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	assert(t, len(stored.Drafts[0].Reviews) == 2, "Re-review after refresh erased an earlier review")
}

// TestExternalReviewLimitsAndRequestIdentity covers configurable revision rounds and mismatched review versions.
func TestExternalReviewLimitsAndRequestIdentity(t *testing.T) {
	_, service, project := externalFixture(t)
	limit := 2
	begin := WritingBeginInput{RequestID: "limits", ChapterID: project.Chapters[0].ID, Instruction: "Continue", RevisionLimit: &limit}
	view := externalInvoke[externalWritingView](t, service, "writing_begin", begin)
	begin.Instruction = "Different request"
	_, err := service.BeginWriting(context.Background(), begin)
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "REQUEST_CONFLICT", "A reused request_id changed its meaning")
	var composition WritingComposition
	_ = decodeJSON([]byte(writingCompositionFixture()), &composition)
	view = externalInvoke[externalWritingView](t, service, "writing_submit_composition", WritingCompositionInput{WritingMutation: externalMutation(view, "limits-compose"), Composition: composition})
	for round := 0; round <= limit; round++ {
		view = externalInvoke[externalWritingView](t, service, "writing_submit_draft", WritingDraftInput{WritingMutation: externalMutation(view, fmt.Sprint("draft-", round)), Draft: fmt.Sprint("Lin Wu wraps the ledger in cloth. Round ", round)})
		review := WritingReviewInput{WritingMutation: externalMutation(view, fmt.Sprint("review-", round)), DraftHash: view.DraftHash, EvidenceVersion: view.EvidenceVersion, Review: WritingReview{Summary: "A local expression can improve.", NeedsRevision: true, Issues: []WritingIssue{{Kind: "reading", Scope: "prose", Quote: "Lin Wu", Explanation: "Clarify the gesture", Suggestion: "Use a precise gesture"}}}}
		invalid := review
		invalid.DraftHash = "wrong-version"
		_, err := service.SubmitReview(context.Background(), invalid)
		assert(t, ExternalEnvelope(nil, err).ErrorCode == "REVIEW_VERSION", "A review for another draft was accepted")
		view = externalInvoke[externalWritingView](t, service, "writing_submit_review", review)
	}
	assert(t, view.Phase == "reviewed", "The configured revision limit was ignored")
	view = externalInvoke[externalWritingView](t, service, "writing_finish", externalMutation(view, "limits-finish"))
	candidate := externalInvoke[ChangeSet](t, service, "get_candidate", ExternalReadRequest{ID: view.CandidateID})
	assert(t, candidate.Provenance.Revisions == 2 && candidate.Provenance.Review.NeedsRevision, "Remaining review issues were lost")
}

// TestExternalReadOnlyDoesNotMigrate verifies startup and reads preserve a previous compatible schema version.
func TestExternalReadOnlyDoesNotMigrate(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	if _, err := engine.store.db.Exec("PRAGMA user_version=4"); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenExternalService(engine.store.path, ExternalOptions{ProjectID: project.ID, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	externalInvoke[ExternalPage](t, reader, "list_chapters", ExternalReadRequest{})
	var version int
	if err := engine.store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	assert(t, version == 4, "Read-only startup migrated the database")
}

// TestExternalCommandCatalogCoverage compares registered operations with the actual desktop dispatch cases.
func TestExternalCommandCatalogCoverage(t *testing.T) {
	registered := map[string]bool{}
	for _, command := range CommandCatalog() {
		if registered[command.Name] {
			t.Fatal("Duplicate command", command.Name)
		}
		registered[command.Name] = true
		if command.InputSchema["type"] != "object" {
			t.Fatal("Missing structured input schema", command.Name)
		}
	}
	file, err := parser.ParseFile(token.NewFileSet(), "service.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "Dispatch" {
			continue
		}
		for _, statement := range function.Body.List {
			selection, ok := statement.(*ast.SwitchStmt)
			if !ok {
				continue
			}
			for _, branch := range selection.Body.List {
				for _, expression := range branch.(*ast.CaseClause).List {
					literal, ok := expression.(*ast.BasicLit)
					if !ok {
						continue
					}
					name, _ := strconv.Unquote(literal.Value)
					if contains([]string{"retain_external", "recall_external", "reflect_external"}, name) {
						continue
					}
					if !registered[name] {
						t.Errorf("Desktop operation lacks CLI coverage: %s", name)
					}
				}
			}
		}
	}
}

// TestExternalCredentialIndependentSession proves a missing encryption key cannot block external authorship.
func TestExternalCredentialIndependentSession(t *testing.T) {
	engine, service, project := externalFixture(t)
	config := defaultProvider()
	config.APIKey = "external-workflow-fake-secret"
	configureModel(t, engine, config)
	if err := os.Remove(engine.store.path + ".key"); err != nil {
		t.Fatal(err)
	}
	view := externalReviewedDraft(t, service, WritingBeginInput{RequestID: "no-key", ChapterID: project.Chapters[0].ID, Instruction: "Continue"})
	assert(t, view.Phase == "reviewed", "External writing attempted to decrypt app credentials")
}

// TestExternalCrossProcessCancellation observes cancellation requested by another independent Engine.
func TestExternalCrossProcessCancellation(t *testing.T) {
	engine, _, project := externalFixture(t)
	release := make(chan struct{})
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-release:
		}
	})
	t.Cleanup(func() { close(release) })
	config := defaultProvider()
	config.BaseURL = server.URL
	config.Model = "cancellation-fixture"
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "ask", "instruction": "Wait for cancellation"})
	peer, err := NewEngine(engine.store.path)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	accepted := invoke[map[string]bool](t, peer, "cancel_ai", map[string]any{"job_id": job.ID})
	assert(t, accepted["cancelled"], "The peer could not request cancellation")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current := invoke[Job](t, engine, "get_job", map[string]any{"job_id": job.ID})
		if current.Status != "running" {
			assert(t, current.Status == "cancelled", "Cancellation reached an unexpected terminal state")
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("The owner did not observe the peer cancellation")
}

// TestExternalDeletedSourcesRetainDrafts verifies named source changes and readable candidates after target deletion.
func TestExternalDeletedSourcesRetainDrafts(t *testing.T) {
	engine, service, project := externalFixture(t)
	finished := externalReviewedDraft(t, service, WritingBeginInput{RequestID: "retained-candidate", ChapterID: project.Chapters[0].ID, Instruction: "Continue"})
	finished = externalInvoke[externalWritingView](t, service, "writing_finish", externalMutation(finished, "retained-finish"))
	view := externalInvoke[externalWritingView](t, service, "writing_begin", WritingBeginInput{RequestID: "retained-active", ChapterID: project.Chapters[0].ID, Instruction: "Continue"})
	var composition WritingComposition
	_ = decodeJSON([]byte(writingCompositionFixture()), &composition)
	view = externalInvoke[externalWritingView](t, service, "writing_submit_composition", WritingCompositionInput{WritingMutation: externalMutation(view, "retained-compose"), Composition: composition})
	view = externalInvoke[externalWritingView](t, service, "writing_submit_draft", WritingDraftInput{WritingMutation: externalMutation(view, "retained-draft"), Draft: "A draft retained after deletion."})
	project.Chapters = nil
	if err := engine.store.Save(&project, project.Revision); err != nil {
		t.Fatal(err)
	}
	status := externalInvoke[map[string]any](t, service, "writing_status", WritingSessionRequest{WritingSessionID: view.ID})
	assert(t, status["draft"] == view.Draft && status["sources_changed"] != nil, "Target deletion hid the retained draft or stale-source warning")
	material := externalInvoke[map[string]any](t, service, "writing_material", WritingMaterialRequest{WritingSessionID: view.ID, Kind: "changes"})
	assert(t, strings.Contains(material["text"].(string), view.ChapterID) && strings.Contains(material["text"].(string), `"deleted":true`), "Change details omitted the deleted source identity")
	candidate := externalInvoke[map[string]any](t, service, "get_candidate", ExternalReadRequest{ID: finished.CandidateID})
	assert(t, candidate["preview_error"] != nil && candidate["operations"] != nil, "An invalid preview hid the stored candidate")
}

// TestExternalWaitFailureRetainsTaskIdentity exposes terminal task failures through the CLI envelope.
func TestExternalWaitFailureRetainsTaskIdentity(t *testing.T) {
	_, service, _ := externalFixture(t)
	value, err := waitCLIJob(service, "start_ai", Job{ID: "failed-task", Status: "error", Error: "Fixture provider rejected the request"})
	envelope := ExternalEnvelope(value, err)
	assert(t, !envelope.OK && envelope.ErrorCode == "TASK_FAILED" && envelope.Result != nil, "Waiting on a failed task reported success or lost its identity")
}

// TestExternalChapterOutlineTargets handles both an existing chapter and a reserved new chapter from outlines.
func TestExternalChapterOutlineTargets(t *testing.T) {
	engine, service, project := externalFixture(t)
	project.Studio.Plans = []StoryPlan{
		{ID: "external-plan-existing", Level: "chapter", Title: "Existing ledger", StartChapter: 1, EndChapter: 1, Status: "confirmed", Choice: "Check the records"},
		{ID: "external-plan-new", Level: "chapter", Title: "New ledger", StartChapter: 2, EndChapter: 2, Status: "confirmed", Choice: "Wrap the ledger"},
	}
	if err := engine.store.Save(&project, project.Revision); err != nil {
		t.Fatal(err)
	}
	existing := externalInvoke[externalWritingView](t, service, "writing_begin", WritingBeginInput{RequestID: "plan-existing", PlanID: "external-plan-existing", Instruction: "Continue"})
	assert(t, existing.ChapterID == project.Chapters[0].ID, "Outline selection failed to resolve its existing chapter")
	created := externalReviewedDraft(t, service, WritingBeginInput{RequestID: "plan-create", PlanID: "external-plan-new", Instruction: "Write the new ledger scene"})
	created = externalInvoke[externalWritingView](t, service, "writing_finish", externalMutation(created, "plan-create-finish"))
	candidate := externalInvoke[ChangeSet](t, service, "get_candidate", ExternalReadRequest{ID: created.CandidateID})
	assert(t, len(candidate.Operations) == 3 && candidate.Operations[0].OpType == OpUpsert, "Outline creation lost the chapter, memory or composition candidate")
}

// TestExternalOutlineFilters covers read_outline level, chapter and title filters plus the page budget.
func TestExternalOutlineFilters(t *testing.T) {
	engine, service, project := externalFixture(t)
	long := strings.Repeat("长", 700)
	project.Studio.Plans = []StoryPlan{
		{ID: "book", Level: "book", Title: "Whole book", StartChapter: 1, EndChapter: 2},
		{ID: "unit-1", Level: "unit", Title: "First unit", StartChapter: 1, EndChapter: 2},
		{ID: "ch-1", Level: "chapter", Title: "Old ledger", StartChapter: 1, EndChapter: 1, Goal: long},
		{ID: "ch-2", Level: "chapter", Title: "New ledger", StartChapter: 2, EndChapter: 2, Goal: long},
	}
	if err := engine.store.Save(&project, project.Revision); err != nil {
		t.Fatal(err)
	}
	chapters := externalInvoke[ExternalPage](t, service, "read_outline", ExternalReadRequest{Kind: "chapter"})
	assert(t, chapters.Total == 2, "kind did not filter outline levels")
	single := externalInvoke[ExternalPage](t, service, "read_outline", ExternalReadRequest{ChapterID: project.Chapters[0].ID})
	assert(t, single.Total == 1 && single.Items[0].(map[string]any)["id"] == "ch-1", "chapter_id did not select its chapter outline")
	titled := externalInvoke[ExternalPage](t, service, "read_outline", ExternalReadRequest{Query: "New"})
	assert(t, titled.Total == 1, "query did not filter outline titles")
	budget := externalInvoke[ExternalPage](t, service, "read_outline", ExternalReadRequest{Kind: "chapter", MaxChars: 1500})
	assert(t, len(budget.Items) == 1 && budget.NextCursor != "", "max_chars did not bound the page")
	_, err := service.Execute(context.Background(), "read_outline", rawMessage(ExternalReadRequest{MaxChars: 70000}))
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "INVALID_INPUT", "oversized max_chars was accepted")
}

// TestStalePreviewConfirmsUnchangedTargets lets independent candidates be confirmed one after another and
// still requires a fresh review when another candidate changed the same target first.
func TestStalePreviewConfirmsUnchangedTargets(t *testing.T) {
	engine, service, project := externalFixture(t)
	type review struct {
		ChangeSet ChangeSet `json:"changeset"`
		Project   Project   `json:"project"`
	}
	stage := func(request string, op ChangeOperation) ChangeSet {
		return externalInvoke[ChangeSet](t, service, "stage_changes", StageChangesInput{RequestID: request, BaseRevision: project.Revision, Summary: request, Operations: []ChangeOperation{op}})
	}
	decision := func(id, content string) ChangeOperation {
		return ChangeOperation{ID: id, TargetKind: TargetDecision, OpType: OpUpsert, Title: "Shared rule", AfterValue: rawMessage(map[string]any{"id": "dec_shared", "title": "Shared rule", "content": content})}
	}
	entity := stage("entity", ChangeOperation{ID: "op-entity", TargetKind: TargetEntity, OpType: OpUpsert, Title: "Keeper", AfterValue: rawMessage(map[string]any{"name": "Grave keeper", "kind": "character"})})
	memory := stage("memory", ChangeOperation{ID: "op-memory", TargetKind: TargetMemory, OpType: OpUpsert, Title: "Lifespan rule", AfterValue: rawMessage(map[string]any{"id": "mem_rule", "kind": "rule", "title": "Lifespan rule", "content": "Every borrowed sword costs lifespan."})})
	first := stage("first", decision("op-first", "Version one"))
	second := stage("second", decision("op-second", "Version two"))
	previews := map[string]review{}
	for _, id := range []string{entity.ID, memory.ID, first.ID, second.ID} {
		previews[id] = invoke[review](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": id})
	}
	confirm := func(id string) error {
		cs := previews[id].ChangeSet
		_, err := engine.Dispatch("apply_changeset", rawMessage(map[string]any{"project_id": project.ID, "changeset_id": id, "revision": cs.PreviewRevision, "review_token": cs.ReviewToken}))
		return err
	}
	for _, id := range []string{entity.ID, memory.ID, first.ID} {
		if err := confirm(id); err != nil {
			t.Fatalf("independent candidate %s was blocked by an earlier confirmation: %v", id, err)
		}
	}
	err := confirm(second.ID)
	assert(t, err != nil && strings.Contains(err.Error(), "after this preview"), fmt.Sprintf("a candidate whose target changed was applied without a new review: %v", err))
	fresh := invoke[review](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": second.ID})
	saved := invoke[review](t, engine, "apply_changeset", map[string]any{"project_id": project.ID, "changeset_id": second.ID, "revision": fresh.ChangeSet.PreviewRevision, "review_token": fresh.ChangeSet.ReviewToken}).Project
	decisions := saved.Studio.Decisions
	assert(t, len(saved.Entities) == len(project.Entities)+1 && len(saved.Studio.Memories) == len(project.Studio.Memories)+1, "confirmed candidates did not all reach the story")
	assert(t, decisions[len(decisions)-1].ID == "dec_shared" && decisions[len(decisions)-1].Content == "Version two", "the re-reviewed candidate did not replace the shared decision")
	assert(t, saved.Revision == project.Revision+4, "each confirmation should save exactly one revision")
}

// TestCandidateClearsPlanFieldsExplicitly keeps an explicit empty list or flexibility through staging and
// confirmation, while a field the candidate omits still keeps its stored value.
func TestCandidateClearsPlanFieldsExplicitly(t *testing.T) {
	engine, service, project := externalFixture(t)
	stage := func(request string, revision uint64, after map[string]any) string {
		op := ChangeOperation{ID: "op-" + request, TargetKind: TargetPlan, OpType: OpUpsert, Title: "Book", AfterValue: rawMessage(after)}
		return externalInvoke[ChangeSet](t, service, "stage_changes", StageChangesInput{RequestID: request, BaseRevision: revision, Summary: request, Operations: []ChangeOperation{op}}).ID
	}
	created := confirmExternalCandidate(t, engine, project.ID, stage("create", project.Revision, map[string]any{
		"id": "plan_keep", "level": "book", "title": "Book", "goal": "Keep the graves", "flexibility": "fixed",
		"open_questions": []string{"Who sleeps in the oldest grave?"}, "start_chapter": 1, "end_chapter": 10}))
	cleared := confirmExternalCandidate(t, engine, project.ID, stage("clear", created.Revision, map[string]any{
		"id": "plan_keep", "level": "book", "title": "Book", "flexibility": "", "open_questions": []string{}}))
	var plan StoryPlan
	for _, item := range cleared.Studio.Plans {
		if item.ID == "plan_keep" {
			plan = item
		}
	}
	assert(t, plan.ID == "plan_keep" && len(plan.OpenQuestions) == 0 && plan.Flexibility == "", "explicitly cleared plan fields came back after confirmation: "+jsonText(plan))
	assert(t, plan.Goal == "Keep the graves", "an omitted plan field lost its stored value")
}
