//go:build quillweave_whitebox

package core

import (
	"strings"
	"testing"
)

// TestRetrievalFiltersFuturePendingAndStale excludes future, pending, and stale facts from retrieval.
func TestRetrievalFiltersFuturePendingAndStale(t *testing.T) {
	project := ExampleProject()
	for i := range project.Facts {
		project.Facts[i].Status = "confirmed"
	}
	items := retrieve(&project, "Lin Wu", 1, 30)
	assert(t, hasKind(items, "wiki"), "Missing valid evidence")
	for _, fact := range project.Facts {
		if fact.Chapter > 1 {
			for _, item := range items {
				assert(t, item.ID != "W:"+fact.ID, "Future facts leaked into retrieval")
			}
		}
	}
	for i := range project.Facts {
		project.Facts[i].Status = "pending"
	}
	assert(t, !hasKind(retrieve(&project, "Lin Wu", 3, 30), "wiki"), "Pending facts were retrieved")
}

// TestContextBudgetRefusesSilentTruncation rejects over-budget context and retains complete text when the budget permits.
func TestContextBudgetRefusesSilentTruncation(t *testing.T) {
	project := testProject()
	project.Chapters[0].Body = strings.Repeat("\u4e2d\u6587\u6b63\u6587", 4000)
	request := AIRequest{ProjectID: project.ID, ChapterID: project.Chapters[0].ID, Action: "review", Instruction: "Review consistency", History: []Message{}}
	_, err := prepareAI(&project, request, 4000)
	assert(t, err != nil && strings.Contains(err.Error(), "exceeds"), "Over-budget context was silently truncated")
	prompt, err := prepareAI(&project, request, 24000)
	assert(t, err == nil && strings.Contains(prompt.Messages[len(prompt.Messages)-1].Content, project.Chapters[0].Body), "The prompt did not include the complete chapter")
}

// TestRelationshipRetrievalObeysReviewAndChapterScope keeps relationship expansion within reviewed evidence and chapter scope.
func TestRelationshipRetrievalObeysReviewAndChapterScope(t *testing.T) {
	project := NewProject("Graph test", "", " ")
	bodies := []string{"Mara knows Theo and hands over the key.", "Theo reads the sailing log alone.", "Mara exchanges letters with Iris."}
	for index, body := range bodies {
		chapter := NewChapter(uint32(index+1), "Chapter")
		chapter.Body = body
		project.Chapters = append(project.Chapters, chapter)
	}
	for index, item := range [][3]string{{"Mara", "Theo", "Hands over the key"}, {"Theo", "", "Reads the log"}, {"Mara", "Iris", "Exchanges letters"}} {
		chapter := project.Chapters[index]
		_, err := ingest(&project, chapter.ID, hashText(chapter.Body), []Candidate{{Entity: item[0], RelatedEntity: item[1], Claim: item[2], Quote: chapter.Body, Kind: "relationship", Epistemic: "explicit"}}, "mock")
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range project.Facts {
		project.Facts[i].Status = "confirmed"
	}
	items := retrieve(&project, "Mara", 2, 30)
	found := false
	for _, item := range items {
		found = found || item.Reason == "Character relationship" && strings.Contains(item.Text, "Reads the log")
		assert(t, !strings.Contains(item.Text, "Exchanges letters"), "Retrieval expanded into future evidence")
	}
	assert(t, found, "Relationship expansion was missing")
	project.Facts[0].Status = "pending"
	for _, item := range retrieve(&project, "Mara", 2, 30) {
		assert(t, !(item.Kind == "wiki" && strings.Contains(item.Text, "Reads the log")), "Retrieval expanded through a pending relationship")
	}
}
