//go:build quillweave_whitebox

package core

import (
	"testing"
)

// TestStoryLintReportsStructuralGaps distinguishes stale sources, broken links, and planned author entries.
func TestStoryLintReportsStructuralGaps(t *testing.T) {
	project, _ := memoryProject(t)
	project.Facts[0].RelatedEntity = "Missing character"
	project.Chapters[0].Body = "The source text has changed"
	kinds := map[string]bool{}
	for _, finding := range lintWiki(&project) {
		kinds[finding.Kind] = true
		if finding.Kind == "unreferenced_entity" {
			assert(t, finding.Severity == "info", "Author planning was treated as an error")
		}
	}
	assert(t, kinds["broken_relation"] && kinds["stale_evidence"] && kinds["stale_memory"], "Knowledge checks missed structural issues")
}

// TestSimultaneousLocationsAreReviewHints keeps multiple-location notices subject to author interpretation.
func TestSimultaneousLocationsAreReviewHints(t *testing.T) {
	project, _ := memoryProject(t)
	other := project.Facts[0]
	other.ID = newID()
	other.Location = "Coast"
	other.StoryTime = "That night"
	other.Status = "confirmed"
	project.Facts[0].Location = "Lighthouse"
	project.Facts[0].StoryTime = "That night"
	project.Facts[0].Status = "confirmed"
	project.Facts = append(project.Facts, other)
	found := false
	for _, finding := range lintWiki(&project) {
		found = found || finding.Kind == "possible_time_conflict" && finding.Severity == "review"
	}
	assert(t, found, "Time or location review notice was missing")
}
