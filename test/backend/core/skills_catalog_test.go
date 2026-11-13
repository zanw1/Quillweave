//go:build quillweave_whitebox

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixtureSkill keeps all discovered instruction files inside automatically cleaned test directories.
func writeFixtureSkill(t *testing.T, root, directory, name string) string {
	t.Helper()
	path := filepath.Join(root, directory, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: >\n  Review prose carefully\n  and preserve character choices.\n---\n\nUse the supplied source.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSkillCatalogDiscoversNestedEntriesAndStableVersions exercises saved roots and real SKILL.md loading.
func TestSkillCatalogDiscoversNestedEntriesAndStableVersions(t *testing.T) {
	engine := testEngine(t)
	root := t.TempDir()
	path := writeFixtureSkill(t, root, "nested/editor", "prose-editor")
	catalog, err := engine.setSkillRoots([]string{root, root})
	assert(t, err == nil && len(catalog.Roots) == 1 && len(catalog.Skills) == 1, "Skill roots were not saved and deduplicated")
	descriptor := catalog.Skills[0]
	canonical, err := filepath.EvalSymlinks(path)
	assert(t, err == nil, "fixture Skill path could not be resolved")
	assert(t, descriptor.Name == "prose-editor" && strings.Contains(descriptor.Description, "preserve character choices"), "Skill front matter was not parsed")
	loaded, err := engine.loadSkill(descriptor.ID)
	assert(t, err == nil && loaded.Path == canonical && loaded.BaseDirectory == filepath.Dir(canonical) && loaded.Version == descriptor.Version, "Skill load lost its version or base directory")
	if err := os.WriteFile(path, []byte("---\nname: prose-editor\n---\nUpdated instructions."), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := engine.loadSkill(descriptor.ID)
	assert(t, err == nil && changed.ID == descriptor.ID && changed.Version != descriptor.Version, "changed Skill did not retain identity and update version")
	_, err = engine.loadSkill(filepath.Join(root, "unlisted.md"))
	assert(t, err != nil, "arbitrary file path was accepted as a discovered Skill identity")
}

// TestSkillSelectionLoadsOnlyChosenBodyAndRecordsItsVersion keeps metadata separate from full instructions.
func TestSkillSelectionLoadsOnlyChosenBodyAndRecordsItsVersion(t *testing.T) {
	engine := testEngine(t)
	root := t.TempDir()
	writeFixtureSkill(t, root, "chosen", "chosen")
	other := writeFixtureSkill(t, root, "other", "other")
	if err := os.WriteFile(other, []byte("---\nname: other\ndescription: Other workflow\n---\nSECRET_BODY_MUST_STAY_OUT_OF_PROMPT"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := engine.setSkillRoots([]string{root})
	assert(t, err == nil && len(catalog.Skills) == 2, "fixture Skills were not discovered")
	request := AIRequest{Action: "agent", ModelSelection: &ChatModelSelection{ProfileID: "default", OutputMode: "auto"}, SkillIDs: []string{catalog.Skills[0].ID}}
	prepared, loaded, err := engine.prepareSkills(request)
	assert(t, err == nil && len(loaded) == 1 && loaded[0].Version != "", "selected Skill version was not retained")
	assert(t, strings.Contains(prepared.systemAppend, "Use the supplied source.") && strings.Contains(prepared.systemAppend, "load_skill with skill_id") && !strings.Contains(prepared.systemAppend, "SECRET_BODY_MUST_STAY_OUT_OF_PROMPT"), "unselected Skill body leaked into the prompt")
	frozen := selectedSkillContext(Prompt{Messages: []Message{{Role: "system", Content: "Stable system" + prepared.systemAppend + "\nTool protocol"}}, Skills: loaded})
	assert(t, strings.Contains(frozen, "Use the supplied source.") && !strings.Contains(frozen, "available_skills") && !strings.Contains(frozen, "Tool protocol"), "delegated model did not receive only the frozen selected Skill instructions")
	_, _, err = engine.prepareSkills(AIRequest{SkillIDs: []string{"missing-skill"}})
	assert(t, err != nil, "missing selected Skill was ignored")
}

// TestSkillCatalogReadDoesNotCreateSettingsAndRejectsOversizedInstructions validates discovery and load boundaries.
func TestSkillCatalogReadDoesNotCreateSettingsAndRejectsOversizedInstructions(t *testing.T) {
	engine := testEngine(t)
	_, err := engine.listSkills()
	assert(t, err == nil, "default Skill catalog failed")
	var count int
	err = engine.store.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='skill_settings'").Scan(&count)
	assert(t, err == nil && count == 0, "read-only catalog created persistent settings")
	root := t.TempDir()
	path := writeFixtureSkill(t, root, "large", "large")
	if err := os.WriteFile(path, []byte(strings.Repeat("中", maxSkillBytes)), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := engine.setSkillRoots([]string{root})
	assert(t, err == nil && len(catalog.Skills) == 1, "large Skill metadata could not be listed")
	loaded, err := engine.loadSkill(catalog.Skills[0].ID)
	assert(t, err == nil && loaded.Truncated, "oversized Skill did not expose truncation")
	_, _, err = engine.prepareSkills(AIRequest{SkillIDs: []string{catalog.Skills[0].ID}})
	assert(t, err != nil && strings.Contains(err.Error(), "instruction limit"), "explicit Skill silently used truncated instructions")
}

// TestSkillResourcesStayWithinTheirDiscoveredDirectory reads references and rejects traversal or symlink escapes.
func TestSkillResourcesStayWithinTheirDiscoveredDirectory(t *testing.T) {
	engine := testEngine(t)
	root := t.TempDir()
	main := writeFixtureSkill(t, root, "editor", "editor")
	resource := filepath.Join(filepath.Dir(main), "references", "review.md")
	if err := os.MkdirAll(filepath.Dir(resource), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resource, []byte("Check the author's chosen viewpoint."), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := engine.setSkillRoots([]string{root})
	assert(t, err == nil && len(catalog.Skills) == 1, "resource fixture was not discovered")
	id := catalog.Skills[0].ID
	loaded, err := engine.loadSkillResource(id, "references/review.md")
	assert(t, err == nil && strings.Contains(loaded.Content, "viewpoint") && loaded.ResourcePath == "references/review.md" && loaded.Version != catalog.Skills[0].Version, "relative Skill reference was not loaded with its own version")
	outside := filepath.Join(root, "outside.md")
	if err := os.WriteFile(outside, []byte("Outside text"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = engine.loadSkillResource(id, "../outside.md")
	assert(t, err != nil, "Skill resource traversal escaped its base directory")
	_, err = engine.loadSkillResource(id, outside)
	assert(t, err != nil, "absolute Skill resource path was accepted")
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(main), "escape.md")); err == nil {
		_, err = engine.loadSkillResource(id, "escape.md")
		assert(t, err != nil, "symlink Skill resource escaped its base directory")
	}
}
