//go:build quillweave_whitebox

package core

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEntityRenameUndoRestoresFactLinks restores both fact roles and aliases while preserving later-edit protection.
func TestEntityRenameUndoRestoresFactLinks(t *testing.T) {
	for _, laterEdit := range []bool{false, true} {
		t.Run(fmt.Sprint(laterEdit), func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			ingestCandidate(t, &project)
			original := project.Facts[0].Entity
			project.Facts[0].Status = "confirmed"
			project.Facts[0].RelatedEntity = original
			entity := project.ResolveEntity(original)
			assert(t, entity != nil, "The source entity is missing")
			entity.Aliases = []string{"Known alias"}
			renamed := *entity
			renamed.Name = "Renamed character"
			insertProject(t, engine, project)
			cs := invoke[ChangeSet](t, engine, "stage_changeset", map[string]any{
				"project_id": project.ID,
				"changeset":  ChangeSet{Operations: []ChangeOperation{{TargetKind: TargetEntity, TargetID: entity.ID, OpType: OpUpsert, AfterValue: rawMessage(renamed)}}},
			})
			preview := invoke[struct {
				ChangeSet ChangeSet `json:"changeset"`
			}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": cs.ID})
			applied := invoke[struct {
				Project Project `json:"project"`
			}](t, engine, "apply_changeset", map[string]any{"project_id": project.ID, "changeset_id": cs.ID, "revision": preview.ChangeSet.PreviewRevision, "review_token": preview.ChangeSet.ReviewToken})
			assert(t, applied.Project.Facts[0].Entity == renamed.Name && applied.Project.Facts[0].RelatedEntity == renamed.Name, "The rename did not update both fact roles")
			if laterEdit {
				chapter := applied.Project.Chapters[0]
				updated := invoke[Project](t, engine, "save_chapter", map[string]any{"project_id": project.ID, "revision": applied.Project.Revision, "chapter_id": chapter.ID, "title": chapter.Title, "body": chapter.Body + " Later edit.", "synopsis": chapter.Synopsis, "status": chapter.Status, "target_words": chapter.TargetWords})
				reject(t, engine, "undo_changeset", map[string]any{"project_id": project.ID, "changeset_id": cs.ID, "revision": updated.Revision})
				current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
				assert(t, current.Chapters[0].Body == updated.Chapters[0].Body && current.Entities[0].Name == renamed.Name, "A rejected undo changed newer content")
				return
			}
			undone := invoke[struct {
				Project Project `json:"project"`
			}](t, engine, "undo_changeset", map[string]any{"project_id": project.ID, "changeset_id": cs.ID, "revision": applied.Project.Revision})
			assert(t, undone.Project.Facts[0].Entity == original && undone.Project.Facts[0].RelatedEntity == original, "Undo left renamed fact references")
			assert(t, undone.Project.ResolveEntity("Known alias").Name == original, "Undo lost the entity alias")
			persisted := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
			assert(t, persisted.Facts[0].Entity == original && persisted.Facts[0].RelatedEntity == original, "Restored references were not persisted")
		})
	}
}

// TestCompoundEntityUndoKeepsUntouchedReferences separates renamed facts from pre-existing references to the destination name.
func TestCompoundEntityUndoKeepsUntouchedReferences(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	project.Chapters[0].Body += " Renamed character entered the library."
	chapter := project.Chapters[0]
	_, err := ingest(&project, chapter.ID, hashText(chapter.Body), []Candidate{
		testCandidate(),
		{Entity: "Renamed character", Kind: "event", Claim: "Enters the library", Quote: "Renamed character entered the library.", Epistemic: "explicit"},
	}, "fixture")
	assert(t, err == nil && len(project.Facts) == 2, "Could not prepare distinct, source-backed references")
	for i := range project.Facts {
		project.Facts[i].Status = "confirmed"
	}
	first := *project.ResolveEntity("Lin Wu")
	second := *project.ResolveEntity("Renamed character")
	first.Name = second.Name
	insertProject(t, engine, project)
	cs := invoke[ChangeSet](t, engine, "stage_changeset", map[string]any{"project_id": project.ID, "changeset": ChangeSet{Operations: []ChangeOperation{
		{TargetKind: TargetEntity, TargetID: second.ID, OpType: OpDelete},
		{TargetKind: TargetEntity, TargetID: first.ID, OpType: OpUpsert, AfterValue: rawMessage(first)},
	}}})
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": cs.ID}).ChangeSet
	applied := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "apply_changeset", map[string]any{"project_id": project.ID, "changeset_id": cs.ID, "revision": preview.PreviewRevision, "review_token": preview.ReviewToken})
	undone := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "undo_changeset", map[string]any{"project_id": project.ID, "changeset_id": cs.ID, "revision": applied.Project.Revision})
	for _, original := range project.Facts {
		for _, actual := range undone.Project.Facts {
			if original.ID == actual.ID && original.Entity != actual.Entity {
				t.Fatalf("Undo changed an untouched reference: fact=%s expected=%q actual=%q", original.ID, original.Entity, actual.Entity)
			}
		}
	}
}

// TestEntityUndoSnapshotCompatibility verifies legacy safety and exact restoration after repeated renames.
func TestEntityUndoSnapshotCompatibility(t *testing.T) {
	for _, scenario := range []string{"legacy-referenced", "legacy-unreferenced", "repeated-renames"} {
		t.Run(scenario, func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			ingestCandidate(t, &project)
			original := project.Facts[0].Entity
			entity := *project.ResolveEntity(original)
			if scenario == "legacy-unreferenced" {
				project.Facts = []Fact{}
			}
			entity.Name = "Intermediate name"
			operations := []ChangeOperation{{TargetKind: TargetEntity, TargetID: entity.ID, OpType: OpUpsert, AfterValue: rawMessage(entity)}}
			if scenario == "repeated-renames" {
				entity.Name = "Final name"
				operations = append(operations, ChangeOperation{TargetKind: TargetEntity, TargetID: entity.ID, OpType: OpUpsert, AfterValue: rawMessage(entity)})
			}
			insertProject(t, engine, project)
			cs := invoke[ChangeSet](t, engine, "stage_changeset", map[string]any{"project_id": project.ID, "changeset": ChangeSet{Operations: operations}})
			preview := invoke[struct {
				ChangeSet ChangeSet `json:"changeset"`
			}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": cs.ID}).ChangeSet
			applied := invoke[struct {
				Project Project `json:"project"`
			}](t, engine, "apply_changeset", map[string]any{"project_id": project.ID, "changeset_id": cs.ID, "revision": preview.PreviewRevision, "review_token": preview.ReviewToken}).Project
			stored := *findChangeSet(&applied, cs.ID)
			assert(t, stored.BeforeFactReferences != nil, "A current change omitted its fact snapshot marker")
			if strings.HasPrefix(scenario, "legacy-") {
				stored.BeforeFactReferences = nil
				err := engine.store.withWriteTransaction(func(conn *sql.Conn) error {
					return writeChangeSetRecord(conn, project.ID, stored)
				})
				assert(t, err == nil, fmt.Sprint(err))
			}
			peer, err := NewEngine(engine.store.path)
			assert(t, err == nil, fmt.Sprint(err))
			t.Cleanup(func() { assert(t, peer.Close() == nil, "Could not close the fresh reader") })
			args := map[string]any{"project_id": project.ID, "changeset_id": cs.ID, "revision": applied.Revision}
			if scenario == "legacy-referenced" {
				_, err := peer.Dispatch("undo_changeset", rawMessage(args))
				assert(t, err != nil && strings.Contains(err.Error(), "fact-reference snapshot"), "An ambiguous old rename was silently reversed")
				current := invoke[Project](t, peer, "get_project", map[string]any{"project_id": project.ID})
				assert(t, current.Revision == applied.Revision && current.Entities[0].Name == entity.Name && current.Facts[0].Entity == entity.Name, "Rejected legacy undo changed persisted data")
				return
			}
			undone := invoke[struct {
				Project Project `json:"project"`
			}](t, peer, "undo_changeset", args).Project
			assert(t, undone.ResolveEntity(original) != nil, "Undo did not restore the original entity")
			if scenario == "repeated-renames" {
				assert(t, len(stored.BeforeFactReferences) == 1 && undone.Facts[0].Entity == original, "Repeated renames lost the earliest reference snapshot")
			}
		})
	}
}

// TestFlowReadinessDeterministicProgression verifies the four primary creation areas from SQLite state.
func TestFlowReadinessDeterministicProgression(t *testing.T) {
	engine := testEngine(t)
	p := NewProject("", "", "")
	insertProject(t, engine, p)

	r0 := invoke[FlowReadiness](t, engine, "get_flow_readiness", map[string]any{"project_id": p.ID})
	assert(t, len(r0.Steps) == 4 && r0.RecommendedStep == 1, fmt.Sprintf("expected step 1 recommended, got %+v", r0))
	assert(t, r0.Steps[0].Status == StepNotStarted, "empty untitled project should be not_started")

	// Complete Step 1 (setup)
	p.Title = "让你苟命养生，你把旧仙熬死了？"
	p.Premise = "长生苟道流，以凡人武夫养气熬死旧仙。"
	p.Studio.Mode = "serial"
	p.Studio.TargetCharacters = 2_000_000
	assert(t, engine.store.Save(&p, p.Revision) == nil, "save step 1 failed")

	r1 := invoke[FlowReadiness](t, engine, "get_flow_readiness", map[string]any{"project_id": p.ID})
	assert(t, r1.Steps[0].Status == StepReady && r1.RecommendedStep == 2, fmt.Sprintf("expected step 1 ready and step 2 recommended, got %+v", r1))

	// Complete Step 2 (world)
	p.Entities = []Entity{
		{ID: "e1", Name: "陈长生", Kind: "character", Aliases: []string{}, Description: "主角"},
		{ID: "e2", Name: "大周神朝", Kind: "faction", Aliases: []string{}, Description: "势力"},
	}
	p.Studio.Memories = []StoryMemory{
		{ID: "m1", Kind: "rule", Title: "寿元代价", Content: "动用仙术必折阳寿", Status: "confirmed", KnownBy: []string{}},
	}
	assert(t, engine.store.Save(&p, p.Revision) == nil, "save step 2 failed")

	r2 := invoke[FlowReadiness](t, engine, "get_flow_readiness", map[string]any{"project_id": p.ID})
	assert(t, r2.Steps[1].Status == StepReady && r2.RecommendedStep == 3, fmt.Sprintf("expected step 2 ready and step 3 recommended, got %+v", r2))

	// Complete Step 3 (plan: confirmed chapter plan)
	p.Studio.Plans = []StoryPlan{
		{ID: "pl1", Level: "chapter", Title: "第一章 药铺学徒", StartChapter: 1, EndChapter: 1, Status: "confirmed", Characters: []string{}, Clues: []string{}},
	}
	assert(t, engine.store.Save(&p, p.Revision) == nil, "save step 3 failed")

	r3 := invoke[FlowReadiness](t, engine, "get_flow_readiness", map[string]any{"project_id": p.ID})
	assert(t, r3.Steps[2].Status == StepReady && r3.RecommendedStep == 4, fmt.Sprintf("expected step 3 ready and step 4 recommended, got %+v", r3))
}

// TestCLIProcessHelper executes the production CLI in an isolated child process for integration tests.
func TestCLIProcessHelper(t *testing.T) {
	arguments := os.Getenv("QUILLWEAVE_TEST_CLI_ARGS")
	if arguments == "" {
		t.Skip("CLI subprocess helper")
	}
	var args []string
	if err := decodeJSON([]byte(arguments), &args); err != nil {
		t.Fatal(err)
	}
	os.Exit(RunCLI(args, os.Stdout, os.Stderr))
}

// TestCLIWorkspaceAcrossProcesses checks external staging, desktop confirmation, continuous sessions and recovery.
func TestCLIWorkspaceAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.sqlite3")
	engine, err := NewEngine(path)
	assert(t, err == nil, fmt.Sprint(err))
	t.Cleanup(func() { engine.Close() })
	p := testProject()
	insertProject(t, engine, p)
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, streamPayload(`{"tool":"finish","text":"The current discussion can continue."}`))
	})
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	// run starts a true peer process and kills it on timeout while the desktop engine remains open.
	run := func(success bool, arguments ...string) []byte {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		arguments = append(arguments, "--db", path, "--project", p.ID, "--json")
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLIProcessHelper$")
		command.Env = append(os.Environ(), "QUILLWEAVE_TEST_CLI_ARGS="+jsonText(arguments))
		output, err := command.CombinedOutput()
		assert(t, (err == nil) == success, fmt.Sprintf("CLI %v: %v %s", arguments[0], err, output))
		return output
	}
	var session Conversation
	assert(t, decodeJSON(run(true, "session", "create", "--title", "Shared discussion"), &session) == nil, "CLI session response was invalid")
	assert(t, len(invoke[[]Conversation](t, engine, "list_conversations", map[string]any{"project_id": p.ID, "chapter_id": ""})) == 1, "Desktop could not see the CLI conversation")
	var answer struct {
		Job Job `json:"job"`
	}
	assert(t, decodeJSON(run(true, "agent", "First idea", "--session", session.ID, "--request-id", "cli-one"), &answer) == nil, "CLI generation response was invalid")
	firstID := answer.Job.ID
	assert(t, decodeJSON(run(true, "agent", "First idea", "--session", session.ID, "--request-id", "cli-one"), &answer) == nil && answer.Job.ID == firstID, "CLI retry created a duplicate task")
	run(true, "agent", "Refine the idea", "--session", session.ID)
	saved := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	assert(t, len(saved.Turns) == 4, "CLI history did not persist as two complete exchanges")
	file := filepath.Join(t.TempDir(), "candidate.json")
	candidate := ChangeSet{Summary: "Character from an external agent", Operations: []ChangeOperation{{ID: "character", TargetKind: TargetEntity, OpType: OpUpsert, Title: "Lin Wu", AfterValue: rawMessage(map[string]any{"id": "local-character", "name": "Lin Wu", "kind": "character", "aliases": []string{}, "description": "Investigates the letter"})}}}
	assert(t, os.WriteFile(file, rawMessage(candidate), 0600) == nil, "Could not prepare the external candidate")
	var preview struct {
		ChangeSet ChangeSet `json:"changeset"`
	}
	assert(t, decodeJSON(run(true, "change", "stage", "--input", file, "--session", session.ID), &preview) == nil && preview.ChangeSet.ReviewToken != "", "CLI staging did not return an exact preview")
	assert(t, strings.Contains(jsonText(preview.ChangeSet), `"preview_revision":0`), "Initial revision zero was omitted from the browser confirmation contract")
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, len(current.Entities) == 0 && len(current.Studio.ChangeSets) == 1, "Staging wrote formal content or hid its candidate from the desktop")
	run(false, "change", "apply", preview.ChangeSet.ID)
	applied := invoke[struct {
		Project   Project   `json:"project"`
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "apply_changeset", map[string]any{"project_id": p.ID, "changeset_id": preview.ChangeSet.ID, "revision": preview.ChangeSet.PreviewRevision, "review_token": preview.ChangeSet.ReviewToken})
	assert(t, len(applied.Project.Entities) == 1, "Desktop confirmation failed")
	var reread Project
	assert(t, decodeJSON(run(true, "read"), &reread) == nil && reread.Entities[0].Name == "Lin Wu", "CLI could not read the desktop's committed content")
	run(true, "change", "apply", preview.ChangeSet.ID, "--confirm", preview.ChangeSet.ReviewToken)
	interrupted := Job{ID: newID(), ProjectID: p.ID, SessionID: session.ID, Action: "agent", Status: "interrupted", Instruction: "Investigate the letter", BaseRevision: reread.Revision, Steps: []AgentStep{{ID: newID(), Tool: "plan", Status: "done", Detail: "Read the letter"}}}
	assert(t, engine.store.SaveRun(interrupted.ID, p.ID, storedJob(&interrupted)) == nil, "Could not prepare an interrupted task")
	assert(t, decodeJSON(run(true, "agent", "Continue from the saved steps", "--resume", interrupted.ID), &answer) == nil && answer.Job.SessionID == session.ID, "Agent recovery lost its shared conversation")
	assert(t, strings.Contains(answer.Job.Prompt.Messages[len(answer.Job.Prompt.Messages)-1].Content, "Resume task "+interrupted.ID), "Recovery omitted the saved task and source pointers")
	run(true, "session", "show", session.ID)
	run(true, "session", "list", "--offset", "100")
	run(false, "session", "list", "--offset", "-1")
	run(true, "job", "show", answer.Job.ID)
}

// TestChangeSetLifecycleAndAtomicUndo verifies staging, applying, revision conflict checking, and atomic rollback across target kinds.
func TestChangeSetLifecycleAndAtomicUndo(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	origTitle := p.Title
	origRevision := p.Revision

	staged := invoke[ChangeSet](t, engine, "stage_changeset", map[string]any{
		"project_id": p.ID,
		"changeset": map[string]any{
			"summary": "立项设定与主角入库",
			"operations": []map[string]any{
				{
					"id":          "op-meta",
					"target_kind": "project_meta",
					"op_type":     "upsert",
					"title":       "更新书名与核心设定",
					"after_value": map[string]any{
						"title":   "苟道长生录",
						"genre":   "古典仙侠",
						"premise": "凡人养生熬死诸仙",
					},
				},
				{
					"id":          "op-goal",
					"target_kind": "studio_goal",
					"op_type":     "upsert",
					"title":       "设置连载目标",
					"after_value": map[string]any{
						"mode":              "serial",
						"stage":             "planning",
						"target_characters": 2000000,
						"target_chapters":   650,
					},
				},
				{
					"id":          "op-dec",
					"target_kind": "decision",
					"op_type":     "upsert",
					"title":       "核心立项决定",
					"after_value": map[string]any{
						"id":      "dec-1",
						"title":   "核心立项决定",
						"content": "采用三卷本长篇结构",
						"status":  "accepted",
					},
				},
				{
					"id":          "op-ent",
					"target_kind": "entity",
					"op_type":     "upsert",
					"title":       "陈长生",
					"after_value": map[string]any{
						"id":          "ent-chen",
						"name":        "陈长生",
						"kind":        "character",
						"description": "百草堂养生学徒",
					},
				},
				{
					"id":          "op-mem",
					"target_kind": "memory",
					"op_type":     "upsert",
					"title":       "养生功法铁律",
					"after_value": map[string]any{
						"id":      "mem-rule-1",
						"kind":    "rule",
						"title":   "养生功法铁律",
						"content": "绝不越级强行动武",
						"status":  "confirmed",
					},
				},
				{
					"id":          "op-plan",
					"target_kind": "plan",
					"op_type":     "upsert",
					"title":       "第一卷：潜龙在渊",
					"after_value": map[string]any{
						"id":            "plan-vol-1",
						"level":         "volume",
						"title":         "第一卷：潜龙在渊",
						"start_chapter": 1,
						"end_chapter":   60,
						"status":        "confirmed",
					},
				},
			},
		},
	})
	assert(t, staged.Status == ChangeSetStaged && len(staged.Operations) == 6, "ChangeSet was not staged properly")
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": p.ID, "changeset_id": staged.ID})
	assert(t, preview.ChangeSet.ReviewToken != "" && len(preview.ChangeSet.Operations[0].PreviewValue) > 0, "preview must bind displayed values")

	// Stale revision must be rejected on apply_changeset
	reject(t, engine, "apply_changeset", map[string]any{
		"project_id":   p.ID,
		"revision":     origRevision + 99,
		"changeset_id": staged.ID,
	})

	// Apply changeset with matching revision
	applyRes := invoke[struct {
		Project   Project   `json:"project"`
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "apply_changeset", map[string]any{
		"project_id":   p.ID,
		"revision":     origRevision,
		"changeset_id": staged.ID,
		"review_token": preview.ChangeSet.ReviewToken,
	})
	updated := applyRes.Project
	assert(t, updated.Revision == origRevision+1 && applyRes.ChangeSet.Status == ChangeSetApplied, "apply_changeset did not increment project revision")
	assert(t, updated.Title == "苟道长生录" && updated.Studio.TargetCharacters == 2000000, "project_meta or studio_goal not applied")
	assert(t, len(updated.Studio.Decisions) == 1 && updated.Studio.Decisions[0].Status == "accepted", "decision not committed")
	assert(t, len(updated.Studio.Plans) == 1 && updated.Studio.Plans[0].Status == "confirmed", "plan not committed")

	// Stale revision must be rejected on undo_changeset
	reject(t, engine, "undo_changeset", map[string]any{
		"project_id":   p.ID,
		"revision":     origRevision,
		"changeset_id": staged.ID,
	})

	// Undo changeset restores exact prior state
	undoRes := invoke[struct {
		Project   Project   `json:"project"`
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "undo_changeset", map[string]any{
		"project_id":   p.ID,
		"revision":     updated.Revision,
		"changeset_id": staged.ID,
	})
	reverted := undoRes.Project
	assert(t, reverted.Title == origTitle && reverted.Studio.TargetCharacters == 0 && undoRes.ChangeSet.Status == ChangeSetUndone, "undo_changeset did not restore project_meta and studio_goal")
	assert(t, len(reverted.Studio.Decisions) == 0 && len(reverted.Studio.Plans) == 0, "undo_changeset did not remove created decision and plan")
}

// TestSaveIntentRequiresReview reproduces the screenshot while requiring preview before every formal write.
func TestSaveIntentRequiresReview(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)

	screenshotPayload := `{"tool":"propose","proposal":{"kind":"decision","title":"作品立项大纲：长篇节奏、核心人设与黄金三章细纲","content":"### 一、作品基本信息与200万字长篇节奏\n1. 书名：《让你苟命养生，你把旧仙熬死了？》\n2. 目标篇幅：200万字长篇（建议全书约650章，单章3000字，第三人称有限视角）。"}}json`
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, nativeStreamFixture("gemini", screenshotPayload))
	})
	config := nativeConfig("gemini")
	config.BaseURL = server.URL
	configureModel(t, engine, config)

	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{
		"project_id":    p.ID,
		"chapter_id":    "",
		"session_id":    session.ID,
		"request_id":    "screenshot-repro-1",
		"action":        "agent",
		"instruction":   "把这些写进创作目标里面",
		"ui_language":   "zh-CN",
		"commit_policy": "auto",
	}))
	assert(t, job.Status == "done", job.Error)
	assert(t, !strings.Contains(job.Text, `"tool"`) && !strings.Contains(job.Text, `"}json`), fmt.Sprintf("raw JSON leaked into job.Text: %s", job.Text))
	assert(t, strings.Contains(job.Text, "作品立项大纲"), "formatted summary missing from job.Text")

	conv := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	lastTurn := conv.Turns[len(conv.Turns)-1]
	assert(t, !strings.Contains(lastTurn.Content, `"tool"`) && !strings.Contains(lastTurn.Content, `"}json`), "raw JSON leaked into turn.Content")

	after := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, len(after.Studio.Decisions) == 0 && after.Studio.TargetCharacters == 0, "save intent bypassed author review")
	assert(t, len(after.Studio.ChangeSets) == 1 && after.Studio.ChangeSets[0].Status == ChangeSetStaged, "expected staged ChangeSet on project")
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": p.ID, "changeset_id": after.Studio.ChangeSets[0].ID})
	committed := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "apply_changeset", map[string]any{"project_id": p.ID, "changeset_id": preview.ChangeSet.ID, "revision": after.Revision, "review_token": preview.ChangeSet.ReviewToken})
	assert(t, committed.Project.Studio.TargetCharacters == 2000000 && len(committed.Project.Studio.Decisions) == 1, "reviewed settings were not saved")
}

// TestPrefixCacheStabilityAndBlockCompaction verifies Layer 0/1 static prefix caching, block compaction, and scoped agent tools.
func TestPrefixCacheStabilityAndBlockCompaction(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Studio.Language = "zh-CN"
	p.Studio.Mode = "serial"
	p.Studio.Stage = "planning"
	p.Entities = append(p.Entities, Entity{
		ID: "ent-1", Name: "陈长生", Kind: "character", Aliases: []string{}, Description: "主角",
	})
	p.Studio.Memories = append(p.Studio.Memories, StoryMemory{
		ID: "rule-1", Kind: "rule", Title: "硬规则", Content: "不可违背的因果律", Status: "confirmed", KnownBy: []string{},
	})
	p.Studio.Plans = append(p.Studio.Plans, StoryPlan{
		ID: "plan-1", Level: "volume", Title: "第一卷", StartChapter: 1, EndChapter: 20, Goal: "潜伏百草堂", Status: "confirmed", Characters: []string{}, Clues: []string{},
	})
	insertProject(t, engine, p)

	layer1A := buildStoryBibleLayer1(&p, 24000)
	layer1B := buildStoryBibleLayer1(&p, 24000)
	assert(t, layer1A == layer1B && strings.Contains(layer1A, "<story_bible>"), "Layer 1 snapshot must be deterministic and byte-identical")

	prompt, err := prepareAI(&p, AIRequest{
		Action:      "agent",
		Language:    "zh-CN",
		Instruction: "梳理细纲",
		IntentHint:  "outline_breakdown",
		WorkspaceContext: &WorkspaceContext{
			ActiveStep:   "plan",
			ActivePlanID: "plan-1",
		},
	}, 24000)
	assert(t, err == nil, fmt.Sprint(err))
	assert(t, len(prompt.Messages) >= 2 && prompt.Messages[0].CacheControl != nil && prompt.Messages[0].CacheControl.Type == "ephemeral", "System prefix message missing ephemeral CacheControl")
	assert(t, strings.Contains(prompt.Messages[0].Content, "Continuity:") && strings.Contains(prompt.Messages[0].Content, "Serial rhythm:") && strings.Contains(prompt.Messages[0].Content, "<story_bible>"), "System prefix missing built-in writing craft skills or the Story Bible")
	lastMsg := prompt.Messages[len(prompt.Messages)-1].Content
	assert(t, strings.Contains(lastMsg, "细纲拆解") && strings.Contains(lastMsg, "<workspace_context>"), "Layer 3 missing intent_hint or workspace_context")

	// Verify 80% block compaction threshold
	history := []Message{
		{Role: "user", Content: strings.Repeat("早期讨论内容A ", 80)},
		{Role: "assistant", Content: strings.Repeat("早期回答内容B ", 80)},
		{Role: "user", Content: "近期问题C"},
		{Role: "assistant", Content: "近期回答D"},
	}
	noCompact, _, _ := compactHistoryBlockwise(history, 10000, "", 0)
	assert(t, len(noCompact) == 4 && noCompact[0].Content == history[0].Content, "history below 80% threshold should not compact")
	compacted, summary, folded := compactHistoryBlockwise(history, 900, "", 0)
	assert(t, folded > 0 && summary != "" && len(compacted) >= 2 && strings.Contains(compacted[0].Content, "Earlier discussion excerpts"), "history above 80% threshold should compact oldest block")

	// Verify scoped agent tools
	out, toolErr := engine.executeScopedAgentTool(&p, Job{ProjectID: p.ID}, agentStepEnvelope{Tool: "get_entity_detail", EntityName: p.Entities[0].Name})
	assert(t, toolErr == nil && strings.Contains(out, p.Entities[0].Name), fmt.Sprintf("get_entity_detail failed: %v", toolErr))
	out, toolErr = engine.executeScopedAgentTool(&p, Job{ProjectID: p.ID}, agentStepEnvelope{Tool: "get_chapter_window", Chapter: 1})
	assert(t, toolErr == nil && strings.Contains(out, p.Chapters[0].Title), fmt.Sprintf("get_chapter_window failed: %v", toolErr))
	out, toolErr = engine.executeScopedAgentTool(&p, Job{ProjectID: p.ID}, agentStepEnvelope{Tool: "search_story_facts", Query: "因果律"})
	assert(t, toolErr == nil && strings.Contains(out, "硬规则"), fmt.Sprintf("search_story_facts failed: %v", toolErr))
}

// TestCLISubcommandsEndToEnd verifies headless CLI subcommands sharing SQLite state.
func TestCLISubcommandsEndToEnd(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cli.sqlite3")
	engine, err := NewEngine(dbPath)
	assert(t, err == nil, fmt.Sprint(err))
	p := testProject()
	insertProject(t, engine, p)
	_ = engine.Close()

	runCLI := func(args ...string) string {
		var stdout, stderr bytes.Buffer
		full := append([]string{args[0], "--db", dbPath, "-p", p.ID}, args[1:]...)
		code := RunCLI(full, &stdout, &stderr)
		assert(t, code == 0, fmt.Sprintf("CLI %v failed (code %d): %s", args, code, stderr.String()))
		return stdout.String()
	}

	statusOut := runCLI("status", "--json")
	assert(t, strings.Contains(statusOut, `"recommended_step"`), "CLI status missing flow readiness")

	goalOut := runCLI("goal", "set", "--mode", "serial", "--chars", "2000000", "--chapters", "650", "--json")
	assert(t, strings.Contains(goalOut, `"target_characters":2000000`), "CLI goal set did not update target_characters")
	var preview struct {
		Project   Project   `json:"project"`
		ChangeSet ChangeSet `json:"changeset"`
	}
	assert(t, decodeJSON([]byte(goalOut), &preview) == nil, "invalid CLI preview")
	assert(t, preview.Project.Studio.TargetCharacters == 0 && preview.ChangeSet.ReviewToken != "", "CLI goal skipped confirmation")
	runCLI("change", "apply", preview.ChangeSet.ID, "--confirm", preview.ChangeSet.ReviewToken, "--json")

	changeListOut := runCLI("change", "list", "--json")
	assert(t, strings.Contains(changeListOut, "["), "CLI change list did not return JSON array")

	changeUndoOut := runCLI("change", "undo", preview.ChangeSet.ID, "--json")
	assert(t, strings.Contains(changeUndoOut, `"target_characters":0`), "CLI change undo did not revert goal set ChangeSet")
}

// TestMapChangesRoundTripAndDependencies covers forward references, persisted map records and complete backups.
func TestMapChangesRoundTripAndDependencies(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	operations := []ChangeOperation{
		{ID: "route-op", TargetKind: TargetRoute, OpType: OpUpsert, AfterValue: rawMessage(WorldRoute{ID: "route", FromID: "port", ToID: "forest", Transport: "walk", TravelHours: 3, Bidirectional: true})},
		{ID: "port-op", TargetKind: TargetLocation, OpType: OpUpsert, AfterValue: rawMessage(WorldLocation{ID: "port", Name: "Port", Kind: "place", RegionID: "realm", PlanIDs: []string{"pending-chapter"}, ChapterIDs: []string{p.Chapters[0].ID}, EntityIDs: []string{}})},
		{ID: "forest-op", TargetKind: TargetLocation, OpType: OpUpsert, AfterValue: rawMessage(WorldLocation{ID: "forest", Name: "Forest", Kind: "place", EntityIDs: []string{}, PlanIDs: []string{}, ChapterIDs: []string{}})},
		{ID: "realm-op", TargetKind: TargetLocation, OpType: OpUpsert, AfterValue: rawMessage(WorldLocation{ID: "realm", Name: "Realm", Kind: "region", EntityIDs: []string{}, PlanIDs: []string{}, ChapterIDs: []string{}})},
		{ID: "chapter-op", TargetKind: TargetPlan, OpType: OpUpsert, AfterValue: rawMessage(StoryPlan{ID: "pending-chapter", ParentID: "pending-volume", Level: "chapter", Title: "Arrival", StartChapter: 1, EndChapter: 1, Characters: []string{}, Clues: []string{}})},
		{ID: "volume-op", TargetKind: TargetPlan, OpType: OpUpsert, AfterValue: rawMessage(StoryPlan{ID: "pending-volume", Level: "volume", Title: "Journey", StartChapter: 1, EndChapter: 5, Characters: []string{}, Clues: []string{}})},
	}
	staged := invoke[ChangeSet](t, engine, "stage_changeset", map[string]any{"project_id": p.ID, "changeset": ChangeSet{Source: "author", Summary: "Map and chapter plan", Operations: operations}})
	preview := invoke[struct {
		Project   Project   `json:"project"`
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": p.ID, "changeset_id": staged.ID})
	assert(t, len(preview.Project.Studio.Locations) == 0, "preview changed the map")
	assert(t, len(preview.ChangeSet.Impacts) == 2, "Map preview omitted its explicitly linked plan and chapter")
	result := invoke[struct {
		Project   Project   `json:"project"`
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "apply_changeset", map[string]any{"project_id": p.ID, "changeset_id": staged.ID, "revision": preview.ChangeSet.PreviewRevision, "review_token": preview.ChangeSet.ReviewToken})
	assert(t, len(result.Project.Studio.Locations) == 3 && len(result.Project.Studio.Routes) == 1, "map records did not persist")
	assert(t, !strings.HasPrefix(result.Project.Studio.Locations[0].PlanIDs[0], "pending-"), "map references retained a temporary plan ID")
	invalid := invoke[ChangeSet](t, engine, "stage_changeset", map[string]any{"project_id": p.ID, "changeset": ChangeSet{Operations: []ChangeOperation{{TargetKind: TargetLocation, TargetID: "port", OpType: OpDelete}}}})
	reject(t, engine, "preview_changeset", map[string]any{"project_id": p.ID, "changeset_id": invalid.ID})
	archive := invoke[map[string]string](t, engine, "export", map[string]any{"project_id": p.ID, "format": "archive"})
	assert(t, strings.Contains(archive["content"], "quillweave-2"), "map backup lacks a compatible format guard")
	restored := invoke[Project](t, engine, "import_archive", map[string]any{"content": archive["content"]})
	assert(t, len(restored.Studio.Locations) == 3 && len(restored.Studio.Routes) == 1 && len(restored.Studio.ChangeSets) == 2, "backup lost map records or review history")
	assert(t, restored.Studio.ChangeSets[0].ProjectID == restored.ID && restored.Studio.ChangeSets[0].Archived, "imported receipt retained live authority")
	reject(t, engine, "undo_changeset", map[string]any{"project_id": restored.ID, "changeset_id": staged.ID, "revision": restored.Revision})
	undone := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "undo_changeset", map[string]any{"project_id": p.ID, "changeset_id": staged.ID, "revision": result.Project.Revision})
	assert(t, len(undone.Project.Studio.Locations) == 0 && len(undone.Project.Studio.Routes) == 0, "map undo left orphaned records")
}

// TestReviewedChangeSetConflictsAndMetadataIsolation verifies immutable review, retries and independent candidate storage.
func TestReviewedChangeSetConflictsAndMetadataIsolation(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	staged := invoke[ChangeSet](t, engine, "stage_changeset", map[string]any{"project_id": p.ID, "changeset": ChangeSet{Operations: []ChangeOperation{
		{ID: "title", TargetKind: TargetProjectMeta, OpType: OpUpsert, AfterValue: rawMessage(map[string]string{"title": "Candidate"})},
		{ID: "idea", TargetKind: TargetDecision, OpType: OpUpsert, AfterValue: rawMessage(StoryDecision{ID: "idea", Title: "Idea", Content: "A possibility"})},
	}}})
	reject(t, engine, "apply_changeset", map[string]any{"project_id": p.ID, "changeset_id": staged.ID, "revision": p.Revision})
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": p.ID, "changeset_id": staged.ID}).ChangeSet
	op := preview.Operations[0]
	op.AfterValue = rawMessage(map[string]string{"title": "Reviewed title"})
	edited := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": p.ID, "changeset_id": staged.ID, "operations": []ChangeOperation{op}}).ChangeSet
	assert(t, len(edited.Skipped) == 1 && edited.ReviewToken != preview.ReviewToken, "edited preview lost its selection or token binding")
	p.Genre = "Changed elsewhere"
	assert(t, engine.store.Save(&p, p.Revision) == nil, "save failed")
	loaded := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, len(loaded.Studio.ChangeSets) == 1 && loaded.Studio.ChangeSets[0].ReviewToken == edited.ReviewToken, "a stale project save erased the independently reviewed candidate")
	reject(t, engine, "apply_changeset", map[string]any{"project_id": p.ID, "changeset_id": staged.ID, "revision": p.Revision, "review_token": edited.ReviewToken})
	current := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": p.ID, "changeset_id": staged.ID}).ChangeSet
	args := map[string]any{"project_id": p.ID, "changeset_id": staged.ID, "revision": current.PreviewRevision, "review_token": current.ReviewToken}
	first := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "apply_changeset", args).Project
	second := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "apply_changeset", args).Project
	assert(t, first.Revision == second.Revision && second.Title == "Reviewed title" && len(second.Studio.Decisions) == 0, "confirmation retry duplicated or broadened the write")
	second.Genre = "Later edit"
	assert(t, engine.store.Save(&second, second.Revision) == nil, "later edit failed")
	reject(t, engine, "undo_changeset", map[string]any{"project_id": p.ID, "changeset_id": staged.ID, "revision": second.Revision})
}
