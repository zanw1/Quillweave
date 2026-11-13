//go:build quillweave_whitebox

package core

import (
	"strings"
	"testing"
)

// importedRepairProject models the earlier import's IDs, composite subjects and marked current-state notes.
func importedRepairProject() Project {
	project := testProject()
	project.Chapters[0].Body = "Mara gives the iron sword to Theo."
	later := NewChapter(260, "Current state")
	later.Body = "Mara is now a teacher."
	project.Chapters = append(project.Chapters, later)
	project.Entities = []Entity{
		{ID: "mara", Name: "Mara", Kind: "character", Aliases: []string{}, Description: "Initial description" + importedProfileMarker + "Mara is now a teacher." + importedLifecycleMarker + "Future author plan."},
		{ID: "theo", Name: "Theo", Kind: "character", Aliases: []string{"The apprentice"}},
		{ID: "compound", Name: "Mara、Theo", Kind: "concept", Aliases: []string{}},
		{ID: "sword", Name: "Iron sword", Kind: "item", Aliases: []string{}},
	}
	project.Facts = []Fact{
		{ID: "fact", Entity: "Mara、Theo", RelatedEntity: "Mara、Theo", Kind: "event", Claim: "The original claim stays intact.", Quote: project.Chapters[0].Body, SourceID: project.Chapters[0].ID, SourceHash: hashText(project.Chapters[0].Body), Status: "confirmed"},
		{ID: "rejected", Entity: "Mara、Theo", Kind: "event", Claim: "An ignored claim.", Quote: project.Chapters[0].Body, SourceID: project.Chapters[0].ID, SourceHash: hashText(project.Chapters[0].Body), Status: "rejected"},
	}
	project.Studio.Memories = []StoryMemory{
		{ID: "memory", Entity: "Mara、Theo", Kind: "fact", Title: "Shared event", Content: "The original claim stays intact.", Quote: project.Chapters[0].Body, SourceID: project.Chapters[0].ID, SourceHash: hashText(project.Chapters[0].Body), Status: "confirmed", Chapter: 1, KnownBy: []string{"Mara、The apprentice", "Mara、Unknown"}},
		{ID: "unknown", Entity: "Unknown name", Kind: "setting", Content: "Untouched author note.", Status: "confirmed", KnownBy: []string{"Unknown"}},
	}
	project.Studio.Plans = []StoryPlan{{ID: "plan", Level: "chapter", Title: "Event", Status: "confirmed", StartChapter: 1, EndChapter: 1, Characters: []string{"mara", "compound", "Mara", "unknown-id"}}}
	project.Sources = []Source{{ID: "style", Title: "Craft", Category: "style", Content: "Do not edit this content."}, {ID: "world", Title: "World", Category: "world", Content: "World source."}, {ID: "legacy", Title: "Legacy", Category: "legacy-note", Content: "Leave unknown categories alone."}}
	project.Studio.Locations = []WorldLocation{{ID: "place", Name: "Village", Kind: "place", EntityIDs: []string{"compound", "mara"}, PlanIDs: []string{}, ChapterIDs: []string{}}}
	return project
}

// TestImportedMetadataRepairAndExactUndo checks every repaired contract and preserves the unmodified manuscript.
func TestImportedMetadataRepairAndExactUndo(t *testing.T) {
	project := importedRepairProject()
	before := jsonText(importedMetadata(&project))
	prose := jsonText(project.Chapters)
	operation := ChangeOperation{TargetKind: TargetImportMetadata, OpType: OpUpsert, AfterValue: rawMessage(importedMetadataRepairConfig{AsOfChapter: 260, PrimaryEntities: map[string]string{"Mara、Theo": "Mara"}, InitialDescriptions: map[string]string{"Mara": "Author-provided initial profile."}})}
	assert(t, applyImportedMetadataRepair(&project, &operation) == nil, "The explicit import repair failed")
	assert(t, jsonText(project.Chapters) == prose, "Import repair changed prose or chapter history")
	assert(t, jsonText(project.Studio.Plans[0].Characters) == `["Mara","unknown-id"]`, "Plan IDs, duplicates or unknown text were mishandled")
	assert(t, len(project.Entities) == 3 && project.ResolveEntity("Mara、Theo") == nil, "The explicitly merged pseudo-entity survived")
	assert(t, project.Sources[0].Category == "craft" && project.Sources[1].Category == "world" && project.Sources[2].Category == "legacy-note", "Category repair guessed beyond the supported compatibility mapping")
	assert(t, project.Entities[0].Description == "Author-provided initial profile."+importedLifecycleMarker+"Future author plan.", "Repair lost the author's initial text or future lifecycle notes")
	assert(t, project.Facts[0].Entity == "Mara" && project.Facts[0].RelatedEntity == "Mara" && project.Facts[0].Status == "pending" && project.Facts[1].Status == "rejected", "Subject repair did not retain review boundaries")
	assert(t, project.Facts[0].Claim == "The original claim stays intact." && project.Facts[0].Quote == project.Chapters[0].Body, "Repair rewrote claims or source quotes")
	memory := project.Studio.Memories[0]
	assert(t, memory.Entity == "Mara" && memory.Status == "proposed" && jsonText(memory.KnownBy) == `["Mara","Theo","Mara、Unknown"]`, "Knowledge attribution was guessed or not returned to review")
	assert(t, project.Studio.Memories[1].Entity == "Unknown name" && project.Studio.Memories[1].Status == "confirmed", "An unrelated unknown author note was changed")
	profile := project.Studio.Memories[2]
	assert(t, profile.Kind == "character" && profile.State == "author_snapshot" && profile.Status == "confirmed" && profile.Chapter == 260 && profile.SourceID == "" && profile.SourceHash == "" && profile.Quote == "" && len(profile.KnownBy) == 0, "An imported author snapshot fabricated source evidence or public knowledge")
	assert(t, !strings.Contains(jsonText(memoryContext(&project, 1)), "Mara is now a teacher.") && strings.Contains(jsonText(memoryContext(&project, 260)), "Mara is now a teacher."), "The current profile ignored its narrative chapter")
	assert(t, jsonText(project.Studio.Locations[0].EntityIDs) == `["mara"]`, "Map links were not redirected and deduplicated")
	var config importedMetadataRepairConfig
	assert(t, decodeJSON(operation.AfterValue, &config) == nil && config.Report.PlansFixed == 1 && config.Report.EntitiesMerged == 1 && config.Report.SourceCategoriesFixed == 1 && config.Report.ProfilesMoved == 1 && config.Report.InitialDescriptions == 1, "The repair report omitted actual work")
	var fields map[string]any
	assert(t, decodeJSON(operation.BeforeValue, &fields) == nil && len(fields) == 6 && fields["chapters"] == nil && fields["changesets"] == nil, "Undo captured unrelated manuscript or change history")
	assert(t, undoImportedMetadataRepair(&project, operation) == nil && jsonText(importedMetadata(&project)) == before && jsonText(project.Chapters) == prose, "Undo did not restore exact original metadata")
}

// TestImportedMetadataRepairIsIdempotent keeps a second application from adding snapshots or disturbing author notes.
func TestImportedMetadataRepairIsIdempotent(t *testing.T) {
	project := importedRepairProject()
	operation := ChangeOperation{TargetKind: TargetImportMetadata, OpType: OpUpsert, AfterValue: rawMessage(importedMetadataRepairConfig{AsOfChapter: 260, PrimaryEntities: map[string]string{"Mara、Theo": "Mara"}})}
	assert(t, applyImportedMetadataRepair(&project, &operation) == nil, "Initial repair failed")
	first := jsonText(importedMetadata(&project))
	assert(t, applyImportedMetadataRepair(&project, &operation) == nil && jsonText(importedMetadata(&project)) == first, "Repeated repair changed metadata or duplicated profile snapshots")
	var config importedMetadataRepairConfig
	assert(t, decodeJSON(operation.AfterValue, &config) == nil && config.Report == (importedMetadataRepairReport{}), "Repeated repair reported changes that did not happen")
}

// TestImportedMetadataRepairProfileKindsAndEmptyCollections preserves item snapshots and empty metadata shapes.
func TestImportedMetadataRepairProfileKindsAndEmptyCollections(t *testing.T) {
	project := importedRepairProject()
	project.Entities[3].Description = "Initial sword." + importedProfileMarker + "The sword is now cracked."
	operation := ChangeOperation{TargetKind: TargetImportMetadata, OpType: OpUpsert, AfterValue: rawMessage(importedMetadataRepairConfig{AsOfChapter: 260})}
	assert(t, applyImportedMetadataRepair(&project, &operation) == nil, "Item profile repair failed")
	last := project.Studio.Memories[len(project.Studio.Memories)-1]
	assert(t, last.Kind == "setting" && last.Entity == "Iron sword" && last.Chapter == 260 && last.Quote == "", "An item snapshot was treated as a sourced character fact")
	empty := NewProject("Empty book", "", "")
	before := jsonText(importedMetadata(&empty))
	operation.AfterValue = rawMessage(importedMetadataRepairConfig{AsOfChapter: 260})
	assert(t, applyImportedMetadataRepair(&empty, &operation) == nil && jsonText(importedMetadata(&empty)) == before, "A no-op repair changed empty metadata collections")
	assert(t, undoImportedMetadataRepair(&empty, operation) == nil && jsonText(importedMetadata(&empty)) == before, "Undo changed empty collection shapes")
}

// TestImportedMetadataRepairRequiresExplicitChoices preserves all entities when no primary subject was supplied.
func TestImportedMetadataRepairRequiresExplicitChoices(t *testing.T) {
	project := importedRepairProject()
	operation := ChangeOperation{TargetKind: TargetImportMetadata, OpType: OpUpsert, AfterValue: rawMessage(importedMetadataRepairConfig{AsOfChapter: 260})}
	assert(t, applyImportedMetadataRepair(&project, &operation) == nil, "A repair without merges failed")
	assert(t, project.ResolveEntity("Mara、Theo") != nil && project.Facts[0].Entity == "Mara、Theo" && project.Facts[0].Status == "confirmed", "The repair guessed a primary subject")
	for _, config := range []importedMetadataRepairConfig{
		{AsOfChapter: 260, PrimaryEntities: map[string]string{"Mara、Theo": "Missing primary"}},
		{AsOfChapter: 260, PrimaryEntities: map[string]string{"Mara、Theo": "Iron sword", "Iron sword": "Mara、Theo"}},
		{AsOfChapter: 260, InitialDescriptions: map[string]string{"Missing person": "Initial profile."}},
		{AsOfChapter: 0},
	} {
		candidate := importedRepairProject()
		before := jsonText(candidate)
		op := ChangeOperation{TargetKind: TargetImportMetadata, OpType: OpUpsert, AfterValue: rawMessage(config)}
		assert(t, applyImportedMetadataRepair(&candidate, &op) != nil && jsonText(candidate) == before && len(op.BeforeValue) == 0, "An invalid repair partially changed the project")
	}
}

// TestImportedMetadataRepairThroughChangesets preserves the normal preview, confirmation and undo contract.
func TestImportedMetadataRepairThroughChangesets(t *testing.T) {
	engine := testEngine(t)
	project := importedRepairProject()
	insertProject(t, engine, project)
	before := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	staged := invoke[ChangeSet](t, engine, "stage_changeset", map[string]any{"project_id": project.ID, "changeset": ChangeSet{Source: "author", Operations: []ChangeOperation{{TargetKind: TargetImportMetadata, OpType: OpUpsert, AfterValue: rawMessage(importedMetadataRepairConfig{AsOfChapter: 260, PrimaryEntities: map[string]string{"Mara、Theo": "Mara"}})}}}})
	preview := invoke[struct {
		Project   Project   `json:"project"`
		ChangeSet ChangeSet `json:"changeset"`
	}](t, engine, "preview_changeset", map[string]any{"project_id": project.ID, "changeset_id": staged.ID})
	assert(t, jsonText(importedMetadata(&preview.Project)) == jsonText(importedMetadata(&before)), "Preview applied the repair before confirmation")
	applied := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "apply_changeset", map[string]any{"project_id": project.ID, "changeset_id": staged.ID, "revision": before.Revision, "review_token": preview.ChangeSet.ReviewToken})
	assert(t, applied.Project.Revision == before.Revision+1 && applied.Project.ResolveEntity("Mara、Theo") == nil, "Confirmed repair was not saved atomically")
	undone := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "undo_changeset", map[string]any{"project_id": project.ID, "changeset_id": staged.ID, "revision": applied.Project.Revision})
	assert(t, jsonText(importedMetadata(&undone.Project)) == jsonText(importedMetadata(&before)) && jsonText(undone.Project.Chapters) == jsonText(before.Chapters), "Normal undo lost original metadata or changed the manuscript")
}
