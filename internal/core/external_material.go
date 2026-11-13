package core

import (
	"context"
	"strings"
)

// WritingMaterialRequest reads one bounded window of a durable draft or stage packet.
type WritingMaterialRequest struct {
	ProjectID        string `json:"project_id,omitempty"`
	WritingSessionID string `json:"writing_session_id"`
	Kind             string `json:"kind,omitempty"`
	Offset           int    `json:"offset,omitempty"`
	MaxChars         int    `json:"max_chars,omitempty"`
}

// boundedWritingView keeps large stage packets resumable instead of relying on host truncation.
func boundedWritingView(result map[string]any, session *ExternalWritingSession) map[string]any {
	if charCount(jsonText(result)) <= 60000 {
		return result
	}
	result["material_truncated"], result["draft_truncated"], result["review_truncated"] = true, charCount(session.Run.Draft) > 8000, session.Run.Review != nil
	result["messages"] = nil
	draft := []rune(session.Run.Draft)
	result["draft"] = string(draft[:min(len(draft), 8000)])
	result["brief"] = truncateSummary(session.Run.Brief, 8000)
	result["evidence"] = writingEvidenceReferences(session, 50)
	if session.Run.Review != nil {
		result["review"] = map[string]any{"summary": truncateSummary(session.Run.Review.Summary, 2000), "needs_revision": session.Run.Review.NeedsRevision, "issue_count": len(session.Run.Review.Issues)}
	}
	result["material_operation"] = "writing_material"
	if charCount(jsonText(result)) > 60000 {
		result["evidence"] = nil
	}
	return result
}

// writingEvidenceReferences lists evidence identities without their text, so an agent can cite exact IDs cheaply.
func writingEvidenceReferences(session *ExternalWritingSession, limit int) []map[string]any {
	references := []map[string]any{}
	if session.Run.SourcePrompt == nil {
		return references
	}
	for _, item := range session.Run.SourcePrompt.Context {
		references = append(references, map[string]any{"id": item.ID, "kind": item.Kind, "title": truncateSummary(item.Title, 120), "source_id": item.SourceID, "source_hash": item.SourceHash})
		if len(references) == limit {
			break
		}
	}
	return references
}

// WritingMaterial returns exact full text through Unicode windows, retaining source and draft hashes.
func (service *ExternalService) WritingMaterial(ctx context.Context, input WritingMaterialRequest) (any, error) {
	id, err := service.projectID(input.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := validateExternalContext(ctx); err != nil {
		return nil, err
	}
	session, err := readExternalWritingSession(service.engine.store.db, id, input.WritingSessionID)
	if err != nil {
		return nil, err
	}
	text := ""
	switch input.Kind {
	case "draft":
		text = session.Run.Draft
	case "review":
		text = jsonText(session.Run.Review)
	case "evidence":
		text = jsonText(session.Run.SourcePrompt)
	case "changes":
		project, err := service.engine.store.Get(id)
		if err != nil {
			return nil, err
		}
		changes := externalWritingSourceChanges(&project, &session)
		if len(changes) == 0 {
			changes = session.RefreshedChanges
		}
		text = jsonText(changes)
	case "", "stage":
		project, err := service.engine.store.Get(id)
		if err != nil {
			return nil, err
		}
		if err := externalSessionSources(&project, &session); err != nil {
			return nil, err
		}
		working := externalWritingProject(project, &session)
		view := writingSessionView(&working, &session)
		// Rebuild only the requested stage packet, keeping the public status response bounded.
		prompt, err := externalStagePrompt(&working, &session)
		if err != nil {
			return nil, err
		}
		for _, message := range prompt.Messages {
			text += "[" + message.Role + "]\n" + message.Content + "\n\n"
		}
		if strings.TrimSpace(text) == "" {
			text = jsonText(view)
		}
	default:
		return nil, externalFailure("INVALID_INPUT", "Choose stage, draft, review, evidence or changes")
	}
	window, err := externalTextWindow(text, input.Offset, input.MaxChars)
	if err != nil {
		return nil, err
	}
	window["writing_session_id"], window["session_revision"] = session.ID, session.Revision
	window["evidence_version"], window["draft_hash"] = session.EvidenceVersion, hashText(session.Run.Draft)
	return window, nil
}

// externalTextWindow slices exact text into one Unicode window (30000 characters by default, at most 60000)
// and reports the offset of the next window and the hash of the whole text.
func externalTextWindow(text string, offset, limit int) (map[string]any, error) {
	if limit == 0 {
		limit = 30000
	}
	characters := []rune(text)
	if offset < 0 || offset > len(characters) || limit < 1 || limit > 60000 {
		return nil, externalFailure("INVALID_INPUT", "Invalid material window")
	}
	end := min(len(characters), offset+limit)
	return map[string]any{"text": string(characters[offset:end]), "text_hash": hashText(text), "offset": offset, "next_offset": end, "total_chars": len(characters), "truncated": end < len(characters)}, nil
}

// externalStagePrompt prepares the exact contract associated with the session's current phase.
func externalStagePrompt(project *Project, session *ExternalWritingSession) (Prompt, error) {
	if session.Run.SourcePrompt == nil || findChapter(project, session.Target.ID) == nil {
		return Prompt{}, externalFailure("SOURCES_CHANGED", "The writing target is unavailable; retain this draft and refresh its sources")
	}
	job := externalWritingJob(context.Background(), session)
	config := ProviderConfig{ContextChars: uint64(session.Input.ContextChars)}
	switch session.Phase {
	case "awaiting_composition":
		return compositionPrompt(project, job, *session.Run.SourcePrompt, &session.Run, config)
	case "awaiting_draft", "awaiting_revision":
		return writingDraftPromptWithVoice(project, findChapter(project, session.Target.ID), *session.Run.SourcePrompt, &session.Run, session.Input.ContextChars), nil
	case "awaiting_review":
		return writingReviewPrompt(project, job, *session.Run.SourcePrompt, &session.Run, config)
	}
	return Prompt{}, nil
}
