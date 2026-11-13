//go:build quillweave_whitebox

package core

import (
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestCreativePartnerDefersUnchosenWritingDefaults prevents agent mode from reintroducing the old setup barrier.
func TestCreativePartnerDefersUnchosenWritingDefaults(t *testing.T) {
	p := NewProject("Undecided", "", "")
	for _, action := range []string{"ask", "agent", "naming", "research"} {
		prompt, err := prepareAI(&p, AIRequest{Action: action, Instruction: "Help me find a story."}, 24000)
		assert(t, err == nil, fmt.Sprint(err))
		text := jsonText(prompt.Messages)
		assert(t, !strings.Contains(text, "Target chapter length is about") && !strings.Contains(text, "Narrative viewpoint: Third-person limited"), "Unchosen writing defaults constrained idea exploration")
	}
}

// TestLargeCastContextPrioritizesSceneState keeps the relevant character and rules within a focused model budget.
func TestLargeCastContextPrioritizesSceneState(t *testing.T) {
	p := testProject()
	p.Chapters[0].Number = 30
	for i := 0; i < 2000; i++ {
		p.Entities = append(p.Entities, Entity{ID: newID(), Name: fmt.Sprintf("Unrelated%d", i), Kind: "character", Description: strings.Repeat("Distant unrelated biography. ", 60)})
		p.Studio.Memories = append(p.Studio.Memories, StoryMemory{ID: newID(), Kind: "character", Entity: fmt.Sprintf("Other%d", i), Title: "Past state", Content: strings.Repeat("Unrelated background. ", 60), Status: "confirmed", State: "active", Chapter: 1})
	}
	p.Studio.Memories = append(p.Studio.Memories, StoryMemory{ID: "current-state", Kind: "character", Entity: "Lin Wu", Title: "Current state", Content: "CRITICAL_SCENE_STATE", Status: "confirmed", Chapter: 29}, StoryMemory{ID: "hard-rule", Kind: "rule", Title: "Cost", Content: "MANDATORY_WORLD_CONSTRAINT", Status: "confirmed"})
	prompt, err := prepareAI(&p, AIRequest{ChapterID: p.Chapters[0].ID, Action: "continue", Instruction: "Continue the scene"}, 7000)
	assert(t, err == nil, fmt.Sprint(err))
	text := jsonText(prompt.Messages)
	assert(t, strings.Contains(text, "CRITICAL_SCENE_STATE") && strings.Contains(text, "MANDATORY_WORLD_CONSTRAINT") && !strings.Contains(text, "Distant unrelated biography"), "Unrelated old records crowded out scene-relevant state")
}

// TestStudioLanguageAcrossProtocols verifies actual outgoing prompts and durable turns through all adapters.
func TestStudioLanguageAcrossProtocols(t *testing.T) {
	for _, protocol := range []string{"openai", "responses", "anthropic", "gemini", "gemini_generate", "ollama"} {
		t.Run(protocol, func(t *testing.T) {
			engine := testEngine(t)
			p := testProject()
			insertProject(t, engine, p)
			var calls atomic.Int32
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := decodeJSON(readBodyForStudioTest(t, r), &body); err != nil {
					t.Error(err)
				}
				if !strings.Contains(jsonText(body), "简体中文") {
					t.Error("Chinese response language missing from provider request")
				}
				calls.Add(1)
				fmt.Fprint(w, nativeStreamFixture(protocol, "我们先比较人物欲望和故事冲突。"))
			})
			config := nativeConfig(protocol)
			config.BaseURL = server.URL
			configureModel(t, engine, config)
			session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
			request := map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "request_id": "stable-request", "action": "agent", "instruction": "我想写一个城市失去记忆的故事", "ui_language": "zh-CN"}
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", request))
			assert(t, job.Status == "done", job.Error)
			duplicate := invoke[Job](t, engine, "start_ai", request)
			assert(t, duplicate.ID == job.ID && calls.Load() == 1, "Idempotent retry repeated a model call")
			stored := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
			assert(t, len(stored.Turns) == 2 && stored.Turns[1].Content == job.Text, "Conversation was not persisted")
		})
	}
}

// TestConversationForkUsesOnlySelectedPrefix checks model-visible branch history and non-destructive rewind.
func TestConversationForkUsesOnlySelectedPrefix(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	server, _ := sequenceModel(t, []string{"FIRST_CONTEXT", "SECOND_FUTURE", "NEW_BRANCH"})
	config := defaultProvider()
	config.BaseURL = server.URL
	config.Model = "fixture"
	configureModel(t, engine, config)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
	for _, text := range []string{"first question", "second future question"} {
		done := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "action": "ask", "instruction": text, "session_id": session.ID, "request_id": newID()}))
		assert(t, done.Status == "done", done.Error)
	}
	session = invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	branch := invoke[Conversation](t, engine, "fork_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID, "turn_id": session.Turns[2].ID})
	assert(t, len(branch.Turns) == 2 && branch.Draft == "second future question" && branch.ParentID == session.ID, "Incorrect branch prefix or restored prompt")
	prompt := invoke[Prompt](t, engine, "prepare_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "action": "ask", "instruction": "replacement", "session_id": branch.ID})
	assert(t, strings.Contains(jsonText(prompt), "FIRST_CONTEXT") && !strings.Contains(jsonText(prompt), "SECOND_FUTURE"), "A rewound branch leaked its old future")
	other := invoke[Project](t, engine, "create_project", map[string]any{"title": "Other", "genre": "", "premise": ""})
	reject(t, engine, "get_conversation", map[string]any{"project_id": other.ID, "session_id": session.ID})
	old := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	assert(t, len(old.Turns) == 4, "Rewind destroyed the original branch")
}

// TestStudioPlanMemoryAndTemporalState proves planning materialization, source checks and historical character state.
func TestStudioPlanMemoryAndTemporalState(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Chapters[0].Body = "Lin Wu is alive and holds the brass key."
	second := NewChapter(12, "The cost")
	second.Body = "Lin Wu died while protecting the bridge."
	p.Chapters = append(p.Chapters, second)
	insertProject(t, engine, p)
	state := p.Studio
	state.TargetCharacters = 1_000_000
	state.TargetChapters = 500
	state.Mode = "serial"
	p = invoke[Project](t, engine, "save_studio", map[string]any{"project_id": p.ID, "revision": p.Revision, "studio": state})
	assert(t, len(studioWarnings(&p)) > 0, "Conflicting length targets were silently accepted without a warning")
	p = invoke[Project](t, engine, "save_plan", map[string]any{"project_id": p.ID, "revision": p.Revision, "plan": StoryPlan{Level: "volume", Title: "First volume", StartChapter: 1, EndChapter: 20, Status: "confirmed"}})
	parent := p.Studio.Plans[0].ID
	p = invoke[Project](t, engine, "save_plan", map[string]any{"project_id": p.ID, "revision": p.Revision, "plan": StoryPlan{Level: "chapter", ParentID: parent, Title: "A new choice", Goal: "Find the key", Conflict: "The bridge is guarded", StartChapter: 2, EndChapter: 2, TargetCharacters: 2000, Status: "confirmed"}})
	p = invoke[Project](t, engine, "create_planned_chapters", map[string]any{"project_id": p.ID, "revision": p.Revision})
	assert(t, len(p.Chapters) == 3 && p.Chapters[1].Number == 2 && strings.Contains(p.Chapters[1].Synopsis, "Find the key"), "Chapter plan was not connected to the editor")
	memory := StoryMemory{Kind: "character", Entity: "Lin Wu", Title: "Life", Content: "Alive", State: "alive", Status: "confirmed", Chapter: 1, SourceID: p.Chapters[0].ID, Quote: "Lin Wu is alive"}
	p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": memory})
	memory = p.Studio.Memories[0]
	memory.Content = "Died"
	memory.State = "dead"
	memory.Chapter = 12
	memory.SourceID = second.ID
	memory.SourceHash = ""
	memory.Quote = "Lin Wu died"
	p = invoke[Project](t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": memory})
	early := jsonText(memoryContext(&p, 6))
	late := jsonText(memoryContext(&p, 20))
	assert(t, strings.Contains(early, "Alive") && !strings.Contains(early, "Died") && strings.Contains(late, "Died"), "Character state did not respect narrative time")
	reject(t, engine, "save_memory", map[string]any{"project_id": p.ID, "revision": p.Revision, "memory": StoryMemory{Kind: "fact", Title: "Invented", Content: "Invented", SourceID: second.ID, Quote: "not actually written"}})
	source := p.Chapters[0]
	source.Body = "The earlier scene was rewritten."
	p = invoke[Project](t, engine, "save_chapter", map[string]any{"project_id": p.ID, "revision": p.Revision, "chapter_id": source.ID, "title": source.Title, "body": source.Body, "synopsis": source.Synopsis, "status": source.Status})
	assert(t, !strings.Contains(jsonText(memoryContext(&p, 6)), "Alive"), "A changed source left stale memory active")
}

// TestNormalizedMigrationAndRollback preserves legacy revisions and rejects partial multi-table writes.
func TestNormalizedMigrationAndRollback(t *testing.T) {
	file := filepath.Join(t.TempDir(), "legacy.sqlite3")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	p := testProject()
	p.Revision = 7
	if _, err = db.Exec("CREATE TABLE projects(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,document TEXT NOT NULL); PRAGMA user_version=1;"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO projects VALUES(?,?,?)", p.ID, p.Revision, jsonText(p)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	engine, err := NewEngine(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	migrated := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, migrated.Revision == 7 && migrated.Chapters[0].Body == p.Chapters[0].Body, "Migration lost text or changed its revision")
	var raw string
	if err := engine.store.db.QueryRow("SELECT document FROM projects WHERE id=?", p.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	assert(t, !strings.Contains(raw, p.Chapters[0].Body), "Large prose remained in the metadata blob")
	if _, err := engine.store.db.Exec("CREATE TRIGGER refuse_part_update BEFORE UPDATE ON project_parts BEGIN SELECT RAISE(ABORT,'fixture write failure'); END"); err != nil {
		t.Fatal(err)
	}
	chapter := migrated.Chapters[0]
	reject(t, engine, "save_chapter", map[string]any{"project_id": p.ID, "revision": migrated.Revision, "chapter_id": chapter.ID, "title": chapter.Title, "body": "Changed text", "synopsis": chapter.Synopsis, "status": "draft"})
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, current.Revision == 7 && current.Chapters[0].Body == chapter.Body, "Failed normalized write partially committed")
}

// TestLargeStoryIncrementalSaveAndRetrieval measures real two-million-character storage without leaving artifacts.
func TestLargeStoryIncrementalSaveAndRetrieval(t *testing.T) {
	engine := testEngine(t)
	p := NewProject("Long work", "", "sources")
	text := strings.Repeat("山城的钟声回荡。", 500)
	for i := 1; i <= 500; i++ {
		chapter := NewChapter(uint32(i), fmt.Sprintf("Chapter %d", i))
		chapter.Body = text
		if i == 7 {
			chapter.Body += "铜钥匙藏在北塔。"
		}
		p.Chapters = append(p.Chapters, chapter)
	}
	started := time.Now()
	insertProject(t, engine, p)
	t.Logf("Stored %d characters across 500 chapters in %s", wordCount(text)*500, time.Since(started))
	hits, err := engine.store.SearchPassages(p.ID, "铜钥匙", 20)
	if err != nil {
		t.Fatal(err)
	}
	assert(t, len(hits) > 0 && strings.Contains(hits[0].Text, "北塔"), "Large Chinese full-text retrieval missed its source")
	future, err := engine.store.SearchPassages(p.ID, "铜钥匙", 3)
	assert(t, err == nil && len(future) == 0, "Full-text search leaked future chapters")
	if _, err := engine.store.db.Exec("CREATE TABLE part_audit(id TEXT); CREATE TRIGGER count_part_update AFTER UPDATE ON project_parts BEGIN INSERT INTO part_audit VALUES(new.id); END"); err != nil {
		t.Fatal(err)
	}
	p.Chapters[0].Body += "Only this chapter changed."
	started = time.Now()
	if err := engine.store.Save(&p, p.Revision); err != nil {
		t.Fatal(err)
	}
	var changed int
	engine.store.db.QueryRow("SELECT COUNT(*) FROM part_audit").Scan(&changed)
	assert(t, changed == 1, "Saving one chapter rewrote unrelated records")
	t.Logf("Incremental save of one chapter: %s", time.Since(started))
}

// TestWritingMemoryCandidatesRetainEvidenceAndUncertainty binds actual deltas without promoting a located quotation.
func TestWritingMemoryCandidatesRetainEvidenceAndUncertainty(t *testing.T) {
	project := testProject()
	chapter := &project.Chapters[0]
	project.Entities = []Entity{{ID: "lin", Name: "Lin Wu", Aliases: []string{"The visitor"}, Kind: "character"}}
	input := StoryMemory{Kind: "knowledge", Entity: "The visitor", Title: "Letter", Content: "The visitor may know the sender", State: "active", Quote: "Lin Wu found a letter.", KnownBy: []string{"Lin Wu"}}
	memories := writingMemoryCandidates(&project, chapter, chapter.Body, []StoryMemory{input, input, {Kind: "rule", Content: "Invented world rule", Quote: input.Quote}, {Kind: "fact", Content: "Missing evidence", Quote: "Not in this source"}, {Kind: "invalid", Title: "Bad kind", Content: "Rejected", Quote: input.Quote}, {Kind: "fact", Content: "Missing title", Quote: input.Quote}})
	assert(t, len(memories) == 1, "Repeated or ungrounded candidates were accepted")
	memory := memories[0]
	assert(t, memory.Status == "proposed" && memory.Epistemic == "inferred" && memory.Entity == "Lin Wu", "Source location was confused with semantic confirmation")
	assert(t, memory.SourceID == chapter.ID && memory.SourceHash == hashText(chapter.Body) && memory.Chapter == 1, "A new memory lost its exact source revision")
	project.Studio.Memories = memories
	assert(t, len(writingMemoryCandidates(&project, chapter, chapter.Body, []StoryMemory{input})) == 0, "Saving the same chapter created another copy of an unchanged memory")
}

// TestPlanSceneCardsCloneIndependently preserves nested scene and participant data in asynchronous project copies.
func TestPlanSceneCardsCloneIndependently(t *testing.T) {
	project := testProject()
	project.Studio.Plans = []StoryPlan{{ID: "plan", Level: "chapter", Title: "Scene", StartChapter: 1, EndChapter: 1, Status: "confirmed", SceneCards: []WritingScene{{POV: "Lin Wu", Goal: "Read letter", Characters: []WritingSceneCharacter{{Name: "Lin Wu", Want: "Find sender"}}}}}}
	copy := cloneProject(project)
	copy.Studio.Plans[0].SceneCards[0].Characters[0].Want = "Changed"
	assert(t, project.Studio.Plans[0].SceneCards[0].Characters[0].Want == "Find sender", "A scene-card participant shared mutable state across project snapshots")
	assert(t, validateStudio(&project.Studio) == nil, "An existing chapter outline could not retain optional scene cards")
}

// TestProposalAcceptanceIsExplicitAndAtomic proves generated plans stay pending and cannot be applied twice.
func TestProposalAcceptanceIsExplicitAndAtomic(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	response := jsonText(map[string]any{"text": "A proposed volume.", "proposals": []StudioProposal{{Kind: "plan", Title: "First volume", Content: "Explore the city", Plan: &StoryPlan{Level: "volume", Title: "First volume", StartChapter: 1, EndChapter: 10, Goal: "Find the secret"}}}})
	server := staticModel(t, streamPayload(response), 0)
	config := defaultProvider()
	config.Model = "fixture"
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "action": "outline", "instruction": "Plan the first volume"}))
	assert(t, job.Status == "done" && len(job.Proposals) == 1, "Model proposal was not staged")
	untouched := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, len(untouched.Studio.Plans) == 0, "Generated text modified author plans without acceptance")
	result := acceptProposalAfterPreview(t, engine, map[string]any{"project_id": p.ID, "revision": p.Revision, "job_id": job.ID, "proposal_id": job.Proposals[0].ID, "decision": "accepted"})
	assert(t, len(result.Project.Studio.Plans) == 1 && result.Job.Proposals[0].Status == "accepted", "Acceptance did not update work and proposal together")
	reject(t, engine, "review_proposal", map[string]any{"project_id": p.ID, "revision": result.Project.Revision, "job_id": job.ID, "proposal_id": job.Proposals[0].ID, "decision": "accepted"})
}

// TestConfirmedChapterPlansHaveOneEffectiveOutline rejects ambiguous chapter plans without deleting alternatives.
func TestConfirmedChapterPlansHaveOneEffectiveOutline(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	first := StoryPlan{ID: "first-outline", Level: "chapter", Title: "First outline", StartChapter: 2, EndChapter: 2, Status: "confirmed"}
	saved := invoke[Project](t, engine, "save_plan", map[string]any{"project_id": p.ID, "revision": p.Revision, "plan": first})
	second := first
	second.ID, second.Title = "second-outline", "Alternative outline"
	reject(t, engine, "save_plan", map[string]any{"project_id": p.ID, "revision": saved.Revision, "plan": second})
	second.Status = "proposed"
	saved = invoke[Project](t, engine, "save_plan", map[string]any{"project_id": p.ID, "revision": saved.Revision, "plan": second})
	assert(t, len(saved.Studio.Plans) == 2 && saved.Studio.Plans[1].Status == "proposed", "An alternative was deleted or silently confirmed")
	legacy := saved
	legacy.Studio.Plans[1].Status = "confirmed"
	_, err := resolveWritingPlan(&legacy, Job{}, agentStepEnvelope{PlanID: first.ID})
	assert(t, err != nil && strings.Contains(err.Error(), "multiple confirmed"), "A legacy duplicate silently selected one outline")
}

// TestCreatePlannedChaptersUsesSelectedScope preserves existing chapters and leaves other volumes unmaterialized.
func TestCreatePlannedChaptersUsesSelectedScope(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Studio.Plans = []StoryPlan{
		{ID: "volume-a", Level: "volume", Title: "Selected volume", StartChapter: 1, EndChapter: 2, Status: "confirmed"},
		{ID: "volume-b", Level: "volume", Title: "Later volume", StartChapter: 3, EndChapter: 3, Status: "confirmed"},
		{ID: "outline-2", ParentID: "volume-a", Level: "chapter", Title: "New chapter", StartChapter: 2, EndChapter: 2, Status: "confirmed"},
		{ID: "outline-3", ParentID: "volume-b", Level: "chapter", Title: "Later chapter", StartChapter: 3, EndChapter: 3, Status: "confirmed"},
	}
	insertProject(t, engine, p)
	created := invoke[Project](t, engine, "create_planned_chapters", map[string]any{"project_id": p.ID, "revision": p.Revision, "plan_id": "volume-a"})
	assert(t, len(created.Chapters) == 2 && created.Chapters[0].Body == p.Chapters[0].Body && created.Chapters[1].Title == "New chapter", "Materialization ignored the selected planning scope")
}

// TestLegacyPlanConflictsCanBeResolvedIncrementally keeps every alternative while the author chooses effective outlines.
func TestLegacyPlanConflictsCanBeResolvedIncrementally(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	for _, number := range []uint32{2, 3} {
		for index := 0; index < 3; index++ {
			p.Studio.Plans = append(p.Studio.Plans, StoryPlan{ID: fmt.Sprintf("outline-%d-%d", number, index), Level: "chapter", Title: fmt.Sprintf("Chapter %d alternative %d", number, index), StartChapter: number, EndChapter: number, Status: "confirmed"})
		}
	}
	// Store.Insert represents an older database that admitted conflicting confirmed outlines.
	insertProject(t, engine, p)
	chosen := p.Studio.Plans[1]
	saved := invoke[Project](t, engine, "save_plan", map[string]any{"project_id": p.ID, "revision": p.Revision, "plan": chosen, "resolve_conflicts": true})
	assert(t, len(saved.Studio.Plans) == 6 && confirmedChapterPlanConflicts(saved.Studio.Plans) == 2, "Resolving one chapter deleted alternatives or required all conflicts at once")
	for _, plan := range saved.Studio.Plans {
		if plan.StartChapter == 2 {
			assert(t, (plan.Status == "confirmed") == (plan.ID == chosen.ID), "The wrong alternative remained effective")
		}
	}
	chosen = p.Studio.Plans[4]
	saved = invoke[Project](t, engine, "save_plan", map[string]any{"project_id": p.ID, "revision": saved.Revision, "plan": chosen, "resolve_conflicts": true})
	assert(t, validateStudio(&saved.Studio) == nil && len(saved.Studio.Plans) == 6, "The final choice failed to restore a valid plan tree")
}

// TestKnowledgeMaintenanceDispatchRoundTrip verifies read-only previews, revision checks and durable reversible cleanup through public commands.
func TestKnowledgeMaintenanceDispatchRoundTrip(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	ingestCandidate(t, &project)
	project.Facts[0].SourceHash = hashText("Earlier chapter body")
	chapter := project.Chapters[0]
	project.Studio.Memories = []StoryMemory{
		{ID: "stale-memory", Kind: "knowledge", Entity: "Lin Wu", Title: "Letter", Content: "Earlier interpretation", Status: "proposed", Epistemic: "inferred", Chapter: 1, SourceID: chapter.ID, SourceHash: hashText("Earlier chapter body"), Quote: "Lin Wu found a letter."},
		{ID: "current-memory", Kind: "knowledge", Entity: "Lin Wu", Title: "Letter", Content: "Uncertain interpretation", Status: "proposed", Epistemic: "inferred", Chapter: 1, SourceID: chapter.ID, SourceHash: hashText(chapter.Body), Quote: "Lin Wu found a letter."},
	}
	insertProject(t, engine, project)
	project = invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	before := project.Revision
	view := invoke[struct {
		Items       []ContextItem               `json:"items"`
		Maintenance KnowledgeMaintenancePreview `json:"maintenance"`
	}](t, engine, "story_knowledge", map[string]any{"project_id": project.ID, "through_chapter": 1, "query": "Lin Wu", "include_candidates": true})
	preview := invoke[KnowledgeMaintenancePreview](t, engine, "preview_memory_maintenance", map[string]any{"project_id": project.ID})
	assert(t, preview.Revision == before && preview.Digest == view.Maintenance.Digest && preview.ArchiveCount == 2, "Public knowledge views disagreed on the current maintenance preview")
	assert(t, strings.Contains(jsonText(view.Items), "Lin Wu found a letter.") && !strings.Contains(jsonText(view.Items), "Uncertain interpretation"), "Current candidates lost source navigation or leaked an inferred claim as a fact")
	stored := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, stored.Revision == before && stored.Facts[0].Status == "pending" && len(stored.Studio.KnowledgeMaintenance) == 0, "Reading a knowledge view mutated the project")
	reject(t, engine, "apply_memory_maintenance", map[string]any{"project_id": project.ID, "revision": before, "digest": "obsolete-preview"})
	reject(t, engine, "apply_memory_maintenance", map[string]any{"project_id": project.ID, "revision": before + 1, "digest": preview.Digest})
	project = invoke[Project](t, engine, "apply_memory_maintenance", map[string]any{"project_id": project.ID, "revision": before, "digest": preview.Digest})
	assert(t, project.Revision == before+1 && project.Facts[0].Status == "rejected" && project.Studio.Memories[0].Status == "rejected" && project.Studio.Memories[1].Status == "proposed", "Maintenance did not archive precisely the obsolete candidates")
	assert(t, len(project.Studio.KnowledgeMaintenance) == 1 && len(project.Studio.KnowledgeMaintenance[0].Entries) == 2, "Maintenance did not persist its affected-record receipt")
	receiptID := project.Studio.KnowledgeMaintenance[0].ID
	state := project.Studio
	state.KnowledgeMaintenance = nil
	project = invoke[Project](t, engine, "save_studio", map[string]any{"project_id": project.ID, "revision": project.Revision, "studio": state})
	assert(t, len(project.Studio.KnowledgeMaintenance) == 1 && project.Studio.KnowledgeMaintenance[0].ID == receiptID, "Saving older studio settings erased the server-owned maintenance receipt")
	reject(t, engine, "undo_memory_maintenance", map[string]any{"project_id": project.ID, "revision": before + 1, "receipt_id": receiptID})
	undoRevision := project.Revision
	project = invoke[Project](t, engine, "undo_memory_maintenance", map[string]any{"project_id": project.ID, "revision": undoRevision, "receipt_id": receiptID})
	assert(t, project.Revision == undoRevision+1 && project.Facts[0].Status == "pending" && project.Studio.Memories[0].Status == "proposed" && project.Studio.Memories[1].Status == "proposed", "Public undo did not restore the exact candidate review states")
	stored = invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, stored.Revision == project.Revision && stored.Studio.KnowledgeMaintenance[0].UndoneAt != "" && stored.Facts[0].Status == "pending", "Undo results did not survive the database boundary")
	reject(t, engine, "undo_memory_maintenance", map[string]any{"project_id": project.ID, "revision": stored.Revision, "receipt_id": receiptID})
}

// TestAddSourceReplacesByID covers in-place source replacement that keeps the id stable and rejects unknown ids.
func TestAddSourceReplacesByID(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	saved := invoke[Project](t, engine, "add_source", map[string]any{"project_id": project.ID, "revision": project.Revision, "title": "Craft notes", "category": "craft", "url": "", "content": "old content"})
	assert(t, len(saved.Sources) == 1, "Adding a source without source_id did not append it")
	id := saved.Sources[0].ID
	replaced := invoke[Project](t, engine, "add_source", map[string]any{"project_id": project.ID, "revision": saved.Revision, "source_id": id, "title": "Craft notes v2", "category": "craft", "url": "", "content": "new content"})
	assert(t, len(replaced.Sources) == 1 && replaced.Sources[0].ID == id && replaced.Sources[0].Title == "Craft notes v2" && replaced.Sources[0].Content == "new content", "A source with source_id was not replaced in place")
	reject(t, engine, "add_source", map[string]any{"project_id": project.ID, "revision": replaced.Revision, "source_id": "missing", "title": "Craft notes", "category": "craft", "url": "", "content": "ignored"})
}
