//go:build quillweave_whitebox

package core

import (
	"strings"
	"testing"
)

func TestBrokenToolEnvelopeBecomesAuthorProse(t *testing.T) {
	question := "{\"tool\":\"ask_author\",\"text\":\"“玄幻修仙”是一个爽文框架。\n\n为了落地，先从金手指切入。\"}"
	visible := stripModelToolJSON(question)
	if strings.Contains(visible, `"tool"`) || strings.Contains(visible, "ask_author") {
		t.Fatalf("question envelope leaked: %s", visible)
	}
	if !strings.Contains(visible, "金手指切入") || !strings.Contains(visible, "\n\n") {
		t.Fatalf("question prose was not preserved: %s", visible)
	}

	proposal := "{\"tool\":\"propose\",\"proposal\":{\"kind\":\"decision\",\"title\":\"作品立项总纲\",\"content\":\"摒弃\"一次脑补\"的写法。\\n* 书名：《让你苟命养生》\"}}json"
	visible = stripModelToolJSON(proposal)
	if strings.Contains(visible, `"tool"`) || strings.Contains(visible, `"}json`) {
		t.Fatalf("proposal envelope leaked: %s", visible)
	}
	if !strings.Contains(visible, "作品立项总纲") || !strings.Contains(visible, "让你苟命养生") || !strings.Contains(visible, "一次脑补") {
		t.Fatalf("proposal prose was not preserved: %s", visible)
	}
	step, ok := recoverAgentStep(proposal)
	if !ok || step.Proposal == nil || step.Proposal.Kind != "decision" || step.Proposal.Title != "作品立项总纲" {
		t.Fatalf("proposal was not recoverable: %+v", step.Proposal)
	}
	if stripModelToolJSON("先聊聊主角。") != "先聊聊主角。" {
		t.Fatal("plain prose changed")
	}
}
