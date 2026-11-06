//go:build quillweave_whitebox

package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

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
