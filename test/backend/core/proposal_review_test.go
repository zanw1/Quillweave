//go:build quillweave_whitebox

package core

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestRelatedPlanProposalsKeepTheirHierarchy proves parent acceptance rewrites pending child references.
func TestRelatedPlanProposalsKeepTheirHierarchy(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	response := jsonText(map[string]any{"text": "Two related planning proposals.", "proposals": []StudioProposal{
		{Kind: "plan", Title: "Volume", Content: "A volume", Plan: &StoryPlan{ID: "local-volume", Level: "volume", Title: "Volume", StartChapter: 1, EndChapter: 10}},
		{Kind: "plan", Title: "Chapter", Content: "A chapter", Plan: &StoryPlan{ID: "local-chapter", ParentID: "local-volume", Level: "chapter", Title: "Chapter", StartChapter: 2, EndChapter: 2, Goal: "Original goal"}},
	}})
	server := staticModel(t, streamPayload(response), 0)
	config := defaultProvider()
	config.Model = "fixture"
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "action": "outline", "instruction": "Plan a volume and chapter"}))
	assert(t, len(job.Proposals) == 2, job.Error)
	args := map[string]any{"project_id": p.ID, "revision": p.Revision, "job_id": job.ID, "proposal_id": job.Proposals[1].ID, "decision": "accepted"}
	reject(t, engine, "review_proposal", args)
	args["proposal_id"] = job.Proposals[0].ID
	parent := invoke[struct {
		Project Project `json:"project"`
		Job     Job     `json:"job"`
	}](t, engine, "review_proposal", args)
	assert(t, parent.Job.Proposals[1].Plan.ParentID == parent.Project.Studio.Plans[0].ID, "Pending child still points to a model-local ID")
	args["revision"] = parent.Project.Revision
	args["proposal_id"] = job.Proposals[1].ID
	args["content"] = "The author's edited chapter goal"
	child := invoke[struct {
		Project Project `json:"project"`
		Job     Job     `json:"job"`
	}](t, engine, "review_proposal", args)
	assert(t, len(child.Project.Studio.Plans) == 2 && child.Project.Studio.Plans[1].ParentID == child.Project.Studio.Plans[0].ID && child.Project.Studio.Plans[1].Goal == "The author's edited chapter goal", "Accepted hierarchy or edited goal was lost")
}

// TestAgentWriterStagesVersionProtectedProse exercises coordinator, independent writer and explicit acceptance.
func TestAgentWriterStagesVersionProtectedProse(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	server, calls := sequenceModel(t, []string{`{"tool":"writer","query":"Continue the scene","mode":"append"}`, "The keeper crossed the bridge.", `{"tool":"finish","text":"A continuation is ready for your review."}`})
	config := defaultProvider()
	config.Model = "fixture"
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": p.Chapters[0].ID, "action": "agent", "instruction": "Help write the next passage"}))
	assert(t, job.Status == "done" && calls.Load() == 3 && job.Calls == 3 && len(job.Proposals) == 1, job.Error)
	before := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, before.Chapters[0].Body == p.Chapters[0].Body, "The writer applied prose automatically")
	accepted := invoke[struct {
		Project Project `json:"project"`
	}](t, engine, "review_proposal", map[string]any{"project_id": p.ID, "revision": before.Revision, "job_id": job.ID, "proposal_id": job.Proposals[0].ID, "decision": "accepted", "mode": "append"})
	assert(t, strings.HasSuffix(accepted.Project.Chapters[0].Body, "The keeper crossed the bridge.") && len(accepted.Project.Chapters[0].Versions) > 0, "Reviewed prose was not applied with version protection")
}

// TestAgentBudgetAndInvalidProposalDoNotCreateChanges bounds unsuccessful tool exploration.
func TestAgentBudgetAndInvalidProposalDoNotCreateChanges(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	server := staticModel(t, streamPayload(`{"tool":"propose","proposal":{"kind":"plan","title":"Incomplete"}}`), 0)
	config := defaultProvider()
	config.Model = "fixture"
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "action": "agent", "instruction": "Propose a plan"}))
	assert(t, job.Status == "error" && job.Calls == 8 && len(job.Proposals) == 0, "An invalid proposal bypassed structure validation or the call budget")
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": p.ID})
	assert(t, current.Revision == p.Revision && len(current.Studio.Plans) == 0, "Failed exploration modified the work")
}

// TestAgentPrivateContinuationKeepsOriginalVisibleEnvelope checks strict provider continuity across tools and turns.
func TestAgentPrivateContinuationKeepsOriginalVisibleEnvelope(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
	var calls atomic.Int32
	var intermediate, restored atomic.Bool
	first := `{"tool":"recall","query":"world rules"}`
	final := `{"tool":"finish","text":"The author can now choose a direction."}`
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := decodeJSON(readBodyForStudioTest(t, r), &body); err != nil {
			t.Error(err)
		}
		index := calls.Add(1)
		for _, message := range body.Messages {
			if index == 2 && message["content"] == first && message["reasoning_content"] == "PRIVATE_INTERMEDIATE" {
				intermediate.Store(true)
			}
			if index == 3 && message["content"] == final && message["reasoning_content"] == "PRIVATE_FINAL" {
				restored.Store(true)
			}
		}
		text, reasoning := first, "PRIVATE_INTERMEDIATE"
		if index >= 2 {
			text, reasoning = final, "PRIVATE_FINAL"
		}
		fmt.Fprint(w, "data: "+jsonText(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning_content": reasoning}}}})+"\n\n"+streamPayload(text))
	})
	config := nativeConfig("openai")
	config.ProviderID, config.Model, config.BaseURL, config.ContextChars = "moonshot", "kimi-k3.1-pro", server.URL, 24000
	configureModel(t, engine, config)
	args := map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "agent", "instruction": "Explore the world"}
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
	assert(t, job.Status == "done" && intermediate.Load() && job.Text == "The author can now choose a direction.", job.Error)
	args["instruction"] = "Continue the discussion"
	next := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
	assert(t, next.Status == "done" && restored.Load(), "Private state was paired with transformed display prose instead of the original provider envelope")
	runs := invoke[[]Job](t, engine, "get_runs", map[string]any{"project_id": p.ID})
	assert(t, !strings.Contains(jsonText(runs), "PRIVATE_"), "Public history exposed provider-private state")
}

// TestAgentSearchBudgetIncludesFallback bounds external cost even when every configured provider fails.
func TestAgentSearchBudgetIncludesFallback(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	var searches atomic.Int32
	search := modelServer(t, func(w http.ResponseWriter, r *http.Request) { searches.Add(1); w.WriteHeader(429) })
	settings := IntegrationSettings{SearchEnabled: true, MaxSearches: 2, Providers: []SearchProvider{
		{ID: "first", Name: "First", Kind: "searxng", Endpoint: search.URL, Enabled: true},
		{ID: "fallback", Name: "Fallback", Kind: "searxng", Endpoint: search.URL, Enabled: true},
	}}
	invoke[map[string]any](t, engine, "save_integrations", map[string]any{"settings": settings})
	server, _ := sequenceModel(t, []string{`{"tool":"search","query":"history"}`, `{"tool":"search","query":"more history"}`, `{"tool":"finish","text":"Search failed; no historical claim is verified."}`})
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "action": "agent", "instruction": "Research the setting"}))
	assert(t, job.Status == "done" && searches.Load() == 2 && len(job.Steps) == 3 && job.Steps[1].Status == "error", "Fallback requests escaped the total search budget: "+job.Error)
}

// TestShortMessageHistoryIsBounded keeps a long chat from overflowing API message limits despite tiny turns.
func TestShortMessageHistoryIsBounded(t *testing.T) {
	history := []Message{}
	for i := 0; i < 150; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		history = append(history, Message{Role: role, Content: fmt.Sprintf("Turn %d", i)})
	}
	compacted, excerpt := compactHistory(history, 4000)
	assert(t, len(compacted) <= maxHistoryMessages && compacted[len(compacted)-1].Content == "Turn 149" && strings.Contains(excerpt, "incomplete"), "Short-message conversation lost recency or escaped the history bound")
}

// TestChineseNamesInsideNaturalQuestionsAreRetrieved checks short names without exposing future chapters.
func TestChineseNamesInsideNaturalQuestionsAreRetrieved(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	p.Chapters[0].Body = "林舟害怕背叛，却仍将铜钥匙交给陌生人。"
	future := NewChapter(2, "Future")
	future.Body = "林舟后来得知了未公开的秘密。"
	p.Chapters = append(p.Chapters, future)
	insertProject(t, engine, p)
	items, err := engine.store.SearchPassages(p.ID, "林舟的性格", 1)
	assert(t, err == nil && len(items) == 1 && strings.Contains(items[0].Text, "害怕背叛") && !strings.Contains(jsonText(items), "未公开"), "Natural Chinese question missed its source or exposed future information")
}

// TestNarrativeReviewHintsUseEvidenceAndRecordedSetup checks death mentions, overdue payoffs and missing clue setup.
func TestNarrativeReviewHintsUseEvidenceAndRecordedSetup(t *testing.T) {
	p := testProject()
	p.requestLanguage = "zh-CN"
	p.Chapters[0].Body = "林舟死于守桥之战。"
	later := NewChapter(30, "Reappearance")
	later.Body = "林舟走进北塔，命运的齿轮开始转动。"
	p.Chapters = append(p.Chapters, later)
	p.Studio.Memories = []StoryMemory{
		{ID: "death", Kind: "character", Entity: "林舟", Title: "守桥之死", Content: "林舟死亡", State: "dead", Status: "confirmed", Chapter: 12, SourceID: p.Chapters[0].ID, SourceHash: hashText(p.Chapters[0].Body), Quote: "林舟死于守桥之战。"},
		{ID: "open", Kind: "clue", Title: "铜钥匙", Content: "铜钥匙的用途尚未揭示", State: "planted", Status: "confirmed", Chapter: 5, DueChapter: 20},
		{ID: "unprepared", Kind: "clue", Title: "突然出现的解法", Content: "解法已兑现", State: "resolved", Status: "confirmed", Chapter: 25},
	}
	findings := studioFindings(&p, later.ID)
	seen := map[string]bool{}
	for _, finding := range findings {
		seen[finding.Kind] = true
		if finding.Kind == "character_state" {
			assert(t, finding.SourceID == p.Chapters[0].ID && strings.Contains(finding.Detail, p.Chapters[0].Body) && strings.Contains(finding.Detail, "回忆"), "A death mention was declared an error without traceable evidence or narrative qualification")
		}
	}
	assert(t, seen["overdue"] && seen["character_state"] && seen["clue_setup"] && seen["style"], "Narrative review omitted an expected evidence-grounded hint")
	p.Studio.Memories = append(p.Studio.Memories, StoryMemory{ID: "setup", Kind: "clue", Title: "突然出现的解法", Content: "先前铺垫", State: "planted", Status: "superseded", Chapter: 3, ThroughChapter: 24})
	for _, finding := range studioFindings(&p, later.ID) {
		assert(t, finding.Kind != "clue_setup", "A recorded historical setup was ignored")
	}
}
