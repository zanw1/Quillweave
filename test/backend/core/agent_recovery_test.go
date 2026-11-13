//go:build quillweave_whitebox

package core

import (
	"strings"
	"testing"
)

// TestSourceReferenceNormalization accepts every context reference form for get_source_detail.
func TestSourceReferenceNormalization(t *testing.T) {
	project := testProject()
	project.Sources = append(project.Sources, Source{ID: "src-1", Title: "Lighthouse notes", Content: "The keeper keeps a diary."}, Source{ID: "src-2", Title: "Tide table", Content: "Spring tide rises."})
	engine := testEngine(t)
	job := Job{ID: "job-sources"}
	for _, item := range []struct {
		name     string
		step     agentStepEnvelope
		expected string
	}{
		{"bare id", agentStepEnvelope{Tool: "get_source_detail", SourceID: "src-1"}, "The keeper keeps a diary."},
		{"workspace prefix", agentStepEnvelope{Tool: "get_source_detail", SourceID: "S:src-1"}, "The keeper keeps a diary."},
		{"passage id", agentStepEnvelope{Tool: "get_source_detail", SourceID: "S:src-1:3"}, "The keeper keeps a diary."},
		{"exact title", agentStepEnvelope{Tool: "get_source_detail", SourceID: "lighthouse notes"}, "The keeper keeps a diary."},
		{"query fallback", agentStepEnvelope{Tool: "get_source_detail", Query: "tide"}, "Spring tide rises."},
	} {
		result, err := engine.executeScopedAgentTool(&project, job, item.step)
		assert(t, err == nil && strings.Contains(result, item.expected), item.name+" did not resolve the source: "+result)
	}
	_, err := engine.executeScopedAgentTool(&project, job, agentStepEnvelope{Tool: "get_source_detail", SourceID: "made-up"})
	assert(t, err != nil && strings.Contains(err.Error(), "src-1") && strings.Contains(err.Error(), "S:<source_id>"), "The miss error did not list reusable IDs: "+err.Error())
}

// TestUnescapedInnerQuotesStillDecode repairs ASCII quotes inside Chinese prose values.
func TestUnescapedInnerQuotesStillDecode(t *testing.T) {
	text := `{"tool":"propose_changes","operations":[{"target_kind":"entity","op_type":"upsert","title":"守塔人","after_value":{"name":"林雾","description":"她说"灯塔是空的"，然后离开了小岛。"}}]}`
	step, ok := recoverAgentStep(text)
	if !ok || len(step.Operations) != 1 {
		t.Fatalf("broken-quote operations were not recovered: %+v", step)
	}
	assert(t, step.Operations[0].TargetKind == TargetEntity && step.Operations[0].OpType == OpUpsert, "operation enums were not preserved")
	assert(t, strings.Contains(string(step.Operations[0].AfterValue), "灯塔是空的"), "prose with quotes was lost from after_value")
}

// TestScrapeRescuesStructuredFields recovers source_id and operations when the envelope stays invalid JSON.
func TestScrapeRescuesStructuredFields(t *testing.T) {
	text := "{\"tool\":\"get_source_detail\",\"source_id\":\"S:src-9:2\",\"offset_chars\":1200,\"query\":\"notes\"}"
	step, ok := scrapeAgentStep(text)
	if !ok {
		t.Fatal("scrape did not recognize the tool")
	}
	assert(t, step.SourceID == "S:src-9:2" && step.Offset == 1200 && step.Query == "notes", "scraped fields were incomplete")

	proposal := `{"tool":"propose_changes","operations":[{"target_kind":"entity","op_type":"upsert","title":"守塔人","after_value":{"name":"林雾","description":"守塔人"}}],"summary":"他说"好,再见"然后离开"}`
	step, ok = scrapeAgentStep(proposal)
	if !ok || len(step.Operations) != 1 {
		t.Fatalf("scrape did not rescue operations: %+v", step)
	}
	assert(t, strings.Contains(string(step.Operations[0].AfterValue), "林雾"), "rescued after_value lost its fields")
}

// TestStageAgentChangeSetExplainsRejections surfaces the validator reason instead of a generic failure.
func TestStageAgentChangeSetExplainsRejections(t *testing.T) {
	engine := testEngine(t)
	engine.jobsMu.Lock()
	engine.jobs["job-cs"] = &Job{ID: "job-cs"}
	engine.jobsMu.Unlock()
	project := testProject()
	_, err := engine.stageAgentChangeSet("job-cs", &project, nil, nil)
	assert(t, err != nil && strings.Contains(err.Error(), "operations"), "missing operations were not explained: "+err.Error())
	_, err = engine.stageAgentChangeSet("job-cs", &project, nil, []ChangeOperation{{TargetKind: "Plan", OpType: OpUpsert, AfterValue: []byte(`{"title":"卷一"}`)}})
	assert(t, err != nil && strings.Contains(err.Error(), "Invalid changeset target kind"), "enum rejection was not passed through: "+err.Error())
	n, err := engine.stageAgentChangeSet("job-cs", &project, nil, []ChangeOperation{{TargetKind: TargetEntity, OpType: OpUpsert, Title: "林雾", AfterValue: []byte(`{"name":"林雾"}`)}})
	assert(t, err == nil && n == 1, "a valid operation set was rejected")
}

// TestSourceIDShadowsQueryFallback keeps an exact source_id match ahead of the title/query fallback.
func TestSourceIDShadowsQueryFallback(t *testing.T) {
	project := testProject()
	project.Sources = append(project.Sources,
		Source{ID: "src-a", Title: "Tide table", Content: "wrong source"},
		Source{ID: "src-b", Title: "Lighthouse diary", Content: "keeper notes"},
	)
	result, err := testEngine(t).executeScopedAgentTool(&project, Job{ID: "job-shadow"}, agentStepEnvelope{Tool: "get_source_detail", SourceID: "src-b", Query: "tide"})
	assert(t, err == nil && strings.Contains(result, "keeper notes"), "exact source_id was shadowed by the query fallback: "+result)
}

// TestStepSignatureSeparatesPayloads ensures different proposals are not counted as identical retries.
func TestStepSignatureSeparatesPayloads(t *testing.T) {
	first := agentStepEnvelope{Tool: "propose", Proposal: &StudioProposal{Kind: "decision", Title: "A", Content: "first"}}
	second := agentStepEnvelope{Tool: "propose", Proposal: &StudioProposal{Kind: "decision", Title: "B", Content: "second"}}
	assert(t, agentStepSignature(first) != agentStepSignature(second), "different proposals shared one signature")
	same := agentStepEnvelope{Tool: "propose", Proposal: &StudioProposal{Kind: "decision", Title: "A", Content: "first"}}
	assert(t, agentStepSignature(first) == agentStepSignature(same), "identical proposals produced different signatures")
	opsA := agentStepEnvelope{Tool: "propose_changes", Operations: []ChangeOperation{{TargetKind: TargetEntity, OpType: OpUpsert, Title: "A", AfterValue: []byte(`{"name":"林雾"}`)}}}
	opsB := agentStepEnvelope{Tool: "propose_changes", Operations: []ChangeOperation{{TargetKind: TargetEntity, OpType: OpUpsert, Title: "B", AfterValue: []byte(`{"name":"陆川"}`)}}}
	assert(t, agentStepSignature(opsA) != agentStepSignature(opsB), "different operation sets shared one signature")
}

// TestProtocolNotesStrippedFromReplay removes the ephemeral final-call note before retention.
func TestProtocolNotesStrippedFromReplay(t *testing.T) {
	messages := appendFinalCallNote([]Message{{Role: "user", Content: "Tool failed: X"}}, true)
	assert(t, strings.Contains(messages[0].Content, "[Protocol note]"), "the note was not appended")
	stripped := stripProtocolNotes(messages)
	assert(t, stripped[0].Content == "Tool failed: X", "the note was not stripped: "+stripped[0].Content)
}
