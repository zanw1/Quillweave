//go:build quillweave_whitebox

package core

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// smallWikiConfig supplies the small token window that previously accepted oversized Chinese character chunks.
func smallWikiConfig() ProviderConfig {
	config := defaultProvider()
	config.Model = "small-wiki-fixture"
	config.ContextChars = 32768
	config.MaxTokens = 1024
	config.ModelLimits = &ModelLimits{ContextWindowTokens: 8192, MaxInputTokens: 7168, MaxOutputTokens: 1024}
	return config
}

// TestWikiChunkBudgetUsesCompleteTokenInput checks Unicode coverage, output reserve and fixed extraction overhead.
func TestWikiChunkBudgetUsesCompleteTokenInput(t *testing.T) {
	project := testProject()
	config := smallWikiConfig()
	source := strings.Repeat("甲乙丙丁", 3000)
	parts, err := prepareWikiChunks(&project, source, config)
	assert(t, err == nil && len(parts) > 2, "An 8K-token model retained oversized Chinese chunks")
	characters := []rune(source)
	start, previousEnd := 0, 0
	for _, part := range parts {
		messages := extractionPrompt(part, &project)
		assert(t, wikiMessagesBudget(config, messages) == nil, "Prepared extraction chunk does not fit its actual model request")
		assert(t, requestInputTokens(config, messages) <= tokenInputLimit(config), "Extraction consumed the reserved output space")
		size := len([]rune(part))
		previousEnd = start + size
		assert(t, previousEnd <= len(characters) && string(characters[start:previousEnd]) == part, "Wiki chunk changed or skipped source characters")
		start = previousEnd - min(150, size/4)
	}
	assert(t, previousEnd == len(characters), "Source characters were dropped while splitting Wiki chunks")
	project.WikiPurpose = strings.Repeat("固定规则", 3000)
	_, err = prepareWikiChunks(&project, source, config)
	assert(t, err != nil && strings.Contains(err.Error(), "instructions and tracking focus"), "Oversized fixed Wiki instructions were deferred until a network request")
}

// TestWikiBothStagesFitSmallModel exercises preview, extraction and multi-batch memory requests through normal commands.
func TestWikiBothStagesFitSmallModel(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	project.Chapters[0].Body = strings.Repeat("甲乙丙丁", 3000)
	source := project.Chapters[0].Body
	for index := 0; index < 6; index++ {
		project.Facts = append(project.Facts, Fact{
			ID: newID(), Entity: "Lin Wu", Kind: "event", Epistemic: "explicit", Status: "pending",
			SourceID: project.Chapters[0].ID, SourceHash: hashText(source), Chapter: 1,
			Claim: fmt.Sprintf("事实%d", index) + strings.Repeat("线索", 400), Quote: string([]rune(source)[:800]),
		})
	}
	insertProject(t, engine, project)
	config := smallWikiConfig()
	var extractionCalls, compilationCalls atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []Message `json:"messages"`
		}
		if err := decodeJSON(readBodyForStudioTest(t, r), &body); err != nil {
			t.Error(err)
			return
		}
		if wikiMessagesBudget(config, body.Messages) != nil {
			t.Error("A Wiki network request exceeded the small model budget")
		}
		if strings.Contains(body.Messages[0].Content, "You compile a fiction Wiki") {
			index := extractionCalls.Add(1)
			candidate := Candidate{Entity: "Lin Wu", Kind: "event", Epistemic: "explicit", Claim: fmt.Sprintf("新线索%d", index), Quote: "甲乙丙丁"}
			fmt.Fprint(w, streamPayload(jsonText(map[string]any{"facts": []Candidate{candidate}})))
			return
		}
		compilationCalls.Add(1)
		_, evidence, _ := strings.Cut(body.Messages[1].Content, "<evidence>\n")
		evidence, _, _ = strings.Cut(evidence, "\n</evidence>")
		var facts []struct {
			ID string `json:"id"`
		}
		if err := decodeJSON([]byte(evidence), &facts); err != nil {
			t.Error(err)
			return
		}
		ids := []string{}
		for _, fact := range facts {
			ids = append(ids, fact.ID)
		}
		fmt.Fprint(w, streamPayload(jsonText(MemorySection{Summary: "线索已经整理。", EvidenceIDs: ids, Questions: []string{}})))
	})
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	args := AIRequest{History: []Message{}, ProjectID: project.ID, ChapterID: project.Chapters[0].ID, Action: "wiki", Instruction: "", ModelSelection: &ChatModelSelection{ProfileID: "default", OutputMode: "custom", MaxTokens: 1024}}
	prompt := invoke[Prompt](t, engine, "prepare_ai", args)
	assert(t, len(prompt.Messages) == 2 && prompt.Budget != nil && prompt.Budget.CanSend && prompt.Budget.InputTokensEstimate > 0, "Wiki preview omitted the real extraction request")
	assert(t, wikiMessagesBudget(config, prompt.Messages) == nil, "Previewed Wiki extraction does not fit its model")
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
	assert(t, job.Status == "done" && extractionCalls.Load() > 2 && compilationCalls.Load() > 1, "Wiki stages were not split to fit the selected model: "+job.Error)
	latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(latest.Compilations) == 1 && len(latest.Compilations[0].Sections) == int(compilationCalls.Load()), "Budgeted compilation lost its evidence batches")
}
