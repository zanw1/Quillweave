package core

import (
	"context"
	"strings"
	"unicode"
)

// ExternalProvenance keeps external authorship and the exact reviewed inputs inspectable.
type ExternalProvenance struct {
	Client           string         `json:"client"`
	Transport        string         `json:"transport"`
	WritingSessionID string         `json:"writing_session_id,omitempty"`
	WikiSessionID    string         `json:"wiki_session_id,omitempty"`
	DraftHash        string         `json:"draft_hash,omitempty"`
	EvidenceVersion  string         `json:"evidence_version,omitempty"`
	Review           *WritingReview `json:"review,omitempty"`
	Revisions        int            `json:"revisions,omitempty"`
}

// WritingBeginInput identifies an existing chapter or a planned new chapter.
type WritingBeginInput struct {
	ProjectID     string `json:"project_id,omitempty"`
	RequestID     string `json:"request_id"`
	ChapterID     string `json:"chapter_id,omitempty"`
	PlanID        string `json:"plan_id,omitempty"`
	Title         string `json:"title,omitempty"`
	Number        uint32 `json:"number,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Instruction   string `json:"instruction"`
	Language      string `json:"language,omitempty"`
	RevisionLimit *int   `json:"revision_limit,omitempty"`
	ContextChars  int    `json:"context_chars,omitempty"`
}

// WritingMutation identifies a durable session revision and one retryable operation.
type WritingMutation struct {
	ProjectID        string `json:"project_id,omitempty"`
	RequestID        string `json:"request_id"`
	WritingSessionID string `json:"writing_session_id"`
	SessionRevision  uint64 `json:"session_revision"`
}

// WritingCompositionInput submits the same scene-card contract used by internal writing.
type WritingCompositionInput struct {
	WritingMutation
	Composition WritingComposition `json:"composition"`
}

// WritingDraftInput submits one new draft while preserving previous draft versions.
type WritingDraftInput struct {
	WritingMutation
	Draft string `json:"draft"`
}

// WritingReviewInput binds external editing opinions to one draft and frozen evidence bundle.
type WritingReviewInput struct {
	WritingMutation
	DraftHash       string        `json:"draft_hash"`
	EvidenceVersion string        `json:"evidence_version"`
	Review          WritingReview `json:"review"`
}

// WritingSessionRequest locates a session without changing it.
type WritingSessionRequest struct {
	ProjectID        string `json:"project_id,omitempty"`
	WritingSessionID string `json:"writing_session_id"`
}

// ExternalDraftVersion retains each submitted draft and its own external review.
type ExternalDraftVersion struct {
	Draft           string                `json:"draft"`
	Hash            string                `json:"hash"`
	EvidenceVersion string                `json:"evidence_version"`
	Review          *WritingReview        `json:"review,omitempty"`
	Reviews         []ExternalDraftReview `json:"reviews,omitempty"`
	Composition     *WritingComposition   `json:"composition,omitempty"`
	CreatedAt       string                `json:"created_at"`
}

// ExternalDraftReview preserves each review's evidence version when a retained draft is reviewed again.
type ExternalDraftReview struct {
	EvidenceVersion string        `json:"evidence_version"`
	Review          WritingReview `json:"review"`
	CreatedAt       string        `json:"created_at"`
}

// ExternalWritingSession persists agent waiting states separately from executing model jobs.
type ExternalWritingSession struct {
	ID              string            `json:"id"`
	ProjectID       string            `json:"project_id"`
	Revision        uint64            `json:"revision"`
	Phase           string            `json:"phase"`
	Input           WritingBeginInput `json:"input"`
	Target          Chapter           `json:"target"`
	NewChapter      bool              `json:"new_chapter"`
	BaseRevision    uint64            `json:"base_revision"`
	EvidenceVersion string            `json:"evidence_version"`
	// CompositionSource is "chapter_outline" while the scene cards are adopted from a confirmed chapter outline.
	CompositionSource string                 `json:"composition_source,omitempty"`
	Run               WritingRun             `json:"writing"`
	Drafts            []ExternalDraftVersion `json:"drafts"`
	CandidateID       string                 `json:"candidate_id,omitempty"`
	Client            string                 `json:"client"`
	Transport         string                 `json:"transport"`
	CreatedAt         string                 `json:"created_at"`
	UpdatedAt         string                 `json:"updated_at"`
	RefreshedChanges  []WritingSourceChange  `json:"refreshed_changes,omitempty"`
}

// externalWritingJob adapts pure writing helpers without creating a durable model job.
func externalWritingJob(ctx context.Context, session *ExternalWritingSession) Job {
	workspace := &WorkspaceContext{}
	workspace.ActivePlanID = session.Input.PlanID
	return Job{ProjectID: session.ProjectID, ChapterID: session.Target.ID, Action: "continue", Instruction: session.Input.Instruction, Language: session.Input.Language, CommitPolicy: "review", WorkspaceContext: workspace, ctx: ctx}
}

// externalSessionLanguage picks the language an external writing session works in.
// The desktop app sends its interface language with every request and external agents do not,
// so a work left to inherit follows the language of its own title, genre and premise.
func externalSessionLanguage(project *Project, requested string) string {
	for _, language := range []string{requested, project.Studio.Language} {
		if language == "zh-CN" || language == "en" {
			return language
		}
	}
	if textMostlyHan(project.Title + project.Genre + project.Premise) {
		return "zh-CN"
	}
	return "en"
}

// textMostlyHan reports whether Han characters make up most of the letters in text.
func textMostlyHan(text string) bool {
	han, letters := 0, 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
			if unicode.Is(unicode.Han, r) {
				han++
			}
		}
	}
	return han*2 > letters
}

// externalWritingProject adds a reserved new chapter only to an isolated context snapshot.
// cloneProject drops runtime-only fields, so the session language is restored on the copy;
// without it, labels, prompts and the Chinese banned-phrase check fall back to English.
func externalWritingProject(project Project, session *ExternalWritingSession) Project {
	copy := cloneProject(project)
	copy.taskLanguage = externalSessionLanguage(&project, session.Input.Language)
	if session.NewChapter {
		copy.Chapters = append(copy.Chapters, session.Target)
	}
	return copy
}

// writingSessionView returns current material and compact history counts instead of every old draft.
func writingSessionView(project *Project, session *ExternalWritingSession) map[string]any {
	result := map[string]any{
		"writing_session_id": session.ID, "project_id": session.ProjectID, "session_revision": session.Revision,
		"phase": session.Phase, "chapter_id": session.Target.ID, "new_chapter": session.NewChapter,
		"base_revision": session.BaseRevision, "evidence_version": session.EvidenceVersion,
		"brief": session.Run.Brief, "composition": session.Run.Composition, "draft": session.Run.Draft,
		"draft_hash": hashText(session.Run.Draft), "review": session.Run.Review, "review_status": session.Run.ReviewStatus,
		"revisions": session.Run.Revisions, "revision_limit": *session.Input.RevisionLimit,
		"draft_versions": len(session.Drafts), "candidate_id": session.CandidateID, "composition_source": session.CompositionSource,
	}
	result["refreshed_changes"] = session.RefreshedChanges[:min(50, len(session.RefreshedChanges))]
	result["refreshed_change_count"] = len(session.RefreshedChanges)
	if session.Run.SourcePrompt != nil {
		result["evidence"] = session.Run.SourcePrompt.Context
	}
	prompt, err := externalStagePrompt(project, session)
	switch session.Phase {
	case "awaiting_composition":
		result["output_schema"] = compositionResultTool().Schema
	case "awaiting_review":
		result["output_schema"] = reviewResultTool().Schema
	}
	if err != nil {
		result["material_error"] = err.Error()
	} else if len(prompt.Messages) > 0 {
		result["messages"] = prompt.Messages
	}
	return boundedWritingView(result, session)
}

// compactWritingView is the MCP reply: identities, hashes and the next output contract.
// The brief, stage packet and draft are not echoed back; agents read them through writing_material or writing_status.
func compactWritingView(session *ExternalWritingSession) map[string]any {
	result := map[string]any{
		"writing_session_id": session.ID, "project_id": session.ProjectID, "session_revision": session.Revision,
		"phase": session.Phase, "chapter_id": session.Target.ID, "new_chapter": session.NewChapter,
		"base_revision": session.BaseRevision, "evidence_version": session.EvidenceVersion,
		"draft_hash": hashText(session.Run.Draft), "draft_chars": charCount(session.Run.Draft), "draft_versions": len(session.Drafts),
		"review_status": session.Run.ReviewStatus, "revisions": session.Run.Revisions, "revision_limit": *session.Input.RevisionLimit,
		"candidate_id": session.CandidateID, "compact": true, "material_operation": "writing_material", "composition_source": session.CompositionSource,
		"refreshed_changes": session.RefreshedChanges[:min(50, len(session.RefreshedChanges))], "refreshed_change_count": len(session.RefreshedChanges),
	}
	switch session.Phase {
	case "awaiting_composition":
		result["output_schema"] = compositionResultTool().Schema
		result["evidence"] = writingEvidenceReferences(session, 50)
	case "awaiting_draft", "awaiting_revision":
		result["evidence"] = writingEvidenceReferences(session, 50)
	case "awaiting_review":
		result["output_schema"] = reviewResultTool().Schema
	}
	if review := session.Run.Review; review != nil {
		result["review"] = map[string]any{"summary": review.Summary, "needs_revision": review.NeedsRevision, "issues": review.Issues, "memory_count": len(review.Memories)}
	}
	return result
}

// writingMutationView keeps MCP replies small because every byte becomes agent context; other transports keep the full view.
func (service *ExternalService) writingMutationView(project *Project, session *ExternalWritingSession) map[string]any {
	if service.options.Transport == "mcp" {
		return compactWritingView(session)
	}
	return writingSessionView(project, session)
}

// externalReviewValid rejects unlocated or fabricated feedback before it becomes an accepted review.
func externalReviewValid(body string, review WritingReview, prompt *Prompt) error {
	if strings.TrimSpace(review.Summary) == "" {
		return externalFailure("INVALID_REVIEW", "A review summary is required")
	}
	if review.NeedsRevision && len(review.Issues) == 0 {
		return externalFailure("INVALID_REVIEW", "A revision request needs a located editing issue")
	}
	known := map[string]bool{}
	for _, item := range prompt.Context {
		known[item.ID] = true
	}
	for _, issue := range review.Issues {
		if !contains([]string{"continuity", "reading"}, issue.Kind) || !contains([]string{"prose", "composition", "context", "plan"}, issue.Scope) || issue.Quote == "" || !strings.Contains(body, issue.Quote) || strings.TrimSpace(issue.Explanation) == "" {
			return externalFailure("INVALID_REVIEW", "Each issue needs a valid scope, explanation and exact candidate quote")
		}
		for _, id := range issue.EvidenceIDs {
			if !known[id] {
				return externalFailure("INVALID_REVIEW", "An issue references unavailable evidence", map[string]any{"evidence_id": id})
			}
		}
	}
	for _, memory := range review.Memories {
		if !contains(writingMemoryKinds(), memory.Kind) || strings.TrimSpace(memory.Title) == "" || strings.TrimSpace(memory.Content) == "" {
			return externalFailure("INVALID_REVIEW", "Each memory needs a supported kind, title and source-supported content")
		}
		if charCount(strings.TrimSpace(memory.Quote)) < 4 || !strings.Contains(body, memory.Quote) {
			return externalFailure("INVALID_REVIEW", "Each memory needs an exact candidate quote of at least four characters")
		}
	}
	return nil
}
