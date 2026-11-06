//go:build quillweave_whitebox

package core

import (
	"reflect"
	"strings"
	"testing"
)

// TestAliasesAndRenamesKeepIdentity checks identity-preserving renames and unique aliases.
func TestAliasesAndRenamesKeepIdentity(t *testing.T) {
	engine := testEngine(t)
	project := ExampleProject()
	insertProject(t, engine, project)
	entity := project.Entities[0]
	entity.Name = "Lin Xiaowu"
	entity.Aliases = []string{"Lin Wu"}
	next := invoke[Project](t, engine, "save_entity", map[string]any{"project_id": project.ID, "revision": 0, "entity": entity})
	assert(t, next.Facts[0].Entity == "Lin Xiaowu", "Existing facts did not follow the entity rename")
	other := project.Entities[1]
	other.Aliases = []string{"Lin Wu"}
	reject(t, engine, "save_entity", map[string]any{"project_id": project.ID, "revision": 1, "entity": other})
}

// TestRenamedEntityDoesNotDuplicateFacts checks that a rename retaining an alias creates no duplicate fact.
func TestRenamedEntityDoesNotDuplicateFacts(t *testing.T) {
	project := testProject()
	ingestCandidate(t, &project)
	project.Entities[0].Name = "Lin Xiaowu"
	project.Entities[0].Aliases = []string{"Lin Wu"}
	project.Facts[0].Entity = "Lin Xiaowu"
	count, err := ingest(&project, project.Chapters[0].ID, hashText(project.Chapters[0].Body), []Candidate{testCandidate()}, "mock")
	assert(t, err == nil && count == 0, "Renaming inserted duplicate facts")
}

// TestGraphFiltersChaptersReviewAndStaleSources checks chapter, review, and source freshness boundaries in graph projections.
func TestGraphFiltersChaptersReviewAndStaleSources(t *testing.T) {
	project := ExampleProject()
	graph := buildGraph(&project, 1, false)
	chapters := 0
	ids := map[string]bool{}
	for _, node := range graph.Nodes {
		ids[node.ID] = true
		if node.Kind == "chapter" {
			chapters++
		}
	}
	assert(t, chapters == 1, "The graph exposed future sources")
	for _, edge := range graph.Edges {
		assert(t, edge.Status == "confirmed" && ids[edge.Source] && ids[edge.Target], "The graph contained pending or dangling edges")
	}
	all := buildGraph(&project, 3, true)
	pending := false
	for _, edge := range all.Edges {
		pending = pending || edge.Status == "pending"
	}
	assert(t, pending, "Pending evidence layer was missing")
	project.Chapters[0].Body += "Revision"
	stale := buildGraph(&project, 3, true)
	assert(t, stale.OmittedStale == 2, "Incorrect stale evidence count")
	for _, node := range stale.Nodes {
		assert(t, node.SourceID == nil || *node.SourceID != project.Chapters[0].ID, "A stale source remained in the graph")
	}
}

// TestGraphIdentitySurvivesAliasesAndReportsMissingTargets checks stable graph identity through aliases and unresolved targets.
func TestGraphIdentitySurvivesAliasesAndReportsMissingTargets(t *testing.T) {
	project := ExampleProject()
	initial := buildGraph(&project, 3, false)
	project.Entities[0].Aliases = append(project.Entities[0].Aliases, "Lin Wu")
	project.Entities[0].Name = "Lin Xiaowu"
	renamed := buildGraph(&project, 3, false)
	assert(t, reflect.DeepEqual(initial.Edges, renamed.Edges), "Renaming changed edge identity")
	found := false
	for _, node := range renamed.Nodes {
		found = found || node.Label == "Lin Xiaowu" && node.ID == "entity:"+project.Entities[0].ID
	}
	assert(t, found, "Renaming changed node identity")
	project.Facts[2].RelatedEntity = "Uncreated character"
	missing := buildGraph(&project, 3, false)
	assert(t, missing.UnresolvedRelations == 1, "An unresolved relationship was not reported")
	for _, node := range missing.Nodes {
		assert(t, node.Label != "Uncreated character", "A relationship target was fabricated")
	}
}

// TestGraphAndRetrievalShareIdentityValidation checks shared alias resolution and rejection of duplicate imported entity IDs.
func TestGraphAndRetrievalShareIdentityValidation(t *testing.T) {
	engine := testEngine(t)
	project := ExampleProject()
	project.Entities[1].Aliases = append(project.Entities[1].Aliases, "The keeper")
	project.Facts[2].RelatedEntity = "The keeper"
	project.Facts[1].Claim = "Stops visitors from entering the lighthouse"
	assert(t, buildGraph(&project, 3, false).UnresolvedRelations == 0, "The graph failed to resolve an alias")
	for _, finding := range lintWiki(&project) {
		assert(t, finding.Kind != "broken_relation", "An alias relationship was treated as a broken link")
	}
	found := false
	for _, item := range retrieve(&project, "Lin Wu", 2, 30) {
		found = found || item.Reason == "Character relationship" && strings.Contains(item.Title, "Lu Chuan")
	}
	assert(t, found, "Retrieval failed to resolve an alias")
	project.Entities[1].ID = project.Entities[0].ID
	reject(t, engine, "import_archive", map[string]any{"content": jsonText(map[string]any{"format": "quillweave-1", "project": project})})
	list, err := engine.store.List()
	assert(t, err == nil && len(list) == 0, "An invalid backup left a partial project")
}
