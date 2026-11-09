//go:build quillweave_whitebox

package core

import (
	"strings"
	"testing"
)

func TestTurnCanOmitCarriedRecords(t *testing.T) {
	project := testProject()
	project.Entities = append(project.Entities,
		Entity{ID: "ent-a", Name: "甲", Kind: "character", Aliases: []string{}, Description: "守墓人"},
		Entity{ID: "ent-b", Name: "乙", Kind: "character", Aliases: []string{}, Description: "掌门"},
	)
	bible := buildStoryBibleLayer1(&project, 24000, &WorkspaceContext{OmitEntityIDs: []string{"ent-a"}})
	if strings.Contains(bible, "甲") || !strings.Contains(bible, "乙") {
		t.Fatalf("omitted entity was still carried:\n%s", bible)
	}
	chapter := &project.Chapters[0]
	block := buildWorkspaceContextBlock(&project, &WorkspaceContext{
		SelectedText:     "被拿掉的句子",
		OmitSelectedText: true,
		OmitChapter:      true,
	}, chapter)
	if strings.Contains(block, "被拿掉的句子") || strings.Contains(block, chapter.Title) {
		t.Fatalf("removed selection or chapter was still carried:\n%s", block)
	}
}

func TestInstructionLimitFollowsModelWindow(t *testing.T) {
	project := testProject()
	instruction := strings.Repeat("字", 8001)
	_, err := prepareAI(&project, AIRequest{ProjectID: project.ID, Action: "ask", Instruction: instruction}, 24000)
	if err != nil && strings.Contains(err.Error(), "8,000") {
		t.Fatal(err)
	}
}
