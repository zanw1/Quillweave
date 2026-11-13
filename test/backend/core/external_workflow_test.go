//go:build quillweave_whitebox

package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

type externalWritingView struct {
	ID              string `json:"writing_session_id"`
	Revision        uint64 `json:"session_revision"`
	Phase           string `json:"phase"`
	ChapterID       string `json:"chapter_id"`
	DraftHash       string `json:"draft_hash"`
	EvidenceVersion string `json:"evidence_version"`
	CandidateID     string `json:"candidate_id"`
	Draft           string `json:"draft"`
}

// externalFixture opens a second connection to an isolated desktop-compatible workspace.
func externalFixture(t *testing.T) (*Engine, *ExternalService, Project) {
	t.Helper()
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	service, err := OpenExternalService(engine.store.path, ExternalOptions{ProjectID: project.ID, Client: "test-agent", Transport: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	return engine, service, project
}

// externalInvoke exercises the shared public transport boundary with typed result decoding.
func externalInvoke[T any](t *testing.T, service *ExternalService, operation string, input any) T {
	t.Helper()
	value, err := service.Execute(context.Background(), operation, rawMessage(input))
	if err != nil {
		t.Fatalf("%s: %v", operation, err)
	}
	var result T
	if err := decodeJSON(rawMessage(value), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// externalMutation binds each next test step to the most recent durable session revision.
func externalMutation(view externalWritingView, request string) WritingMutation {
	return WritingMutation{RequestID: request, WritingSessionID: view.ID, SessionRevision: view.Revision}
}

// externalReviewedDraft completes the real external-only composition, evidence, prose and review steps.
func externalReviewedDraft(t *testing.T, service *ExternalService, begin WritingBeginInput) externalWritingView {
	t.Helper()
	return externalReviewedDraftText(t, service, begin, "Lin Wu wraps the ledger in cloth.")
}

// externalReviewedDraftText runs the same external steps with a caller-chosen draft.
func externalReviewedDraftText(t *testing.T, service *ExternalService, begin WritingBeginInput, draft string) externalWritingView {
	t.Helper()
	view := externalInvoke[externalWritingView](t, service, "writing_begin", begin)
	var composition WritingComposition
	_ = decodeJSON([]byte(writingCompositionFixture()), &composition)
	view = externalInvoke[externalWritingView](t, service, "writing_submit_composition", WritingCompositionInput{WritingMutation: externalMutation(view, begin.RequestID+"-compose"), Composition: composition})
	view = externalInvoke[externalWritingView](t, service, "writing_submit_draft", WritingDraftInput{WritingMutation: externalMutation(view, begin.RequestID+"-draft"), Draft: draft})
	view = externalInvoke[externalWritingView](t, service, "writing_submit_review", WritingReviewInput{WritingMutation: externalMutation(view, begin.RequestID+"-review"), DraftHash: view.DraftHash, EvidenceVersion: view.EvidenceVersion, Review: WritingReview{Summary: "The action follows the evidence.", Issues: []WritingIssue{}, Memories: []StoryMemory{{Kind: "fact", Title: "Ledger wrapped", Content: draft, Quote: draft}}}})
	return view
}

// TestExternalWritingRoundTrip covers zero-credential authorship, replay, desktop acceptance and atomic undo.
func TestExternalWritingRoundTrip(t *testing.T) {
	engine, service, project := externalFixture(t)
	view := externalReviewedDraft(t, service, WritingBeginInput{RequestID: "chapter-one", ChapterID: project.Chapters[0].ID, Instruction: "Continue the ledger scene"})
	input := externalMutation(view, "finish-once")
	view = externalInvoke[externalWritingView](t, service, "writing_finish", input)
	again := externalInvoke[externalWritingView](t, service, "writing_finish", input)
	assert(t, view.CandidateID != "" && again.CandidateID == view.CandidateID, "Finish replay created another candidate")
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, saved.Revision == project.Revision && saved.Chapters[0].Body == project.Chapters[0].Body && len(saved.Studio.ChangeSets) == 1, "External drafting changed formal content")
	assert(t, saved.Studio.ChangeSets[0].Provenance != nil && saved.Studio.ChangeSets[0].Provenance.WritingSessionID == view.ID && len(saved.Studio.ChangeSets[0].Operations) >= 2, "Compound candidate lost provenance or memories")
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": view.CandidateID})
	applied := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "apply_changeset", map[string]any{"project_id": project.ID, "changeset_id": view.CandidateID, "revision": preview.ChangeSet.PreviewRevision, "review_token": preview.ChangeSet.ReviewToken})
	assert(t, strings.Contains(applied.Project.Chapters[0].Body, view.Draft), "Desktop acceptance lost the external draft")
	invoke[any](t, engine, "undo_changeset", map[string]any{"project_id": project.ID, "changeset_id": view.CandidateID, "revision": applied.Project.Revision})
	undone := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, undone.Chapters[0].Body == project.Chapters[0].Body && len(undone.Studio.Memories) == 0, "Undo did not restore the compound boundary")
}

// TestExternalNewChapterAndReadOnly verifies reserved chapter creation and credential-independent read-only startup.
func TestExternalNewChapterAndReadOnly(t *testing.T) {
	engine, service, project := externalFixture(t)
	view := externalReviewedDraft(t, service, WritingBeginInput{RequestID: "new-chapter", Title: "A new ledger", Number: 2, Mode: "create", Instruction: "Write the next scene"})
	view = externalInvoke[externalWritingView](t, service, "writing_finish", externalMutation(view, "new-finish"))
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(saved.Chapters) == 1 && saved.Studio.ChangeSets[0].Operations[0].OpType == OpUpsert, "Reserving a chapter created formal prose")
	reader, err := OpenExternalService(engine.store.path, ExternalOptions{ProjectID: project.ID, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	page := externalInvoke[ExternalPage](t, reader, "list_chapters", ExternalReadRequest{})
	assert(t, page.Total == 1, "Read-only chapter directory was incorrect")
	_, err = reader.FinishWriting(context.Background(), WritingMutation{})
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "READ_ONLY_MODE", "Read-only write was accepted")
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": view.CandidateID})
	invoke[any](t, engine, "apply_changeset", map[string]any{"project_id": project.ID, "changeset_id": view.CandidateID, "revision": preview.ChangeSet.PreviewRevision, "review_token": preview.ChangeSet.ReviewToken})
	result := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(result.Chapters) == 2 && result.Chapters[1].Body == view.Draft, "New chapter candidate failed to materialize")
}

// externalChineseFixture opens an external connection to a Chinese work whose language is left to inherit.
func externalChineseFixture(t *testing.T) (*Engine, *ExternalService, Project) {
	t.Helper()
	engine := testEngine(t)
	project := NewProject("守墓长生", "仙侠", "顾长青在后山守墓，扫一座坟多活三天。")
	chapter := NewChapter(1, "扫墓")
	chapter.Body = "顾长青扫完北坡，冲碑弯了弯腰。"
	project.Chapters = append(project.Chapters, chapter)
	insertProject(t, engine, project)
	service, err := OpenExternalService(engine.store.path, ExternalOptions{ProjectID: project.ID, Client: "test-agent", Transport: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	return engine, service, project
}

// externalCandidateLabel finishes a reviewed session and returns the staged candidate's summary label.
func externalCandidateLabel(t *testing.T, engine *Engine, service *ExternalService, project Project, view externalWritingView) string {
	t.Helper()
	externalInvoke[externalWritingView](t, service, "writing_finish", externalMutation(view, view.ID+"-finish"))
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	return saved.Studio.ChangeSets[0].Summary
}

// TestExternalSessionLanguage gives an inherited Chinese work Chinese labels and the author's banned-phrase
// check, while an explicitly requested language still wins over the work's own text.
func TestExternalSessionLanguage(t *testing.T) {
	clean := "他把扫帚靠在碑边，坐下歇了口气。"
	engine, service, project := externalChineseFixture(t)
	view := externalReviewedDraftText(t, service, WritingBeginInput{RequestID: "zh-clean", ChapterID: project.Chapters[0].ID, Instruction: "续写扫墓"}, clean)
	assert(t, view.Phase == "reviewed", "A clean Chinese draft was sent back for revision")
	assert(t, externalCandidateLabel(t, engine, service, project, view) == "章节正文建议", "An inherited Chinese work received English candidate labels")

	_, service, project = externalChineseFixture(t)
	view = externalReviewedDraftText(t, service, WritingBeginInput{RequestID: "zh-banned", ChapterID: project.Chapters[0].ID, Instruction: "续写扫墓"}, "他扫完最后一级台阶，随即坐下歇了口气。")
	status := externalInvoke[struct {
		Phase  string         `json:"phase"`
		Review *WritingReview `json:"review"`
	}](t, service, "writing_status", WritingSessionRequest{WritingSessionID: view.ID})
	assert(t, status.Phase == "awaiting_revision" && status.Review != nil && len(status.Review.Issues) == 1 && strings.Contains(status.Review.Issues[0].Quote, "随即"), "The author's banned phrase passed an external review")

	engine, service, project = externalChineseFixture(t)
	view = externalReviewedDraftText(t, service, WritingBeginInput{RequestID: "en-explicit", ChapterID: project.Chapters[0].ID, Instruction: "续写扫墓", Language: "en"}, clean)
	assert(t, externalCandidateLabel(t, engine, service, project, view) == "Chapter prose proposal", "An explicitly requested language was ignored")
}

// TestExternalPhaseRefreshAndConcurrentReplay covers stale evidence, rejected order and concurrent idempotency.
func TestExternalPhaseRefreshAndConcurrentReplay(t *testing.T) {
	engine, service, project := externalFixture(t)
	view := externalInvoke[externalWritingView](t, service, "writing_begin", WritingBeginInput{RequestID: "refresh-begin", ChapterID: project.Chapters[0].ID, Instruction: "Continue"})
	_, err := service.SubmitDraft(context.Background(), WritingDraftInput{WritingMutation: externalMutation(view, "too-soon"), Draft: "Prose"})
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "PHASE_ORDER", "Draft skipped composition")
	updated := invoke[Project](t, engine, "update_project", map[string]any{"project_id": project.ID, "revision": project.Revision, "title": "Changed project", "genre": project.Genre, "premise": project.Premise, "style": project.Style, "outline": project.Outline})
	var composition WritingComposition
	_ = decodeJSON([]byte(writingCompositionFixture()), &composition)
	_, err = service.SubmitComposition(context.Background(), WritingCompositionInput{WritingMutation: externalMutation(view, "stale"), Composition: composition})
	assert(t, updated.Revision > project.Revision && ExternalEnvelope(nil, err).ErrorCode == "SOURCES_CHANGED", "Changed evidence was accepted")
	view = externalInvoke[externalWritingView](t, service, "writing_refresh", externalMutation(view, "refresh"))
	input := WritingCompositionInput{WritingMutation: externalMutation(view, "parallel-compose"), Composition: composition}
	var group sync.WaitGroup
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := service.SubmitComposition(context.Background(), input)
			errors <- err
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestExternalStructuredCLI covers stdin JSON and discovery without loading model credentials.
func TestExternalStructuredCLI(t *testing.T) {
	engine, _, project := externalFixture(t)
	var stdout, stderr bytes.Buffer
	code := RunCLIWithInput([]string{"api", "call", "read_chapter", "--creator", "--db", engine.store.path, "--project", project.ID, "--input", "-", "--json"}, strings.NewReader(jsonText(map[string]any{"id": project.Chapters[0].ID})), &stdout, &stderr)
	assert(t, code == 0 && strings.Contains(stdout.String(), "body_hash"), fmt.Sprint(code, stdout.String(), stderr.String()))
	stdout.Reset()
	code = RunCLIWithInput([]string{"api", "list", "--json"}, strings.NewReader(""), &stdout, &stderr)
	assert(t, code == 0 && strings.Contains(stdout.String(), "writing_submit_review"), "Command discovery omitted external review")
	assert(t, os.Getenv("QUILLWEAVE_TEST_CLI_ARGS") == "", "Test inherited a CLI child fixture")
}

// stageFixture seeds one plan, entry and memory so staging tests can update stored records in place.
func stageFixture(t *testing.T) (*Engine, *ExternalService, Project) {
	t.Helper()
	engine := testEngine(t)
	project := testProject()
	project.Studio.Plans = []StoryPlan{{ID: "plan_book", Level: "book", Title: "Outline", Theme: "old theme", Goal: "old goal", Conflict: "old conflict", Pacing: "old pacing", Characters: []string{"A", "B"}, StartChapter: 1, EndChapter: 20, TargetCharacters: 60000, Status: "confirmed", Flexibility: "flexible"}}
	project.Entities = append(project.Entities, Entity{ID: "ent_rule", Name: "Old rule", Kind: "concept", Aliases: []string{"old alias"}, Description: "old description"})
	project.Studio.Memories = append(project.Studio.Memories, StoryMemory{ID: "mem_rule", Kind: "plan", Title: "Old memory", Content: "old content", State: "author_constraint", Status: "confirmed", ThroughChapter: 1400})
	insertProject(t, engine, project)
	service, err := OpenExternalService(engine.store.path, ExternalOptions{ProjectID: project.ID, Client: "test-agent", Transport: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	return engine, service, project
}

// TestExternalStageKeepsOmittedFields covers in-place updates by id, unknown ids, explicit clearing and creation.
func TestExternalStageKeepsOmittedFields(t *testing.T) {
	engine, service, project := stageFixture(t)
	revision := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID}).Revision
	stage := func(request string, operations ...map[string]any) ChangeSet {
		return externalInvoke[ChangeSet](t, service, "stage_changes", map[string]any{"request_id": request, "base_revision": revision, "summary": request, "operations": operations})
	}
	var plan StoryPlan

	// 1. target_id alone selects the stored record and omitted fields keep their values.
	candidate := stage("partial",
		map[string]any{"target_kind": "plan", "op_type": "upsert", "target_id": "plan_book", "title": "Outline", "after_value": map[string]any{"level": "book", "title": "Outline", "theme": "new theme"}},
		map[string]any{"target_kind": "entity", "op_type": "upsert", "target_id": "ent_rule", "title": "Old rule", "after_value": map[string]any{"name": "Old rule", "description": "new description"}},
		map[string]any{"target_kind": "memory", "op_type": "upsert", "target_id": "mem_rule", "title": "Old memory", "after_value": map[string]any{"kind": "plan", "title": "Old memory", "content": "new content"}},
	)
	assert(t, decodeJSON(candidate.Operations[0].PreviewValue, &plan) == nil, "Plan preview was not valid JSON")
	assert(t, plan.ID == "plan_book" && plan.Theme == "new theme", "Plan update did not target the stored plan")
	assert(t, plan.Goal == "old goal" && plan.Conflict == "old conflict" && plan.Pacing == "old pacing" && len(plan.Characters) == 2 && plan.TargetCharacters == 60000 && plan.StartChapter == 1 && plan.EndChapter == 20 && plan.Flexibility == "flexible", "Plan update lost fields the caller omitted")
	assert(t, len(candidate.Operations[0].BeforeValue) > 0, "Plan update recorded no before value")
	var entity Entity
	assert(t, decodeJSON(candidate.Operations[1].PreviewValue, &entity) == nil, "Entry preview was not valid JSON")
	assert(t, entity.ID == "ent_rule" && entity.Kind == "concept" && entity.Description == "new description" && len(entity.Aliases) == 1, "Entry update reset its kind or aliases")
	var memory StoryMemory
	assert(t, decodeJSON(candidate.Operations[2].PreviewValue, &memory) == nil, "Memory preview was not valid JSON")
	assert(t, memory.Supersedes == "mem_rule" && memory.Content == "new content" && memory.State == "author_constraint" && memory.ThroughChapter == 1400, fmt.Sprintf("Memory update reset its state or chapter range: %+v", memory))

	// 2. An id that matches no stored plan is rejected instead of silently creating a new plan.
	_, err := service.StageChanges(context.Background(), StageChangesInput{RequestID: "unknown", BaseRevision: revision, Summary: "unknown", Operations: []ChangeOperation{{TargetKind: TargetPlan, OpType: OpUpsert, TargetID: "plan_missing", Title: "Outline", AfterValue: rawMessage(map[string]any{"level": "book", "title": "Outline"})}}})
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "INVALID_INPUT", "An unknown plan target_id was accepted")

	// 3. A supplied empty value still clears its field.
	cleared := stage("clear", map[string]any{"target_kind": "plan", "op_type": "upsert", "target_id": "plan_book", "title": "Outline", "after_value": map[string]any{"level": "book", "title": "Outline", "theme": ""}})
	assert(t, decodeJSON(cleared.Operations[0].PreviewValue, &plan) == nil && plan.Theme == "" && plan.Goal == "old goal", "An explicit empty value did not clear only its own field")

	// 4. A plan without an id is still created as a new record.
	created := stage("create", map[string]any{"target_kind": "plan", "op_type": "upsert", "title": "Volume one", "after_value": map[string]any{"level": "volume", "title": "Volume one", "parent_id": "plan_book", "start_chapter": 1, "end_chapter": 10}})
	assert(t, decodeJSON(created.Operations[0].PreviewValue, &plan) == nil && plan.ID != "" && plan.ID != "plan_book" && len(created.Operations[0].BeforeValue) == 0, "A plan without an id was not created as a new record")

	// 5. Confirming the reviewed candidate stores the merged records without duplicating them.
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": candidate.ID})
	invoke[any](t, engine, "apply_changeset", map[string]any{"project_id": project.ID, "changeset_id": candidate.ID, "revision": preview.ChangeSet.PreviewRevision, "review_token": preview.ChangeSet.ReviewToken})
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(saved.Studio.Plans) == 1 && saved.Studio.Plans[0].Theme == "new theme" && saved.Studio.Plans[0].Goal == "old goal" && saved.Studio.Plans[0].TargetCharacters == 60000, "The confirmed plan lost omitted fields or was duplicated")
	assert(t, len(saved.Entities) == 1 && saved.Entities[0].Kind == "concept" && saved.Entities[0].Description == "new description", "The confirmed entry lost omitted fields")
	current, archived := StoryMemory{}, 0
	for _, stored := range saved.Studio.Memories {
		if stored.Status == "confirmed" && stored.Title == "Old memory" {
			current = stored
		}
		if stored.ID == "mem_rule" && stored.Status == "superseded" {
			archived++
		}
	}
	assert(t, current.Content == "new content" && current.Supersedes == "mem_rule" && current.State == "author_constraint" && current.ThroughChapter == 1400 && archived == 1, fmt.Sprintf("The confirmed memory edit was not stored as a new version with its metadata: %+v", current))
}

// outlineWritingView adds the adopted composition to the shared writing view.
type outlineWritingView struct {
	externalWritingView
	Source      string              `json:"composition_source"`
	Composition *WritingComposition `json:"composition"`
}

// TestExternalOutlineCardsReused adopts confirmed outline scene cards like in-app writing, follows later outline
// edits on refresh, and still lets the agent replace the cards before drafting.
func TestExternalOutlineCardsReused(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	project.Studio.Plans = []StoryPlan{{ID: "plan_ch2", Level: "chapter", Title: "The ferry", Goal: "Bring the ledger home", StartChapter: 2, EndChapter: 2, Status: "confirmed", SceneCards: []WritingScene{writingSceneFixture()}}}
	insertProject(t, engine, project)
	service, err := OpenExternalService(engine.store.path, ExternalOptions{ProjectID: project.ID, Client: "test-agent", Transport: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	begun := externalInvoke[outlineWritingView](t, service, "writing_begin", WritingBeginInput{RequestID: "outline-begin", PlanID: "plan_ch2", Instruction: "Write chapter two"})
	assert(t, begun.Phase == "awaiting_draft" && begun.Source == "chapter_outline", fmt.Sprintf("Confirmed outline cards were not adopted: %s %q", begun.Phase, begun.Source))
	assert(t, begun.Composition != nil && begun.Composition.Intent == "Bring the ledger home" && jsonText(begun.Composition.SceneCards) == jsonText(project.Studio.Plans[0].SceneCards), "The adopted composition differs from the outline")
	stored := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	edited := stored.Studio.Plans[0]
	edited.SceneCards = []WritingScene{writingSceneFixture()}
	edited.SceneCards[0].Goal = "Cross before the tide"
	invoke[Project](t, engine, "save_plan", map[string]any{"project_id": project.ID, "revision": stored.Revision, "plan": edited})
	refreshed := externalInvoke[outlineWritingView](t, service, "writing_refresh", externalMutation(begun.externalWritingView, "outline-refresh"))
	assert(t, refreshed.Phase == "awaiting_draft" && refreshed.Source == "chapter_outline" && refreshed.Composition.SceneCards[0].Goal == "Cross before the tide", "Refresh kept stale outline cards")
	var composition WritingComposition
	_ = decodeJSON([]byte(writingCompositionFixture()), &composition)
	replaced := externalInvoke[outlineWritingView](t, service, "writing_submit_composition", WritingCompositionInput{WritingMutation: externalMutation(refreshed.externalWritingView, "outline-replace"), Composition: composition})
	assert(t, replaced.Phase == "awaiting_draft" && replaced.Source == "" && replaced.Composition.Intent == composition.Intent, "The agent could not replace adopted cards before drafting")
}

// candidateReviewWindow is one review_candidate window.
type candidateReviewWindow struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	Next      int    `json:"next_offset"`
	Token     string `json:"confirm_token"`
}

// TestExternalConfirmCandidate applies a candidate only with the token of its complete, current review: a forged
// token or an edit to a reviewed record is refused, an unrelated save is not, and replays apply nothing twice.
func TestExternalConfirmCandidate(t *testing.T) {
	engine, service, project := stageFixture(t)
	current := func() Project {
		return invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	}
	candidate := externalInvoke[ChangeSet](t, service, "stage_changes", map[string]any{"request_id": "theme", "base_revision": current().Revision, "summary": "Sharpen the theme", "operations": []map[string]any{
		{"target_kind": "plan", "op_type": "upsert", "target_id": "plan_book", "title": "Outline", "after_value": map[string]any{"level": "book", "title": "Outline", "theme": "new theme"}},
		{"target_kind": "entity", "op_type": "upsert", "target_id": "ent_rule", "title": "Old rule", "after_value": map[string]any{"name": "Old rule", "description": "new description"}},
		{"target_kind": "memory", "op_type": "upsert", "target_id": "mem_rule", "title": "Old memory", "after_value": map[string]any{"kind": "plan", "title": "Old memory", "content": "new content"}},
	}})
	review := func(offset, size int) candidateReviewWindow {
		return externalInvoke[candidateReviewWindow](t, service, "review_candidate", ExternalReadRequest{ID: candidate.ID, Offset: offset, MaxChars: size})
	}
	confirm := func(request, token string) (any, error) {
		return service.Execute(context.Background(), "confirm_candidate", rawMessage(CandidateConfirmation{RequestID: request, CandidateID: candidate.ID, ConfirmToken: token}))
	}
	full := review(0, 0)
	assert(t, !full.Truncated && full.Token != "" && strings.Contains(full.Text, "【1/3】修改 · 分层细纲 · Outline") && strings.Contains(full.Text, "原：old theme") && strings.Contains(full.Text, "新：new theme") && strings.Contains(full.Text, "【3/3】修改 · 故事记忆") && strings.Contains(full.Text, "原：old content") && !strings.Contains(full.Text, "old goal"), full.Text)
	first := review(0, 10)
	assert(t, first.Truncated && first.Token == "" && review(first.Next, 60000).Token == full.Token, "A partial review exposed the confirm token")
	_, err := confirm("forged", "forged")
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "CANDIDATE_CHANGED", "A forged token confirmed the candidate")

	// An author edit to the reviewed record needs a new review; the old approval no longer applies.
	stored := current()
	plan := stored.Studio.Plans[0]
	plan.Theme = "author theme"
	invoke[Project](t, engine, "save_plan", map[string]any{"project_id": project.ID, "revision": stored.Revision, "plan": plan})
	_, err = confirm("stale", full.Token)
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "CANDIDATE_CHANGED" && current().Studio.Plans[0].Theme == "author theme", "A stale approval was applied")
	again := review(0, 0)
	assert(t, again.Token != full.Token && strings.Contains(again.Text, "原：author theme"), "The new review did not show the author's edit")

	// An unrelated save keeps the reviewed text, so the approval still holds.
	stored = current()
	invoke[Project](t, engine, "update_project", map[string]any{"project_id": project.ID, "revision": stored.Revision, "title": "Renamed project", "genre": stored.Genre, "premise": stored.Premise, "style": stored.Style, "outline": stored.Outline})
	input := CandidateConfirmation{RequestID: "approved", CandidateID: candidate.ID, ConfirmToken: again.Token}
	applied := externalInvoke[map[string]any](t, service, "confirm_candidate", input)
	saved := current()
	assert(t, applied["status"] == "applied" && saved.Studio.Plans[0].Theme == "new theme" && saved.Entities[0].Description == "new description" && saved.Title == "Renamed project", fmt.Sprint(applied))
	replayed := externalInvoke[map[string]any](t, service, "confirm_candidate", input)
	assert(t, jsonText(replayed) == jsonText(applied) && current().Revision == saved.Revision, "A replayed confirmation applied the candidate twice")
	assert(t, !strings.Contains(jsonText(applied)+jsonText(again), "review_token"), "Confirmation exposed the internal review token")
	_, err = service.Execute(context.Background(), "review_candidate", rawMessage(ExternalReadRequest{ID: candidate.ID}))
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "INVALID_INPUT", "An applied candidate was offered for confirmation again")

	reader, err := OpenExternalService(engine.store.path, ExternalOptions{ProjectID: project.ID, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	_, err = reader.Execute(context.Background(), "confirm_candidate", rawMessage(input))
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "READ_ONLY_MODE", "A read-only connection confirmed a candidate")
}
