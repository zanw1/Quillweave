//go:build quillweave_whitebox

package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestAppendingChapterRecomposesRemainingScenes prevents confirmed whole-chapter cards from replaying saved action.
func TestAppendingChapterRecomposesRemainingScenes(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	project.Chapters[0].Body = "Lin Wu has wrapped the ledger. The ferry leaves without him."
	oldScene := writingSceneFixture()
	oldScene.Action = "REPLAY_COMPLETED_ACTION: Wrap the ledger in cloth"
	project.Studio.Plans = []StoryPlan{{ID: "append-plan", Level: "chapter", Title: "Keep the promise", Goal: "Bring the ledger home", StartChapter: 1, EndChapter: 1, Status: "confirmed", SceneCards: []WritingScene{oldScene}}}
	insertProject(t, engine, project)
	nextScene := writingSceneFixture()
	nextScene.Action, nextScene.Choice, nextScene.Outcome = "Ask Chen for a place to sleep", "Stay until morning", "Chen shares his spare room"
	composition := WritingComposition{Intent: "Continue after the missed ferry", Approach: "Let the missed departure change the relationship", SceneCards: []WritingScene{nextScene}}
	draft := "Chen opens the spare room. Lin Wu leaves his wet boots outside."
	server, calls := sequenceModel(t, []string{jsonText(composition), draft, `{"summary":"Ready.","issues":[],"memories":[]}`})
	config := defaultProvider()
	config.BaseURL, config.Model, config.ContextChars = server.URL, "append-existing-scenes", 64000
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "continue", "commit_policy": "auto", "instruction": "Continue after the ferry has left"}))
	assert(t, job.Status == "done" && calls.Load() == 3 && job.Writing.Stages[0].Name == "composing" && job.Writing.Composition.SceneCards[0].Action == nextScene.Action, "Append reused a completed scene: "+job.Error)
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, job.Writing.Saved && saved.Chapters[0].Body == project.Chapters[0].Body+"\n\n"+draft && saved.Studio.Plans[0].SceneCards[0].Action == nextScene.Action, "Append failed to save the new scene and retain the existing chapter boundary")
	invoke[any](t, engine, "undo_changeset", map[string]any{"project_id": project.ID, "changeset_id": job.ChangeSets[0].ID, "revision": saved.Revision})
	undone := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, undone.Chapters[0].Body == project.Chapters[0].Body && undone.Studio.Plans[0].SceneCards[0].Action == oldScene.Action, "Undo lost the prior prose or prior scene cards")
}

// writingSettingsStory copies the real goal form payload, including legacy stored writing values.
func writingSettingsStory(project Project) map[string]any {
	return map[string]any{
		"title": project.Title, "genre": project.Genre, "premise": project.Premise,
		"style": project.Style, "outline": project.Outline, "writing_style": project.WritingStyle,
		"default_chapter_words": project.DefaultChapterWords, "narrative_pov": project.NarrativePov,
	}
}

// TestGoalSaveRetainsLegacyWritingInstructions reproduces the screenshot through the actual manual save command.
func TestGoalSaveRetainsLegacyWritingInstructions(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	legacy := "去AI痕迹精修规范（Humanizer准则）\n1. 保留人物独立声音。\n2. 用具体行动承接情绪。"
	project.WritingStyle, project.NarrativePov = legacy, "third_limited"
	project.Style = "慢节奏日常与克制幽默。"
	insertProject(t, engine, project)
	project.Studio.NarrativeFocus = "守墓事务、同门相处和生存选择。"
	saved := invoke[Project](t, engine, "save_studio", map[string]any{"project_id": project.ID, "revision": project.Revision, "studio": project.Studio, "story": writingSettingsStory(project)})
	assert(t, saved.WritingStyle == "custom" && saved.NarrativePov == "Third-person limited", "Legacy writing values were not normalized")
	assert(t, strings.Contains(saved.Style, legacy) && strings.Contains(saved.Style, project.Style) && saved.Studio.NarrativeFocus == project.Studio.NarrativeFocus, "The manual save lost writing instructions or the edited reading experience")
	assert(t, saved.Chapters[0].Body == project.Chapters[0].Body && saved.Revision == 1, "Saving goals unexpectedly changed the chapter")
	again := invoke[Project](t, engine, "save_studio", map[string]any{"project_id": saved.ID, "revision": saved.Revision, "studio": saved.Studio, "story": writingSettingsStory(saved)})
	assert(t, strings.Count(again.Style, legacy) == 1, "Repeated saving duplicated migrated writing instructions")
}

// TestNarrativeViewpointAliases accepts stored enum values and keeps unknown viewpoints invalid.
func TestNarrativeViewpointAliases(t *testing.T) {
	for _, item := range []struct{ input, want string }{
		{"third_limited", "Third-person limited"},
		{"third_person_limited", "Third-person limited"},
		{" Third-person limited ", "Third-person limited"},
		{"第三人称有限视角", "Third-person limited"},
		{"first_person", "First person"},
		{"First person", "First person"},
		{"第一人称", "First person"},
		{"omniscient", "Omniscient"},
		{"third_omniscient", "Omniscient"},
		{"全知视角", "Omniscient"},
	} {
		t.Run(item.input, func(t *testing.T) {
			assert(t, narrativePovLabel(item.input) == item.want, "A stored viewpoint alias is not recognized")
		})
	}
	assert(t, narrativePovLabel("unknown-viewpoint") == "unknown-viewpoint", "An invalid viewpoint was silently assigned")
}

// TestWritingSettingsAreValidatedOnPreview prevents assistant-created invalid presets from breaking later manual saves.
func TestWritingSettingsAreValidatedOnPreview(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	invalid := "another-unknown-preset"
	cs := ChangeSet{Operations: []ChangeOperation{{TargetKind: TargetProjectMeta, OpType: OpUpsert, AfterValue: rawMessage(projectMetaPatch{WritingStyle: &invalid})}}}
	staged := invoke[ChangeSet](t, engine, "stage_changeset", map[string]any{"project_id": project.ID, "changeset": cs})
	reject(t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": staged.ID})
	pov := "third_limited"
	cs = ChangeSet{Operations: []ChangeOperation{{TargetKind: TargetProjectMeta, OpType: OpUpsert, AfterValue: rawMessage(projectMetaPatch{NarrativePov: &pov})}}}
	staged = invoke[ChangeSet](t, engine, "stage_changeset", map[string]any{"project_id": project.ID, "changeset": cs})
	preview := invoke[struct {
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": staged.ID})
	applied := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "apply_changeset", map[string]any{"project_id": project.ID, "changeset_id": staged.ID, "revision": preview.ChangeSet.PreviewRevision, "review_token": preview.ChangeSet.ReviewToken})
	assert(t, applied.Project.NarrativePov == "Third-person limited", "The assistant preview did not use canonical writing settings")
}

// TestWritingSettingsPreserveLegacyWhenChangingPreset keeps old author instructions during an explicit preset change.
func TestWritingSettingsPreserveLegacyWhenChangingPreset(t *testing.T) {
	project := testProject()
	project.WritingStyle = "Humanizer legacy instructions\nKeep deliberate repetition."
	project.Style = "Warm and restrained."
	legacy := project.WritingStyle
	err := applyWritingSettings(&project, &arguments{fields: map[string]json.RawMessage{"writing_style": rawMessage("slow_growth"), "narrative_pov": rawMessage("third_limited")}})
	assert(t, err == nil && project.WritingStyle == "slow_growth" && project.NarrativePov == "Third-person limited", "A valid preset change was rejected")
	assert(t, strings.Contains(project.Style, legacy) && strings.Contains(project.Style, "Warm and restrained."), "The preset change discarded old author instructions")
}

// TestWritingSettingsInvalidInputsAreAtomic checks ten malformed values across the same shared validator.
func TestWritingSettingsInvalidInputsAreAtomic(t *testing.T) {
	for _, fields := range []map[string]json.RawMessage{
		{"writing_style": rawMessage("unlisted-preset")},
		{"writing_style": rawMessage(nil)},
		{"writing_style": rawMessage(7)},
		{"narrative_pov": rawMessage("unknown-viewpoint")},
		{"narrative_pov": rawMessage(nil)},
		{"narrative_pov": rawMessage(9)},
		{"default_chapter_words": rawMessage(0)},
		{"default_chapter_words": rawMessage(50001)},
		{"default_chapter_words": rawMessage(nil)},
		{"writing_style": rawMessage("slow_growth"), "narrative_pov": rawMessage("unknown-viewpoint")},
	} {
		project := testProject()
		before := jsonText(project)
		err := applyWritingSettings(&project, &arguments{fields: fields})
		assert(t, err != nil && jsonText(project) == before, "Invalid settings partially mutated the project")
	}
}

// TestLegacyWritingInstructionsReachPrompt preserves existing custom guidance even before the first repaired save.
func TestLegacyWritingInstructionsReachPrompt(t *testing.T) {
	project := testProject()
	project.WritingStyle = "LEGACY_HUMANIZER Keep distinct character voices."
	project.NarrativePov = "third_limited"
	prompt, err := prepareAI(&project, AIRequest{ProjectID: project.ID, ChapterID: project.Chapters[0].ID, Action: "rewrite", Instruction: "Revise the scene"}, 24000)
	assert(t, err == nil && strings.Contains(jsonText(prompt.Messages), project.WritingStyle) && strings.Contains(jsonText(prompt.Messages), "Third-person limited"), "Old author guidance disappeared from the real prompt")
}

// TestFictionGuidanceIsScopedToProse checks author voice is preserved without contaminating planning.
func TestFictionGuidanceIsScopedToProse(t *testing.T) {
	project := testProject()
	project.Style = "Use spare dialogue, deliberate repetition, and a first-person narrator."
	for _, action := range []string{"continue", "rewrite", "ask", "outline", "architecture", "review"} {
		prompt, err := prepareAI(&project, AIRequest{ProjectID: project.ID, ChapterID: project.Chapters[0].ID, Action: action, Instruction: "Work on this chapter", History: []Message{}}, 16000)
		assert(t, err == nil, fmt.Sprint(err))
		content := jsonText(prompt.Messages)
		prose := action == "continue" || action == "rewrite"
		assert(t, strings.Contains(content, "Fiction prose:") == prose, "Fiction-only guidance leaked into discussion or analysis")
		if prose {
			for _, requirement := range []string{project.Style, "character voices take priority", "Keep deliberate repetition", "plot facts", "planted clues", "without chatbot greetings"} {
				assert(t, strings.Contains(content, requirement), "Prose generation lost a voice or continuity requirement: "+requirement)
			}
		}
	}
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
