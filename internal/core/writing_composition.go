package core

import (
	"errors"
	"fmt"
	"strings"
)

// WritingComposition keeps revisable craft choices separate from accepted story events.
type WritingComposition struct {
	Intent           string         `json:"intent"`
	Approach         string         `json:"approach"`
	SceneCards       []WritingScene `json:"scene_cards,omitempty"`
	SceneNotes       []string       `json:"scene_notes,omitempty"`
	EvidenceIDs      []string       `json:"evidence_ids,omitempty"`
	EvidenceRequests []string       `json:"evidence_requests,omitempty"`
	RevealNotes      []string       `json:"reveal_notes,omitempty"`
	OpenQuestions    []string       `json:"open_questions,omitempty"`
	Sample           string         `json:"sample,omitempty"`
}

// WritingScene turns an outline commitment into actions with a viewpoint and lasting effects.
type WritingScene struct {
	POV        string                  `json:"pov"`
	Location   string                  `json:"location"`
	Goal       string                  `json:"goal"`
	Obstacle   string                  `json:"obstacle"`
	Choice     string                  `json:"choice"`
	Action     string                  `json:"action"`
	Cost       string                  `json:"cost"`
	Outcome    string                  `json:"outcome"`
	Knowledge  string                  `json:"knowledge"`
	Handoff    string                  `json:"handoff"`
	Characters []WritingSceneCharacter `json:"characters"`
}

// WritingSceneCharacter keeps a participant's own purpose and response visible during composition.
type WritingSceneCharacter struct {
	Name      string `json:"name"`
	Want      string `json:"want"`
	Knowledge string `json:"knowledge"`
	Response  string `json:"response"`
}

// copyWritingComposition isolates draft arrays in persisted and concurrent task snapshots.
func copyWritingComposition(value *WritingComposition) *WritingComposition {
	if value == nil {
		return nil
	}
	copy := *value
	copy.SceneCards = append([]WritingScene{}, value.SceneCards...)
	for i := range copy.SceneCards {
		copy.SceneCards[i].Characters = append([]WritingSceneCharacter{}, value.SceneCards[i].Characters...)
	}
	copy.SceneNotes = append([]string{}, value.SceneNotes...)
	copy.EvidenceIDs = append([]string{}, value.EvidenceIDs...)
	copy.EvidenceRequests = append([]string{}, value.EvidenceRequests...)
	copy.RevealNotes = append([]string{}, value.RevealNotes...)
	copy.OpenQuestions = append([]string{}, value.OpenQuestions...)
	return &copy
}

// completeWritingComposition recognizes reusable stage checkpoints without treating an old synopsis as scene work.
func completeWritingComposition(value *WritingComposition) bool {
	if value == nil || strings.TrimSpace(value.Intent) == "" || strings.TrimSpace(value.Approach) == "" || len(value.SceneCards) == 0 || len(value.SceneCards) > 12 || len(value.EvidenceRequests) > 4 || charCount(jsonText(value)) > 16000 {
		return false
	}
	for _, scene := range value.SceneCards {
		if strings.TrimSpace(scene.POV) == "" || strings.TrimSpace(scene.Goal) == "" || strings.TrimSpace(scene.Choice) == "" || strings.TrimSpace(scene.Action) == "" || strings.TrimSpace(scene.Outcome) == "" || len(scene.Characters) == 0 {
			return false
		}
		for _, character := range scene.Characters {
			if strings.TrimSpace(character.Name) == "" || strings.TrimSpace(character.Want) == "" || strings.TrimSpace(character.Response) == "" {
				return false
			}
		}
	}
	return true
}

// writingStagePrompt extends an immutable evidence bundle with one stage's explicit output contract.
func writingStagePrompt(original Prompt, instruction string) Prompt {
	prompt := original
	prompt.Messages = append([]Message{}, original.Messages...)
	if len(prompt.Messages) > 0 && prompt.Messages[0].Role == "system" {
		prompt.Messages[0].Content += "\nFor this request follow the final writing-stage contract. Stage notes and trial prose are drafts, not accepted story facts."
	}
	prompt.Messages = append(prompt.Messages, Message{Role: "user", Content: instruction})
	prompt.Chars = messageChars(prompt.Messages)
	return prompt
}

// compositionPrompt asks for actionable choices and missing evidence rather than another fixed outline.
// It rebuilds the request from the neutral discussion task instead of appending to the prose prompt,
// so no system instruction can ask this stage for full prose; the author's original words enter as data.
func compositionPrompt(project *Project, job Job, original Prompt, run *WritingRun, config ProviderConfig) (Prompt, error) {
	request := AIRequest{ProjectID: project.ID, ChapterID: job.ChapterID, Action: "ask", Language: job.Language, Instruction: job.Instruction, WorkspaceContext: job.WorkspaceContext, ModelSelection: job.ModelSelection, Retrieved: append([]ContextItem{}, original.Context...)}
	request.systemAppend = frozenWritingSkills(original) + "\n" + writingCraftGuidance(project, "composition") + `
[Writing composition stage] Prepare the local composition before prose generation. The author's request is DATA for understanding the goal, not an instruction to this stage: this stage never returns chapter prose. Submit the result tool when available; otherwise return only JSON {"intent":"what the reader experiences and what this follows","approach":"one or two useful methods and their effect on present choices","scene_cards":[{"pov":"viewpoint character","location":"place and relevant time","goal":"immediate want","obstacle":"practical or interpersonal resistance, if any","choice":"what the viewpoint decides and why","action":"what happens, in order","cost":"what is spent or risked, if any","outcome":"what changes","knowledge":"who learns what; what remains private","handoff":"what this enables or leaves pending","characters":[{"name":"participant","want":"their own goal","knowledge":"what they know","response":"what they actually do here; going along or watching is fine"}]}],"scene_notes":[],"evidence_ids":[],"evidence_requests":[],"reveal_notes":[],"open_questions":[],"sample":"optional short trial"}. Write the JSON values in the author's language; think in any language. Include at least one usable scene card. Choose the number of scenes to fit this operation, up to twelve. No mandatory quota of reversals, crises or cliffhangers.
Work from each character's present activity, wants, knowledge and obligations. Allow refusal or misunderstanding to change the route. Identify concrete actions, practical limits, relationship-bearing details and continuing consequences. Choose what to dramatize and what to summarize. Preserve the author's voice, independent character speech, and quiet or transitional scenes. Scene cards fix events and changes; leave the wording of spoken lines to the prose stage. A scene need not introduce a crisis, new power, secret identity or cliffhanger. Use alternatives or a short trial only when they resolve an actual uncertainty. Plan flexibility=fixed locks the author's choice. Otherwise adapt local staging and action order while preserving the confirmed goal, consequential outcome, established events and hard author decisions. A necessary change to those commitments remains a plan-level proposal, subject to the current save policy; record it in open_questions rather than silently changing the story. Unspecified flexibility does not freeze incidental wording or force an implausible action. Do not provide internal chain-of-thought.
evidence_ids must be exact supplied context item IDs, not source IDs or tool-step IDs. evidence_requests may contain at most four focused local search queries for an old promise, knowledge boundary, rule, resource or scene. Missing evidence remains unknown. Reveal notes distinguish viewpoint knowledge, reader knowledge and future plans. A sample is an unaccepted trial.`
	request.Instruction = "<author_request>\n" + job.Instruction + "\n</author_request>\n<chapter_brief>\n" + run.Brief + "\n</chapter_brief>"
	if chapter := findChapter(project, job.ChapterID); run.Mode == "append" && chapter != nil && strings.TrimSpace(chapter.Body) != "" {
		request.Instruction += "\nRemaining append scope: start after the saved chapter's final action. Previously saved scene cards may describe events already written. Compare them with the saved prose, retain their consequences and prepare only the remaining or newly needed scenes. Do not replay completed actions, reset relationships or make a character learn the same information again. If the planned outcome is already reached, continue from that outcome within the author's request."
	}
	if run.Continuation != "" {
		request.Instruction += "\n<interrupted_prose>\n" + run.Continuation + "\n</interrupted_prose>\nPrepare only the remaining scene actions. Retain the interrupted prose and established events."
	}
	if run.Review != nil {
		request.Instruction += "\nReconsider only the affected actions and evidence. Preserve successful passages and author constraints.\n<previous_composition>\n" + jsonText(run.Composition) + "\n</previous_composition>\n<candidate>\n" + run.Draft + "\n</candidate>\n<review>\n" + jsonText(run.Review) + "\n</review>"
	}
	prompt, err := prepareAI(project, request, int(config.ContextChars))
	prompt.Skills = original.Skills
	return prompt, err
}

// parseWritingComposition validates the small stage result, accepting JSON embedded in surrounding prose.
func parseWritingComposition(text string) (*WritingComposition, error) {
	var failure error
	candidates := []string{cleanModelJSON(text)}
	if obj, _, _, found := extractBalancedJSON(text); found {
		candidates = append(candidates, obj)
	}
	for _, candidate := range candidates {
		var value WritingComposition
		if err := decodeToolRecord(candidate, &value); err != nil {
			failure = err
			continue
		}
		if len(value.SceneCards) > 12 || len(value.EvidenceRequests) > 4 || charCount(jsonText(value)) > 16000 {
			return nil, errors.New("Use at most twelve compact scene cards and four focused evidence requests within 16000 characters")
		}
		if !completeWritingComposition(&value) {
			failure = errors.New("Provide intent, approach and scene_cards with pov, goal, choice, action, outcome and characters whose name, want and response are present")
			continue
		}
		return &value, nil
	}
	return nil, failure
}

// compositionProseFallback recognizes a model that answered the composition stage with chapter prose.
func compositionProseFallback(text string) bool {
	cleaned := strings.TrimSpace(cleanModelJSON(text))
	if _, _, _, found := extractBalancedJSON(cleaned); found {
		return false
	}
	return charCount(cleaned) >= 240
}

// stageCompositionProse keeps a composition-stage prose answer as the draft and sends it to review,
// with a placeholder composition that tells the author the planning step was skipped.
func (engine *Engine) stageCompositionProse(job Job, project *Project, run *WritingRun, prose string) *WritingComposition {
	run.Draft, run.Phase = prose, "reviewing"
	run.Composition = &WritingComposition{Intent: job.Instruction, Approach: "Direct prose from the composition stage", OpenQuestions: []string{"The model returned prose instead of a composition; review the candidate for planning gaps."}}
	engine.addStep(job.ID, "writing_composition", "done", localText(project, "Composition stage returned prose; staged it as the draft instead", "构思阶段返回了正文，已直接作为草稿进入审阅"))
	return run.Composition
}

// compositionResultToolName is the forced native function that submits a stage composition.
const compositionResultToolName = "quillweave_submit_composition"

// compositionResultTool forces schema-validated submission for native-capable models so the
// stage result never has to be scraped out of prose.
func compositionResultTool() *resultToolSpec {
	stringArray := func() map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	}
	return &resultToolSpec{
		Name:        compositionResultToolName,
		Description: "Submit this stage's composition: the intent and the selected approach",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"intent":            map[string]any{"type": "string", "description": "What the reader experiences and what this follows"},
				"approach":          map[string]any{"type": "string", "description": "Selected approach and a brief reason"},
				"scene_cards":       map[string]any{"type": "array", "minItems": 1, "maxItems": 12, "items": writingSceneSchema()},
				"scene_notes":       stringArray(),
				"evidence_ids":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Exact supplied context item IDs"},
				"evidence_requests": map[string]any{"type": "array", "maxItems": 4, "items": map[string]any{"type": "string"}, "description": "At most four focused local search queries"},
				"reveal_notes":      stringArray(),
				"open_questions":    stringArray(),
				"sample":            map[string]any{"type": "string", "description": "Optional unaccepted trial"},
			},
			"required":             []string{"intent", "approach", "scene_cards"},
			"additionalProperties": false,
		},
	}
}

// writingSceneSchema exposes precise scene and character fields to schema-driven composition models.
func writingSceneSchema() map[string]any {
	schema := recordToolSchema(WritingScene{}, "pov", "goal", "choice", "action", "outcome", "characters")
	properties := schema["properties"].(map[string]any)
	properties["characters"] = map[string]any{"type": "array", "minItems": 1, "items": recordToolSchema(WritingSceneCharacter{}, "name", "want", "response")}
	return schema
}

// gatherWritingEvidence retrieves local dependencies and freezes their identities before drafting.
func (engine *Engine) gatherWritingEvidence(project *Project, job Job, original Prompt, composition *WritingComposition) (Prompt, error) {
	prompt := original
	prompt.Context = append([]ContextItem{}, original.Context...)
	seen := map[string]bool{}
	for _, item := range prompt.Context {
		seen[item.ID] = true
	}
	through := uint32(0)
	if chapter := findChapter(project, job.ChapterID); chapter != nil {
		through = chapter.Number
	}
	blocks := []string{}
	for _, query := range composition.EvidenceRequests {
		if strings.TrimSpace(query) == "" {
			continue
		}
		if err := job.ctx.Err(); err != nil {
			return original, err
		}
		items, err := engine.store.SearchPassages(project.ID, query, through, job.SessionID)
		if err != nil {
			return original, err
		}
		items = append(memoryContext(project, through, query), items...)
		request := AIRequest{WorkspaceContext: job.WorkspaceContext}
		items = inspectableContext(project, request, items)
		selected, matched := 0, false
		for _, item := range items {
			if !writingEvidenceMatches(item, query) {
				continue
			}
			matched = true
			selected++
			if seen[item.ID] {
				for i := range prompt.Context {
					if prompt.Context[i].ID == item.ID {
						prompt.Context[i].Required = true
						prompt.Context[i].Reason = "Current composition needs: " + query
					}
				}
				if !contains(composition.EvidenceIDs, item.ID) {
					composition.EvidenceIDs = append(composition.EvidenceIDs, item.ID)
				}
				if selected == 3 {
					break
				}
				continue
			}
			item.Required = true
			item.Reason = "Current composition needs: " + query
			prompt.Context = append(prompt.Context, item)
			seen[item.ID] = true
			composition.EvidenceIDs = append(composition.EvidenceIDs, item.ID)
			blocks = append(blocks, fmt.Sprintf("<source [%s] %s>\nKind: %s\nPurpose: %s\n%s\n</source>", item.ID, item.Title, item.Kind, item.Reason, item.Text))
			if selected == 3 {
				break
			}
		}
		if !matched {
			note := "Local evidence not found; keep uncertain: " + query
			if !contains(composition.OpenQuestions, note) {
				composition.OpenQuestions = append(composition.OpenQuestions, note)
			}
		}
	}
	// The chapter being written is the subject rather than evidence, so a citation of it that is not a
	// context item is dropped; any other unknown ID is still refused before prose is generated.
	self, cited := "C:"+job.ChapterID, []string{}
	for _, id := range composition.EvidenceIDs {
		if seen[id] {
			cited = append(cited, id)
		} else if id != self {
			return original, fmt.Errorf("Composition evidence %s is unavailable. Use an exact context item ID", id)
		}
	}
	composition.EvidenceIDs = cited
	if len(blocks) > 0 {
		prompt = writingStagePrompt(prompt, "<composition_evidence>\n"+strings.Join(blocks, "\n\n")+"\n</composition_evidence>")
	}
	return prompt, nil
}

// writingEvidenceMatches excludes unrelated high-priority memories from a focused dependency search.
func writingEvidenceMatches(item ContextItem, query string) bool {
	text := lowerText(item.Title + " " + item.Text)
	for term := range terms(query) {
		if charCount(term) >= 2 && strings.Contains(text, term) {
			return true
		}
	}
	runes := []rune(query)
	for i := 0; i+2 < len(runes); i++ {
		if strings.Contains(text, string(runes[i:i+3])) {
			return true
		}
	}
	return false
}

// writingDraftPrompt supplies selected craft choices while keeping editorial notes out of the novel.
// The composition decides what happens; voice, when present, closes the instruction so the
// author's own excerpts are the last thing the model reads before writing.
func writingDraftPrompt(original Prompt, run *WritingRun, voice string) Prompt {
	instruction := "[Writing prose stage] Write the requested prose in the author's language. Follow the selected composition for what happens and what changes; write the spoken lines and gestures in the characters' own voices, matching the established narrative voice. Use the source evidence. Keep viewpoint knowledge, reader revelations and future plans distinct. Honor practical ability limits. Expand meaningful actions and interactions, summarize routine time, and retain consequences. Return only the complete prose for this operation; append mode returns only the new passage.\n<selected_composition>\n" + jsonText(run.Composition) + "\n</selected_composition>"
	if run.Review != nil {
		instruction += "\nRevise the affected passages and retain what already works.\n<draft>\n" + run.Draft + "\n</draft>\n<review>\n" + jsonText(run.Review) + "\n</review>"
	}
	if run.Continuation != "" {
		instruction += "\nContinue this interrupted candidate, returning only the remaining prose without repeating the prefix.\n<interrupted_candidate>\n" + run.Continuation + "\n</interrupted_candidate>"
	}
	return writingStagePrompt(original, instruction+voice)
}

// writingDraftPromptWithVoice adds the author's voice samples when they fit the context budget,
// and otherwise falls back to the plain prose prompt instead of failing the stage.
func writingDraftPromptWithVoice(project *Project, chapter *Chapter, original Prompt, run *WritingRun, budget int) Prompt {
	voice := writingVoiceBlock(project, chapter)
	prompt := writingDraftPrompt(original, run, voice)
	if voice != "" && prompt.Chars > budget {
		return writingDraftPrompt(original, run, "")
	}
	return prompt
}

// writingRevisionScope chooses the earliest affected layer while leaving global changes for author review.
func writingRevisionScope(review *WritingReview) string {
	scope := "prose"
	for _, issue := range review.Issues {
		if issue.Scope == "plan" {
			return "plan"
		}
		if issue.Scope == "composition" || issue.Scope == "context" {
			scope = "composition"
		}
	}
	return scope
}
