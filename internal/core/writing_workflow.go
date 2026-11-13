package core

import (
	"errors"
	"fmt"
	"strings"
)

// WritingIssue is a source-located editing opinion rather than an automatic quality score.
type WritingIssue struct {
	Kind        string   `json:"kind"`
	Scope       string   `json:"scope,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	Quote       string   `json:"quote"`
	Explanation string   `json:"explanation"`
	Suggestion  string   `json:"suggestion"`
}

// WritingReview keeps reading feedback and source-grounded memory candidates together.
type WritingReview struct {
	Summary       string         `json:"summary"`
	Issues        []WritingIssue `json:"issues"`
	Memories      []StoryMemory  `json:"memories,omitempty"`
	NeedsRevision bool           `json:"needs_revision"`
	Structured    bool           `json:"structured"`
	Warning       string         `json:"warning,omitempty"`
}

const reviewResultToolName = "quillweave_submit_writing_review"

// reviewResultTool exposes the review fields to native models while preserving the text-protocol fallback.
func reviewResultTool() *resultToolSpec {
	issue := recordToolSchema(WritingIssue{}, "kind", "scope", "quote", "explanation", "suggestion")
	issueProperties := issue["properties"].(map[string]any)
	issueProperties["kind"] = map[string]any{"type": "string", "enum": []string{"continuity", "reading"}}
	issueProperties["scope"] = map[string]any{"type": "string", "enum": []string{"prose", "composition", "context", "plan"}}
	memory := recordToolSchema(StoryMemory{}, "kind", "title", "content", "quote", "known_by")
	memoryProperties := memory["properties"].(map[string]any)
	memoryProperties["kind"] = map[string]any{"type": "string", "enum": writingMemoryKinds()}
	return &resultToolSpec{Name: reviewResultToolName, Description: "Submit located editing feedback and source-linked chapter state changes", Schema: map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"summary": map[string]any{"type": "string"}, "issues": map[string]any{"type": "array", "items": issue},
			"needs_revision": map[string]any{"type": "boolean"}, "memories": map[string]any{"type": "array", "maxItems": 20, "items": memory},
		},
		"required": []string{"summary", "issues", "needs_revision", "memories"},
	}}
}

// WritingStage preserves the actual prompt used by each generation or review request.
type WritingStage struct {
	Name   string `json:"name"`
	Prompt Prompt `json:"prompt"`
}

// WritingRun checkpoints one candidate independently from the saved chapter and conversation prose.
type WritingRun struct {
	AutoWrite       bool                `json:"auto_write,omitempty"`
	TargetChapterID string              `json:"target_chapter_id,omitempty"`
	Saved           bool                `json:"saved,omitempty"`
	Phase           string              `json:"phase"`
	Mode            string              `json:"mode"`
	Brief           string              `json:"brief"`
	SourcePrompt    *Prompt             `json:"source_prompt,omitempty"`
	Draft           string              `json:"draft"`
	Composition     *WritingComposition `json:"composition,omitempty"`
	Continuation    string              `json:"continuation,omitempty"`
	CandidateBody   string              `json:"candidate_body,omitempty"`
	Revisions       int                 `json:"revisions,omitempty"`
	Calls           int                 `json:"calls,omitempty"`
	Metrics         []ModelCallMetric   `json:"metrics,omitempty"`
	CandidateHash   string              `json:"candidate_hash"`
	Review          *WritingReview      `json:"review,omitempty"`
	ReviewStatus    string              `json:"review_status,omitempty"`
	Stages          []WritingStage      `json:"stages"`
}

// copyWritingRun isolates mutable stage slices in task snapshots.
func copyWritingRun(run *WritingRun) *WritingRun {
	if run == nil {
		return nil
	}
	copy := *run
	copy.Stages = append([]WritingStage{}, run.Stages...)
	copy.Metrics = append([]ModelCallMetric{}, run.Metrics...)
	copy.Composition = copyWritingComposition(run.Composition)
	if run.SourcePrompt != nil {
		prompt := *run.SourcePrompt
		prompt.Messages = append([]Message{}, prompt.Messages...)
		prompt.Context = append([]ContextItem{}, prompt.Context...)
		copy.SourcePrompt = &prompt
	}
	if run.Review != nil {
		review := *run.Review
		review.Issues = append([]WritingIssue{}, run.Review.Issues...)
		for i := range review.Issues {
			review.Issues[i].EvidenceIDs = append([]string{}, review.Issues[i].EvidenceIDs...)
		}
		review.Memories = append([]StoryMemory{}, run.Review.Memories...)
		for i := range review.Memories {
			review.Memories[i].KnownBy = append([]string{}, review.Memories[i].KnownBy...)
		}
		copy.Review = &review
	}
	return &copy
}

// writingCandidateBody uses the same append boundary as the reviewed chapter operation.
func writingCandidateBody(chapter *Chapter, draft, mode string) string {
	if mode == "append" && chapter.Body != "" {
		return chapter.Body + "\n\n" + draft
	}
	return draft
}

// writingBrief reuses confirmed local planning and makes material gaps visible before drafting.
func writingBrief(project *Project, job Job) string {
	chapter := findChapter(project, job.ChapterID)
	parts := []string{localText(project, "Author goal", "本轮目标") + "\n" + job.Instruction}
	for _, plan := range planningPath(project, chapter, job.WorkspaceContext) {
		parts = append(parts, plan.Title+"\n"+planSynopsis(project, plan))
	}
	if notes := chapterPlanningNotes(project, chapter); notes != "" {
		parts = append(parts, localText(project, "Author chapter notes", "作者章节备注")+"\n"+notes)
	}
	parts = append(parts, localText(project,
		"Use the available goals and source evidence to choose feasible actions, independent character reactions, and the resulting change. Identify consequential missing rules in the review. Match quiet, farewell and transition scenes to their purpose.",
		"结合已有目标与原文，落实可执行的行动、人物各自的反应和局面变化。影响因果的规则缺口须在审阅中指出。日常、告别与过渡场景按其目的安排。"))
	return strings.Join(parts, "\n\n")
}

// updateWritingRun publishes an immutable checkpoint while retaining full candidate prose separately.
func (engine *Engine) updateWritingRun(jobID string, run *WritingRun) error {
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	if job := engine.jobs[jobID]; job != nil {
		job.Writing = copyWritingRun(run)
		job.Progress = "Writing stage · " + run.Phase
		if err := engine.store.SaveRun(job.ID, job.ProjectID, storedJob(job)); err != nil {
			job.Error = "Unable to save the writing checkpoint: " + err.Error()
			return err
		}
		return nil
	}
	return errors.New("Writing task not found")
}

// errWritingSourcesChanged marks evidence that moved under a running candidate; such a
// candidate must not be staged for the author because it was computed against stale sources.
var errWritingSourcesChanged = errors.New("writing sources changed")

// writingSourceError keeps the author-facing message while tagging the sentinel.
func writingSourceError(text string) error {
	return errors.Join(errWritingSourcesChanged, errors.New(text))
}

// validateWritingSources refuses to continue a candidate after its saved evidence changed.
func (engine *Engine) validateWritingSources(projectID string, prompt Prompt) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	project, err := engine.store.Get(projectID)
	if err != nil {
		return err
	}
	return validateWritingSnapshot(&project, prompt)
}

// validateWritingSnapshot checks the same frozen sources inside an existing story write transaction.
func validateWritingSnapshot(project *Project, prompt Prompt) error {
	if prompt.SourceID != "" {
		text, _, exists := project.SourceText(prompt.SourceID)
		if !exists || hashText(text) != prompt.SourceHash {
			return writingSourceError("The chapter changed during writing. Review the current version before continuing")
		}
	}
	for _, item := range prompt.Context {
		if item.Kind == "project_revision" && item.Text != fmt.Sprint(project.Revision) {
			return writingSourceError("The story changed during writing. Rebuild this chapter's context before continuing")
		}
		if item.SourceID != "" && item.SourceHash != "" {
			text, _, exists := project.SourceText(item.SourceID)
			if !exists || hashText(text) != item.SourceHash {
				return writingSourceError(fmt.Sprintf("Writing evidence changed: %s. Rebuild this chapter's context", item.Title))
			}
		}
		if strings.HasPrefix(item.ID, "P:") {
			found := false
			for _, plan := range project.Studio.Plans {
				if item.ID == "P:"+plan.ID && strings.HasSuffix(item.Text, jsonText(plan)) {
					found = true
				}
			}
			if !found {
				return writingSourceError(fmt.Sprintf("Writing plan changed: %s. Rebuild this chapter's context", item.Title))
			}
		}
	}
	return nil
}

// frozenWritingSkills transfers only already-loaded instruction blocks to later writing stages.
func frozenWritingSkills(prompt Prompt) string {
	result := selectedSkillContext(prompt)
	if len(prompt.Messages) == 0 {
		return result
	}
	remaining := prompt.Messages[0].Content
	for {
		_, tail, exists := strings.Cut(remaining, "<loaded_skill ")
		if !exists {
			break
		}
		block, rest, closed := strings.Cut(tail, "</loaded_skill>")
		if !closed {
			break
		}
		result += "\n<loaded_skill " + block + "</loaded_skill>\n"
		remaining = rest
	}
	return result
}

// validateHandoffEvidence rejects source selections that cannot be recovered from successful parent steps.
func validateHandoffEvidence(job Job, ids []string) error {
	for _, id := range ids {
		found := false
		for _, step := range job.Steps {
			if step.ID == id && step.Status == "done" {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("Evidence step %s is unavailable. Select an exact successful step ID", id)
		}
	}
	return nil
}

// writingReviewPrompt reviews an unsaved candidate against the same frozen source bundle.
func writingReviewPrompt(project *Project, job Job, original Prompt, run *WritingRun, config ProviderConfig) (Prompt, error) {
	candidate := *project
	candidate.Chapters = append([]Chapter{}, project.Chapters...)
	chapter := findChapter(&candidate, job.ChapterID)
	chapter.Body = writingCandidateBody(findChapter(project, job.ChapterID), run.Draft, run.Mode)
	items := append([]ContextItem{}, original.Context...)
	for i := range items {
		items[i].Required = true
	}
	request := AIRequest{ProjectID: project.ID, ChapterID: job.ChapterID, Action: "review", Language: job.Language, Instruction: job.Instruction, WorkspaceContext: job.WorkspaceContext, ModelSelection: job.ModelSelection, Retrieved: items}
	request.systemAppend = frozenWritingSkills(original) + `
[Writing review stage] The current chapter is an UNSAVED candidate. First describe concrete reading effects, then check them against the frozen evidence and selected composition. Return JSON {"summary":"editing summary","issues":[{"kind":"continuity|reading","scope":"prose|composition|context|plan","evidence_ids":[],"quote":"verbatim candidate passage affected by the issue","explanation":"reader impact, source evidence and uncertainty","suggestion":"targeted edit at the indicated level"}],"needs_revision":false,"memories":[{"kind":"character|relationship|knowledge|clue|promise|fact|inference|summary","entity":"existing story entity or empty","title":"one identifiable change","content":"source-supported state change and its cause","state":"active|resolved|fulfilled","story_time":"when it happened","quote":"verbatim candidate passage of at least four characters","known_by":[]}]}. Scope prose means local expression; composition means an unmotivated action or missing scene; context means missing or misused evidence; plan means changing an author-approved direction or saved earlier chapter. For a missing bridge quote its affected neighboring passage. Preserve effective passages. Respect quiet scenes and individual character voices. Empty issues and memories arrays are valid. Treat literary judgments as editing opinions. Request revision only for a concrete issue in newly generated prose. Future plans, trials and craft references remain separate from this story's events. Only return the JSON review.`
	request.Instruction += "\n<chapter_brief>\n" + run.Brief + "\n</chapter_brief>"
	if run.Composition != nil {
		request.Instruction += "\n<selected_composition>\n" + jsonText(run.Composition) + "\n</selected_composition>"
	}
	prompt, err := prepareAI(&candidate, request, int(config.ContextChars))
	prompt.Skills = original.Skills
	return prompt, err
}

// parseWritingReview locates feedback and memory quotes in the candidate before offering them for review.
func parseWritingReview(project *Project, body, text string) WritingReview {
	var review WritingReview
	if decodeToolRecord(text, &review) != nil || strings.TrimSpace(review.Summary) == "" {
		return WritingReview{Summary: text, Warning: localText(project, "The review returned unstructured feedback. Check the candidate before accepting.", "审阅返回了非结构化意见，请结合正文检查后采纳。")}
	}
	review.Structured = true
	issues := []WritingIssue{}
	for _, issue := range review.Issues {
		if contains([]string{"continuity", "reading"}, issue.Kind) && strings.TrimSpace(issue.Explanation) != "" && strings.TrimSpace(issue.Quote) != "" && strings.Contains(body, issue.Quote) {
			if !contains([]string{"prose", "composition", "context", "plan"}, issue.Scope) {
				issue.Scope = "prose"
			}
			issues = append(issues, issue)
		}
	}
	if len(issues) != len(review.Issues) {
		review.Warning = localText(project, "Some feedback could not be located in the candidate and was omitted.", "部分审阅意见无法定位到候选原文，已单独排除。")
	}
	review.Issues = issues
	review.NeedsRevision = review.NeedsRevision && len(issues) > 0
	return review
}

// writingReviewOutput validates native fields before accepting the same source-located review used by text adapters.
func writingReviewOutput(project *Project, body string, output modelOutput) WritingReview {
	text := output.Text
	if submitted, arguments := resultToolCall(output, reviewResultToolName); submitted {
		if err := validateResultToolCall(output, reviewResultTool()); err != nil {
			return WritingReview{Summary: localText(project, "The submitted review failed field validation.", "审阅提交的字段未通过校验。"), Warning: err.Error()}
		}
		text = arguments
	}
	return parseWritingReview(project, body, text)
}

// writingReviewStatus keeps completed reviews distinct from preserved candidates needing author attention.
func writingReviewStatus(review *WritingReview) string {
	if review == nil {
		return "failed"
	}
	if !review.Structured {
		return "unstructured"
	}
	if review.Warning != "" {
		return "failed"
	}
	if review.NeedsRevision {
		if writingRevisionScope(review) == "plan" {
			return "needs_plan"
		}
		return "needs_revision"
	}
	return "complete"
}

// writingResultSummary describes the actual review outcome without treating retained prose as a finished review.
func writingResultSummary(project *Project, run *WritingRun) string {
	if run != nil && run.Saved {
		return localText(project, "The reviewed chapter and its linked changes are saved. The change receipt supports undo.", "正文与关联修改已保存，可通过变更回执撤销。")
	}
	if run != nil && run.ReviewStatus == "complete" {
		return localText(project, "Writing and review complete. Preview the prose and memory candidates", "写作与审阅已完成，请预览正文和记忆候选。")
	}
	if run != nil && run.ReviewStatus == "needs_plan" {
		return localText(project, "Candidate preserved. Align the chapter plan before adoption; authorized local changes can continue under the current save policy.", "候选已保留，采纳前需对齐章纲；获准调整的局部可按当前保存策略继续处理。")
	}
	if run != nil && run.ReviewStatus == "needs_revision" {
		return localText(project, "Candidate preserved. Editing feedback remains unresolved; review before acceptance.", "候选已保留，仍有未处理的编辑意见，请审阅后采纳。")
	}
	return localText(project, "Candidate preserved. Review is incomplete; check it before acceptance.", "候选已保留，审稿尚未完整完成，请检查后采纳。")
}

// writingOutcome returns a synchronized copy of the latest writing checkpoint for task receipts.
func (engine *Engine) writingOutcome(jobID string) *WritingRun {
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	if job := engine.jobs[jobID]; job != nil {
		return copyWritingRun(job.Writing)
	}
	return nil
}

// stageWritingCandidate binds reviewed memories to exact text and preserves incomplete reviews as pending candidates.
func (engine *Engine) stageWritingCandidate(job Job, project *Project, run *WritingRun) error {
	chapter := findChapter(project, job.ChapterID)
	body := writingCandidateBody(chapter, run.Draft, run.Mode)
	run.CandidateBody = body
	run.CandidateHash, run.Phase = hashText(body), "ready"
	if run.ReviewStatus != "failed" {
		run.ReviewStatus = writingReviewStatus(run.Review)
	}
	if run.ReviewStatus != "complete" {
		run.Phase = "needs_review"
	}
	if err := engine.updateWritingRun(job.ID, run); err != nil {
		return err
	}
	proposals := writingCandidateProposals(job, project, run)
	if staged, propErr := engine.recordProposals(job.ID, jsonText(map[string]any{"proposals": proposals}), project); propErr != nil {
		return errors.Join(errors.New("Unable to stage the writing candidate. The draft is retained in the task"), propErr)
	} else if staged == 0 {
		return errors.New("Unable to stage the writing candidate. The draft is retained in the task")
	}
	if job.CommitPolicy == "auto" && run.ReviewStatus == "complete" {
		return engine.saveAutomaticChapter(job, project, run)
	}
	return nil
}

// writingCandidateProposals builds shared prose, memory and composition candidates without persisting a job.
func writingCandidateProposals(job Job, project *Project, run *WritingRun) []StudioProposal {
	chapter := findChapter(project, job.ChapterID)
	body := writingCandidateBody(chapter, run.Draft, run.Mode)
	proposals := []StudioProposal{{Kind: "chapter", Mode: run.Mode, Title: localText(project, "Chapter prose proposal", "章节正文建议"), Content: run.Draft}}
	if run.ReviewStatus == "complete" {
		for _, memory := range writingMemoryCandidates(project, chapter, body, run.Review.Memories) {
			memory.SessionID = job.SessionID
			proposals = append(proposals, StudioProposal{Kind: "memory", Title: memory.Title, Content: memory.Content, Memory: &memory})
		}
		if completeWritingComposition(run.Composition) {
			for _, plan := range planningPath(project, chapter, job.WorkspaceContext) {
				if plan.Level != "chapter" || plan.Status != "confirmed" || plan.StartChapter != chapter.Number {
					continue
				}
				if jsonText(plan.SceneCards) != jsonText(run.Composition.SceneCards) {
					plan.SceneCards = copyWritingComposition(run.Composition).SceneCards
					proposals = append(proposals, StudioProposal{Kind: "plan", Title: plan.Title, Content: localText(project, "Save the scene composition used for this chapter.", "保存本章实际采用的场景构思。"), Plan: &plan})
				}
				break
			}
		}
	}
	return proposals
}

// writingStageModel prefers an explicit composition/review model route and falls back to
// the writer's own model when the author has not configured one.
func (engine *Engine) writingStageModel(project *Project, task string, fallback SelectedModel) SelectedModel {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	id := project.ModelOverrides[task]
	if id == "" {
		id = engine.models.Routes[task]
	}
	if id == "" {
		return fallback
	}
	selected, err := engine.models.Select(project, task)
	if err != nil {
		return fallback
	}
	return selected
}

// salvageWritingCandidate keeps already-generated prose reviewable when a later stage fails:
// any existing draft is staged as a candidate with the failure recorded, instead of failing the whole task.
// Cancellation keeps the resume path, and stale evidence never stages a candidate.
func (engine *Engine) salvageWritingCandidate(job Job, project *Project, run *WritingRun, cause error) error {
	if run == nil || strings.TrimSpace(run.Draft) == "" || job.ctx.Err() != nil || errors.Is(cause, errWritingSourcesChanged) {
		return cause
	}
	note := localText(project, "A later stage failed; this candidate was preserved: ", "后续阶段失败，已保留这份候选：") + cause.Error()
	if run.Review == nil {
		run.Review = &WritingReview{Summary: localText(project, "Review unavailable; the candidate was preserved after a stage failure.", "审阅不可用，阶段失败后已保留候选。")}
	}
	if run.Review.Warning != "" {
		run.Review.Warning += " "
	}
	run.Review.Warning += note
	run.ReviewStatus = "failed"
	if err := engine.stageWritingCandidate(job, project, run); err != nil {
		return errors.Join(cause, err)
	}
	return nil
}

// reviewAgentProse reviews chapter proposals emitted directly by the coordinator through the same frozen pipeline.
func (engine *Engine) reviewAgentProse(job Job, project *Project, selected SelectedModel, step agentStepEnvelope, available int) (agentStepEnvelope, int, bool, error) {
	proposals := append([]StudioProposal{}, step.Proposals...)
	if step.Proposal != nil {
		proposals = append([]StudioProposal{*step.Proposal}, proposals...)
	}
	remaining := []StudioProposal{}
	calls, reviewed := 0, false
	for _, proposal := range proposals {
		if proposal.Kind != "chapter" {
			remaining = append(remaining, proposal)
			continue
		}
		if calls >= available {
			return step, calls, reviewed, errors.New("The chapter candidate needs a review call before staging")
		}
		if !contains([]string{"", "append", "replace"}, proposal.Mode) || strings.TrimSpace(proposal.Content) == "" {
			return step, calls, reviewed, errors.New("Provide a non-empty chapter candidate with append or replace mode")
		}
		mode := "append"
		if proposal.Mode == "replace" {
			mode = "replace"
		}
		engine.jobsMu.Lock()
		active := engine.jobs[job.ID]
		if active == nil {
			engine.jobsMu.Unlock()
			return step, calls, reviewed, errors.New("Writing task not found")
		}
		current := active.snapshot()
		engine.jobsMu.Unlock()
		if err := validateHandoffEvidence(current, step.EvidenceIDs); err != nil {
			return step, calls, reviewed, err
		}
		request := scopedAgentRequest(project, current, agentStepEnvelope{Tool: "writer", Query: job.Instruction, Mode: mode, EvidenceIDs: step.EvidenceIDs})
		prompt, err := prepareAI(project, request, int(selected.Config.ContextChars))
		if err != nil {
			return step, calls, reviewed, err
		}
		prompt.Skills = job.Prompt.Skills
		current.Writing = &WritingRun{Phase: "reviewing", Mode: mode, Brief: writingBrief(project, job), Draft: proposal.Content, Stages: []WritingStage{}}
		_, used, err := engine.runWritingTask(current, project, selected, prompt, mode, 1)
		calls += used
		if err != nil {
			return step, calls, reviewed, err
		}
		reviewed = true
	}
	step.Proposal, step.Proposals = nil, remaining
	if step.ChangeSet != nil || len(step.Operations) > 0 {
		candidate, used, err := engine.reviewChapterChangeSet(job, project, selected, step, available-calls)
		calls += used
		if err != nil {
			return step, calls, reviewed, err
		}
		if candidate != nil {
			step.ChangeSet, step.Operations, reviewed = candidate, nil, true
		}
	}
	return step, calls, reviewed, nil
}

// reviewChapterChangeSet inspects batch prose, including new chapters, before the batch becomes a pending change.
func (engine *Engine) reviewChapterChangeSet(job Job, project *Project, selected SelectedModel, step agentStepEnvelope, available int) (*ChangeSet, int, error) {
	cs := ChangeSet{Operations: append([]ChangeOperation{}, step.Operations...)}
	if step.ChangeSet != nil {
		cs = *step.ChangeSet
		cs.Operations = append([]ChangeOperation{}, cs.Operations...)
	}
	prose := false
	for _, op := range cs.Operations {
		var patch chapterProsePatch
		if op.TargetKind == TargetChapter && op.OpType != OpDelete && decodeJSON(op.AfterValue, &patch) == nil && strings.TrimSpace(patch.Body) != "" {
			prose = true
		}
	}
	if !prose {
		return nil, 0, nil
	}
	cs.Status, cs.Source = ChangeSetStaged, "agent"
	projected := cloneProject(*project)
	if err := applyChangeSetToProject(&projected, &cs); err != nil {
		return nil, 0, err
	}
	bodies, generated := []string{}, []string{}
	for _, op := range cs.Operations {
		if op.TargetKind == TargetChapter {
			var patch chapterProsePatch
			if op.OpType != OpDelete && decodeJSON(op.AfterValue, &patch) == nil {
				generated = append(generated, patch.Body)
			}
			if chapter := findChapter(&projected, op.TargetID); chapter != nil && strings.TrimSpace(chapter.Body) != "" {
				bodies = append(bodies, chapter.Title+"\n"+chapter.Body)
			}
		}
	}
	trace := engine.addStep(job.ID, "chapter_candidate", "running", "Reviewing proposed chapter changes")
	engine.finishStep(job.ID, trace, "done", jsonText(cs.Operations))
	if available < 1 {
		return nil, 0, errors.New("The chapter changes need a review call before staging. Recover the saved chapter_candidate step to continue")
	}
	engine.jobsMu.Lock()
	active := engine.jobs[job.ID]
	if active == nil {
		engine.jobsMu.Unlock()
		return nil, 0, errors.New("Writing task not found")
	}
	current := active.snapshot()
	engine.jobsMu.Unlock()
	if err := validateHandoffEvidence(current, step.EvidenceIDs); err != nil {
		return nil, 0, err
	}
	request := scopedAgentRequest(project, current, agentStepEnvelope{Tool: "researcher", Query: job.Instruction, EvidenceIDs: step.EvidenceIDs})
	request.systemAppend += `
[Writing review stage] Review the UNSAVED prose in the proposed chapter changes against the saved story and author goal. Other setting changes in this batch remain proposals. Return JSON {"summary":"editing summary","issues":[{"kind":"continuity|reading","scope":"prose|composition|context|plan","evidence_ids":[],"quote":"verbatim candidate passage","explanation":"issue, reader impact and uncertainty","suggestion":"targeted edit at the affected level"}],"needs_revision":false,"memories":[]}. Check knowledge, feasible ability use, character goals, emotional progression, repeated payoffs and viewpoint information. Keep the batch intact for the author's final review. Only return the JSON review.`
	request.Retrieved = append(request.Retrieved,
		ContextItem{ID: "project_revision", Title: "Saved project revision", Kind: "project_revision", Text: fmt.Sprint(project.Revision), Required: true},
		ContextItem{ID: "chapter_changes", Title: "Unsaved chapter changes", Kind: "chapter_candidate", Text: jsonText(cs.Operations), Required: true},
	)
	reviewSelected := engine.writingStageModel(project, "writing_review", selected)
	config := engine.writingResultConfig(reviewSelected, reviewResultTool())
	prompt, err := prepareAI(project, request, int(config.ContextChars))
	if err != nil {
		return nil, 0, err
	}
	prompt.Skills = job.Prompt.Skills
	prepareTokenBudget(&prompt, &config, job.ModelSelection)
	if prompt.Budget != nil && !prompt.Budget.CanSend {
		return nil, 0, errors.New(prompt.Budget.Warning)
	}
	run := &WritingRun{Phase: "reviewing", Mode: "changeset", Brief: job.Instruction, Draft: strings.Join(bodies, "\n\n"), SourcePrompt: &prompt, Stages: []WritingStage{}}
	run.CandidateBody, run.CandidateHash = run.Draft, hashText(run.Draft)
	engine.updateWritingRun(job.ID, run)
	if err := engine.validateWritingSources(project.ID, prompt); err != nil {
		return nil, 0, err
	}
	output, used, err := engine.runWritingStage(job, run, reviewSelected, config, prompt, "writing_review", func(string) {}, available)
	if err != nil {
		return nil, used, err
	}
	if err := engine.validateWritingSources(project.ID, prompt); err != nil {
		return nil, used, err
	}
	review := writingReviewOutput(project, run.Draft, output)
	appendWritingLanguageIssues(project, strings.Join(generated, "\n\n"), &review)
	run.Review, run.Phase = &review, "ready"
	run.ReviewStatus = writingReviewStatus(&review)
	if run.ReviewStatus != "complete" {
		run.Phase = "needs_review"
	}
	if err := engine.updateWritingRun(job.ID, run); err != nil {
		return nil, used, err
	}
	cs.Status = ChangeSetStaged
	return &cs, used, nil
}

// runWritingTask composes, retrieves, drafts and reviews with one resumable, bounded revision cycle.
func (engine *Engine) runWritingTask(job Job, project *Project, selected SelectedModel, initial Prompt, mode string, maxCalls int) (string, int, error) {
	chapter := findChapter(project, job.ChapterID)
	if chapter == nil {
		return "", 0, errors.New("Select a chapter first")
	}
	for _, plan := range project.Studio.Plans {
		if plan.Level == "chapter" && plan.Status == "confirmed" && plan.StartChapter == chapter.Number {
			if _, err := confirmedWritingPlans(project, plan.ID); err != nil {
				return "", 0, err
			}
			break
		}
	}
	run := &WritingRun{Mode: mode, Phase: "brief", Brief: writingBrief(project, job), SourcePrompt: &initial, Stages: []WritingStage{}}
	if job.Writing != nil {
		run = copyWritingRun(job.Writing)
		if run.SourcePrompt == nil {
			run.SourcePrompt = &initial
		}
	}
	if run.Brief == "" {
		run.Brief = writingBrief(project, job)
	}
	if run.Mode == "" {
		run.Mode = mode
	}
	if run.Saved {
		return run.Draft, 0, nil
	}
	run.AutoWrite, run.TargetChapterID = job.CommitPolicy == "auto", chapter.ID
	if contains([]string{"retrieving", "drafting", "revising"}, run.Phase) && !completeWritingComposition(run.Composition) {
		run.Phase = "composing"
	}
	if run.Phase == "needs_review" {
		run.Review, run.ReviewStatus, run.Phase = nil, "", "reviewing"
	}
	if run.Phase == "brief" || run.Phase == "" {
		run.Phase = "reviewing"
		if run.Draft == "" {
			run.Composition = nil
			if job.IntentHint != "writing_recompose" {
				run.Composition = existingWritingComposition(project, job, run.Mode)
			}
			if job.Action != "agent" {
				run.Continuation = job.Text
			}
			run.Phase = "composing"
			if completeWritingComposition(run.Composition) {
				run.Phase = "retrieving"
			}
		}
	}
	transcriptBase := ""
	if job.Action == "agent" {
		engine.jobsMu.Lock()
		if current := engine.jobs[job.ID]; current != nil {
			transcriptBase = current.Text
		}
		engine.jobsMu.Unlock()
	}
	if err := engine.updateWritingRun(job.ID, run); err != nil {
		return run.Draft, 0, err
	}
	config := selected.Config
	config.nativeTools = false
	calls := 0
	for {
		if err := job.ctx.Err(); err != nil {
			return run.Draft, calls, err
		}
		if err := engine.validateWritingSources(project.ID, *run.SourcePrompt); err != nil {
			return run.Draft, calls, err
		}
		switch run.Phase {
		case "composing":
			if calls+3 > maxCalls {
				return run.Draft, calls, engine.salvageWritingCandidate(job, project, run, errors.New("Writing needs a composition, draft and review call. Resume with sufficient remaining budget"))
			}
			stageSelected := engine.writingStageModel(project, "writing_composition", selected)
			stageConfig := engine.writingResultConfig(stageSelected, compositionResultTool())
			prompt, err := compositionPrompt(project, job, *run.SourcePrompt, run, stageConfig)
			if err != nil {
				return run.Draft, calls, engine.salvageWritingCandidate(job, project, run, err)
			}
			var composition *WritingComposition
			prose := ""
			for attempt := 0; attempt < 3; attempt++ {
				if calls+3 > maxCalls {
					break
				}
				stageConfig = engine.writingResultConfig(stageSelected, compositionResultTool())
				output, used, err := engine.runWritingStage(job, run, stageSelected, stageConfig, prompt, "writing_composition", func(string) {}, maxCalls-calls-2)
				calls += used
				if err != nil && prose != "" && job.ctx.Err() == nil {
					break // The corrective retry failed: keep the earlier prose answer instead of losing it.
				}
				if err != nil {
					return run.Draft, calls, engine.salvageWritingCandidate(job, project, run, err)
				}
				if submitted, arguments := resultToolCall(output, compositionResultToolName); submitted {
					err = validateResultToolCall(output, compositionResultTool())
					if err == nil {
						composition, err = parseWritingComposition(arguments)
					}
					if err == nil {
						break
					}
					if attempt < 2 {
						call := firstToolCall(output, compositionResultToolName)
						prompt.Messages = append(prompt.Messages, assistantModelMessage(output), nativeResultMessage(call, "The submitted composition was rejected: "+err.Error()+" Call "+compositionResultToolName+" again with complete scene_cards and participant goals."))
						continue
					}
				} else if composition, err = parseWritingComposition(output.Text); err == nil {
					break
				}
				// A prose answer is asked once more for scene cards, so the prose stage and its voice
				// samples still write the draft. Prose given again is kept as the draft, not discarded.
				if compositionProseFallback(output.Text) {
					retry := prose == "" && attempt < 2 && calls+3 <= maxCalls
					prose = visibleAgentOutput(output.Text)
					if !retry {
						break
					}
					engine.addStep(job.ID, "writing_composition", "done", localText(project, "Composition stage returned prose; asked once more for scene cards", "构思阶段返回了正文，已要求重新给出场景卡"))
					prompt.Messages = append(prompt.Messages, Message{Role: "assistant", Content: output.Text}, Message{Role: "user", Content: "The previous answer was chapter prose, which this stage cannot use. Return only the composition JSON with intent, approach and actionable scene_cards; the scene cards may follow the events of that answer, and the prose is written in a later stage."})
					continue
				}
				if attempt < 2 {
					prompt.Messages = append(prompt.Messages, Message{Role: "assistant", Content: output.Text}, Message{Role: "user", Content: "The previous answer was rejected: " + err.Error() + ". Return the complete composition JSON with intent, approach and actionable scene_cards."})
				}
			}
			if composition == nil && prose != "" {
				composition = engine.stageCompositionProse(job, project, run, prose)
			}
			if composition == nil {
				cause := errors.New("The composition did not return complete scene cards within the available call budget")
				return run.Draft, calls, engine.salvageWritingCandidate(job, project, run, cause)
			}
			if run.Phase != "reviewing" {
				run.Composition, run.Phase = composition, "retrieving"
			}
		case "retrieving":
			if run.Composition == nil {
				return run.Draft, calls, errors.New("The selected composition is missing")
			}
			prompt, err := engine.gatherWritingEvidence(project, job, *run.SourcePrompt, run.Composition)
			if err != nil {
				// A revision round keeps its reviewed draft; a first round has none and still stops.
				return run.Draft, calls, engine.salvageWritingCandidate(job, project, run, err)
			}
			run.SourcePrompt, run.Phase = &prompt, "drafting"
			if run.Revisions > 0 {
				run.Phase = "revising"
			}
		case "drafting", "revising":
			if calls+2 > maxCalls {
				return run.Draft, calls, engine.salvageWritingCandidate(job, project, run, errors.New("The writing task needs a drafting call and a review call"))
			}
			prompt := writingDraftPromptWithVoice(project, chapter, *run.SourcePrompt, run, int(config.ContextChars))
			stream := engine.newAgentTextStream(job.ID)
			stream.base = transcriptBase
			stream.writing, stream.prefix = true, run.Continuation
			if job.Action != "agent" {
				stream.base, stream.continuous = run.Continuation, true
			} else if run.Continuation != "" {
				if stream.base != "" {
					stream.base += "\n\n"
				}
				stream.base += run.Continuation
				stream.continuous = true
			}
			prefix, previousDraft := run.Continuation, run.Draft
			output, used, err := engine.runWritingStage(job, run, selected, config, prompt, "writing_draft", stream.append, maxCalls-calls-1)
			calls += used
			if err != nil {
				run.Continuation = prefix + visibleAgentOutput(stream.raw.String())
				_ = engine.updateWritingRun(job.ID, run)
				// An interrupted revision must not discard the already reviewed draft.
				return run.Draft, calls, engine.salvageWritingCandidate(job, project, run, err)
			}
			stream.finish(output.Text)
			run.Draft = prefix + visibleAgentOutput(output.Text)
			run.Continuation, run.CandidateBody, run.CandidateHash = "", "", ""
			run.Review, run.ReviewStatus, run.Phase = nil, "", "reviewing"
			if strings.TrimSpace(run.Draft) == "" {
				if strings.TrimSpace(previousDraft) != "" {
					run.Draft = previousDraft
					return run.Draft, calls, engine.salvageWritingCandidate(job, project, run, errors.New("The writer returned an empty candidate"))
				}
				return "", calls, errors.New("The writer returned an empty candidate")
			}
		case "reviewing":
			if strings.TrimSpace(run.Draft) == "" {
				return "", calls, errors.New("The writer returned an empty candidate")
			}
			if calls >= maxCalls {
				return run.Draft, calls, engine.salvageWritingCandidate(job, project, run, errors.New("Model call budget exhausted before writing review"))
			}
			reviewSelected := engine.writingStageModel(project, "writing_review", selected)
			reviewConfig := engine.writingResultConfig(reviewSelected, reviewResultTool())
			prompt, err := writingReviewPrompt(project, job, *run.SourcePrompt, run, reviewConfig)
			if err != nil {
				return run.Draft, calls, engine.salvageWritingCandidate(job, project, run, err)
			}
			output, used, err := engine.runWritingStage(job, run, reviewSelected, reviewConfig, prompt, "writing_review", func(string) {}, maxCalls-calls)
			calls += used
			if err != nil {
				return run.Draft, calls, engine.salvageWritingCandidate(job, project, run, err)
			}
			review := writingReviewOutput(project, writingCandidateBody(chapter, run.Draft, run.Mode), output)
			appendWritingLanguageIssues(project, run.Draft, &review)
			appendVoiceDriftIssues(project, chapter, run.Draft, run.Revisions, &review)
			run.Review = &review
			scope := writingRevisionScope(&review)
			required := 2
			if scope == "composition" {
				required = 3
			}
			if !review.NeedsRevision || run.Revisions >= 1 || scope == "plan" || calls+required > maxCalls {
				return run.Draft, calls, engine.stageWritingCandidate(job, project, run)
			}
			run.Revisions++
			run.Phase = "revising"
			if scope == "composition" {
				run.Phase = "composing"
			}
		case "ready":
			return run.Draft, calls, engine.stageWritingCandidate(job, project, run)
		default:
			return run.Draft, calls, errors.New("Unknown saved writing stage")
		}
		if err := engine.updateWritingRun(job.ID, run); err != nil {
			return run.Draft, calls, err
		}
	}
}

// writingResultConfig enables schema-driven stage submission until that model explicitly rejects native tools.
func (engine *Engine) writingResultConfig(selected SelectedModel, tool *resultToolSpec) ProviderConfig {
	config := selected.Config
	engine.mu.Lock()
	unsupported := engine.nativeToolFallback[nativeCapabilityKey(selected)]
	engine.mu.Unlock()
	config.nativeTools = !unsupported
	config.resultTool = nil
	if config.nativeTools {
		config.resultTool = tool
	}
	return config
}

// runWritingStage records each real request and falls back once within the remaining call budget.
func (engine *Engine) runWritingStage(job Job, run *WritingRun, selected SelectedModel, config ProviderConfig, prompt Prompt, purpose string, appendText func(string), remaining ...int) (modelOutput, int, error) {
	if err := job.ctx.Err(); err != nil {
		return modelOutput{}, 0, err
	}
	limit := 2
	if len(remaining) > 0 {
		limit = min(limit, remaining[0])
	}
	if limit <= 0 {
		return modelOutput{}, 0, errors.New("Model call budget exhausted before writing stage")
	}
	prompt.ToolChars = nativeToolChars(config)
	prompt.Chars = messageChars(prompt.Messages) + prompt.ToolChars
	if prompt.Chars > int(config.ContextChars) {
		return modelOutput{}, 0, errors.New("Writing instructions and required evidence exceed the model context budget")
	}
	prepareTokenBudget(&prompt, &config, job.ModelSelection)
	if prompt.Budget != nil && !prompt.Budget.CanSend {
		return modelOutput{}, 0, errors.New(prompt.Budget.Warning)
	}
	metricStart := 0
	engine.jobsMu.Lock()
	if current := engine.jobs[job.ID]; current != nil {
		metricStart = len(current.Metrics)
	}
	engine.jobsMu.Unlock()
	var output modelOutput
	var err error
	used := 0
	for used < limit {
		run.Stages = append(run.Stages, WritingStage{Name: run.Phase, Prompt: prompt})
		run.Calls++
		if err := engine.updateWritingRun(job.ID, run); err != nil {
			return output, used, err
		}
		used++
		output, err = engine.streamJobModel(job.ID, purpose, job.ctx, config, prompt.Messages, appendText, selected.ProfileID)
		if !errors.Is(err, errNativeToolsUnsupported) || config.resultTool == nil {
			break
		}
		engine.mu.Lock()
		if engine.nativeToolFallback == nil {
			engine.nativeToolFallback = map[string]bool{}
		}
		engine.nativeToolFallback[nativeCapabilityKey(selected)] = true
		engine.mu.Unlock()
		config.nativeTools, config.resultTool = false, nil
		prompt.ToolChars, prompt.Chars = 0, messageChars(prompt.Messages)
	}
	engine.jobsMu.Lock()
	if current := engine.jobs[job.ID]; current != nil && len(current.Metrics) > metricStart {
		run.Metrics = append(run.Metrics, current.Metrics[metricStart:]...)
	}
	if current := engine.jobs[job.ID]; current != nil && current.Writing != nil && contains([]string{"drafting", "revising"}, run.Phase) {
		run.Continuation = current.Writing.Continuation
	}
	engine.jobsMu.Unlock()
	if checkpointErr := engine.updateWritingRun(job.ID, run); err == nil {
		err = checkpointErr
	}
	if err == nil {
		err = engine.validateWritingSources(job.ProjectID, *run.SourcePrompt)
	}
	return output, used, err
}
