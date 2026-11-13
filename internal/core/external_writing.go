package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// BeginWriting prepares a credential-free context and durably reserves a writing session.
func (service *ExternalService) BeginWriting(ctx context.Context, input WritingBeginInput) (any, error) {
	if err := service.writable(); err != nil {
		return nil, err
	}
	if err := validateExternalContext(ctx); err != nil {
		return nil, err
	}
	id, err := service.projectID(input.ProjectID)
	if err != nil {
		return nil, err
	}
	input.ProjectID = id
	fingerprint := hashText(jsonText(input))
	if result, found, err := externalReceipt(service.engine.store.db, id, "writing_begin", input.RequestID, fingerprint); found || err != nil {
		return result, err
	}
	if input.RevisionLimit == nil {
		value := 1
		input.RevisionLimit = &value
	}
	if *input.RevisionLimit < 0 {
		return nil, externalFailure("INVALID_INPUT", "revision_limit must be nonnegative")
	}
	if input.ContextChars == 0 {
		input.ContextChars = 64000
	}
	if input.ContextChars < 4000 || input.ContextChars > 500000 {
		return nil, externalFailure("INVALID_INPUT", "context_chars must be between 4000 and 500000")
	}
	if input.Mode == "" {
		input.Mode = "append"
	}
	if !contains([]string{"append", "replace", "create"}, input.Mode) {
		return nil, externalFailure("INVALID_INPUT", "Choose append, replace or create")
	}
	project, err := service.engine.store.Get(id)
	if err != nil {
		return nil, err
	}
	// Resolved after the replay fingerprint, like the other defaults, so retries still match.
	input.Language = externalSessionLanguage(&project, input.Language)
	session := ExternalWritingSession{ID: newID(), ProjectID: id, Revision: 1, Phase: "awaiting_composition", Input: input, BaseRevision: project.Revision, Client: service.options.Client, Transport: service.options.Transport, CreatedAt: now(), UpdatedAt: now(), Drafts: []ExternalDraftVersion{}}
	if err := reserveExternalChapter(&project, &session); err != nil {
		return nil, err
	}
	working := externalWritingProject(project, &session)
	job := externalWritingJob(ctx, &session)
	session.Run = WritingRun{Mode: input.Mode, Brief: writingBrief(&working, job)}
	if session.NewChapter {
		session.Run.Mode = "replace"
	}
	if adoptOutlineComposition(ctx, &working, &session) {
		session.Phase = "awaiting_draft"
	}
	if err := service.prepareExternalEvidence(ctx, &working, &session); err != nil {
		return nil, err
	}
	var result any
	err = service.engine.store.withWriteTransaction(func(db *sql.Conn) error {
		if prior, found, err := externalReceipt(db, id, "writing_begin", input.RequestID, fingerprint); found || err != nil {
			result = prior
			return err
		}
		current, err := readProject(db, id)
		if err != nil {
			return err
		}
		if err := externalSessionSources(&current, &session); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO writing_sessions(id,project_id,revision,document) VALUES(?,?,?,?)", session.ID, id, session.Revision, jsonText(session)); err != nil {
			return err
		}
		result = service.writingMutationView(&working, &session)
		return saveExternalReceipt(db, id, "writing_begin", input.RequestID, fingerprint, result)
	})
	return result, err
}

// reserveExternalChapter binds a saved chapter or reserves a planned chapter without editing the project.
func reserveExternalChapter(project *Project, session *ExternalWritingSession) error {
	input := &session.Input
	if input.ChapterID != "" {
		chapter := findChapter(project, input.ChapterID)
		if chapter == nil {
			return externalFailure("NOT_FOUND", "Chapter not found")
		}
		if input.Mode == "create" {
			return externalFailure("INVALID_INPUT", "Choose a new chapter target for create mode")
		}
		session.Target = *chapter
		return nil
	}
	if input.PlanID != "" {
		found := false
		for _, plan := range project.Studio.Plans {
			if plan.ID != input.PlanID {
				continue
			}
			if plan.Level != "chapter" || plan.Status != "confirmed" {
				return externalFailure("NEEDS_AUTHOR", "Choose a confirmed chapter outline")
			}
			if _, err := confirmedWritingPlans(project, plan.ID); err != nil {
				return err
			}
			input.Number, input.Title, found = plan.StartChapter, plan.Title, true
		}
		if !found {
			return externalFailure("NOT_FOUND", "Chapter outline not found")
		}
	}
	if strings.TrimSpace(input.Title) == "" {
		return externalFailure("INVALID_INPUT", "Choose chapter_id or supply a chapter outline or new chapter title")
	}
	if input.Number == 0 {
		input.Number = 1
		for _, chapter := range project.Chapters {
			input.Number = max(input.Number, chapter.Number+1)
		}
	}
	for _, chapter := range project.Chapters {
		if chapter.Number == input.Number {
			if input.PlanID != "" && input.Mode != "create" {
				session.Target = chapter
				input.ChapterID = chapter.ID
				return nil
			}
			return externalFailure("REVISION_CONFLICT", "This chapter number already exists; select its chapter_id")
		}
	}
	session.Target, session.NewChapter = NewChapter(input.Number, input.Title), true
	return nil
}

// adoptOutlineComposition reuses a confirmed chapter outline's complete scene cards, exactly as in-app writing does.
// It reports whether the cards were adopted; an outline that no longer qualifies clears the adopted composition.
func adoptOutlineComposition(ctx context.Context, project *Project, session *ExternalWritingSession) bool {
	session.Run.Composition = existingWritingComposition(project, externalWritingJob(ctx, session), session.Run.Mode)
	session.CompositionSource = ""
	if session.Run.Composition != nil {
		session.CompositionSource = "chapter_outline"
	}
	return session.Run.Composition != nil
}

// prepareExternalEvidence reuses prompt and evidence helpers with an explicit budget and no model selection.
func (service *ExternalService) prepareExternalEvidence(ctx context.Context, project *Project, session *ExternalWritingSession) error {
	job := externalWritingJob(ctx, session)
	action := "continue"
	if session.Run.Mode == "replace" {
		action = "rewrite"
	}
	request := AIRequest{ProjectID: project.ID, ChapterID: job.ChapterID, Action: action, Instruction: job.Instruction, Language: job.Language, CommitPolicy: "review", WorkspaceContext: job.WorkspaceContext}
	var err error
	request.Retrieved, err = service.engine.contextPassages(project, request)
	if err != nil {
		return err
	}
	prompt, err := prepareAI(project, request, session.Input.ContextChars)
	if err != nil {
		return err
	}
	if session.Run.Composition != nil {
		// Fresh evidence has new identities; previous unlocated citations remain explicit open questions.
		known := map[string]bool{}
		for _, item := range prompt.Context {
			known[item.ID] = true
		}
		kept := []string{}
		for _, id := range session.Run.Composition.EvidenceIDs {
			if known[id] {
				kept = append(kept, id)
			} else {
				session.Run.Composition.OpenQuestions = append(session.Run.Composition.OpenQuestions, "Evidence requires renewed verification: "+id)
			}
		}
		session.Run.Composition.EvidenceIDs = kept
		prompt, err = service.engine.gatherWritingEvidence(project, job, prompt, session.Run.Composition)
		if err != nil {
			return err
		}
	}
	if session.NewChapter {
		prompt.SourceID, prompt.SourceHash = "", ""
	}
	session.Run.SourcePrompt = &prompt
	session.EvidenceVersion = hashText(jsonText([]any{session.BaseRevision, prompt.SourceHash, prompt.Context}))
	return nil
}

// WritingStatus reads the current durable session after client or server restarts.
func (service *ExternalService) WritingStatus(ctx context.Context, request WritingSessionRequest) (any, error) {
	if err := validateExternalContext(ctx); err != nil {
		return nil, err
	}
	id, err := service.projectID(request.ProjectID)
	if err != nil {
		return nil, err
	}
	session, err := readExternalWritingSession(service.engine.store.db, id, request.WritingSessionID)
	if err != nil {
		return nil, err
	}
	project, err := service.engine.store.Get(id)
	if err != nil {
		return nil, err
	}
	working := externalWritingProject(project, &session)
	result := writingSessionView(&working, &session)
	if err := externalSessionSources(&project, &session); err != nil {
		result["sources_changed"] = ExternalEnvelope(nil, err)
	}
	return result, nil
}

// readExternalWritingSession binds session identity to its project in every storage lookup.
func readExternalWritingSession(db documentDB, projectID, id string) (ExternalWritingSession, error) {
	var raw string
	var session ExternalWritingSession
	if err := db.QueryRowContext(background(), "SELECT document FROM writing_sessions WHERE project_id=? AND id=?", projectID, id).Scan(&raw); err != nil {
		return session, err
	}
	err := decodeJSON([]byte(raw), &session)
	return session, err
}

// advanceExternalWriting computes material outside the write lock and atomically compares both revisions.
func (service *ExternalService) advanceExternalWriting(ctx context.Context, operation string, input WritingMutation, payload any, change func(*Project, *ExternalWritingSession) error) (any, error) {
	if err := service.writable(); err != nil {
		return nil, err
	}
	if err := validateExternalContext(ctx); err != nil {
		return nil, err
	}
	id, err := service.projectID(input.ProjectID)
	if err != nil {
		return nil, err
	}
	fingerprint := hashText(jsonText(payload))
	if result, found, err := externalReceipt(service.engine.store.db, id, operation, input.RequestID, fingerprint); found || err != nil {
		return result, err
	}
	session, err := readExternalWritingSession(service.engine.store.db, id, input.WritingSessionID)
	if err != nil {
		return nil, err
	}
	if session.Revision != input.SessionRevision {
		if result, found, err := externalReceipt(service.engine.store.db, id, operation, input.RequestID, fingerprint); found || err != nil {
			return result, err
		}
		return nil, externalFailure("REVISION_CONFLICT", "The writing session changed; reload its current state", map[string]any{"session_revision": session.Revision})
	}
	if contains([]string{"staged", "abandoned"}, session.Phase) {
		return nil, externalFailure("PHASE_ORDER", "This writing session has ended")
	}
	project, err := service.engine.store.Get(id)
	if err != nil {
		return nil, err
	}
	if operation != "writing_refresh" && operation != "writing_abandon" {
		if err := externalSessionSources(&project, &session); err != nil {
			return nil, err
		}
	}
	if operation == "writing_refresh" {
		session.RefreshedChanges = externalWritingSourceChanges(&project, &session)
	}
	working := externalWritingProject(project, &session)
	if err := change(&working, &session); err != nil {
		return nil, err
	}
	session.Revision++
	session.UpdatedAt = now()
	var result any
	err = service.engine.store.withWriteTransaction(func(db *sql.Conn) error {
		if prior, found, err := externalReceipt(db, id, operation, input.RequestID, fingerprint); found || err != nil {
			result = prior
			return err
		}
		stored, err := readExternalWritingSession(db, id, session.ID)
		if err != nil {
			return err
		}
		if stored.Revision != input.SessionRevision {
			return externalFailure("REVISION_CONFLICT", "Another client advanced this writing session")
		}
		current, err := readProject(db, id)
		if err != nil {
			return err
		}
		if operation != "writing_abandon" {
			if err := externalSessionSources(&current, &session); err != nil {
				return err
			}
		}
		if operation == "writing_finish" {
			candidate, err := externalWritingCandidate(&current, &session)
			if err != nil {
				return err
			}
			if err := persistStagedChangeSetsInDB(db, id, []ChangeSet{candidate}); err != nil {
				return err
			}
			session.CandidateID = candidate.ID
		}
		if _, err := db.ExecContext(ctx, "UPDATE writing_sessions SET revision=?,document=? WHERE project_id=? AND id=? AND revision=?", session.Revision, jsonText(session), id, session.ID, input.SessionRevision); err != nil {
			return err
		}
		result = service.writingMutationView(&working, &session)
		return saveExternalReceipt(db, id, operation, input.RequestID, fingerprint, result)
	})
	return result, err
}

// SubmitComposition validates scene cards before freezing their requested local evidence.
func (service *ExternalService) SubmitComposition(ctx context.Context, input WritingCompositionInput) (any, error) {
	return service.advanceExternalWriting(ctx, "writing_submit_composition", input.WritingMutation, input, func(project *Project, session *ExternalWritingSession) error {
		if !contains([]string{"awaiting_composition", "awaiting_draft", "awaiting_revision"}, session.Phase) {
			return externalFailure("PHASE_ORDER", "Submit composition before drafting or during its revision stage")
		}
		composition, err := parseWritingComposition(jsonText(input.Composition))
		if err != nil {
			return externalFailure("INVALID_INPUT", err.Error())
		}
		job := externalWritingJob(ctx, session)
		prompt, err := service.engine.gatherWritingEvidence(project, job, *session.Run.SourcePrompt, composition)
		if err != nil {
			return err
		}
		session.Run.Composition, session.Run.SourcePrompt, session.CompositionSource = composition, &prompt, ""
		session.EvidenceVersion = hashText(jsonText([]any{session.BaseRevision, prompt.SourceHash, prompt.Context}))
		if session.Run.Draft == "" {
			session.Phase = "awaiting_draft"
		} else {
			session.Phase = "awaiting_revision"
		}
		return nil
	})
}

// SubmitDraft checkpoints prose and prepares an external review of the exact candidate.
func (service *ExternalService) SubmitDraft(ctx context.Context, input WritingDraftInput) (any, error) {
	return service.advanceExternalWriting(ctx, "writing_submit_draft", input.WritingMutation, input, func(project *Project, session *ExternalWritingSession) error {
		if !contains([]string{"awaiting_draft", "awaiting_revision"}, session.Phase) {
			return externalFailure("PHASE_ORDER", "Submit prose after composition and evidence gathering")
		}
		if strings.TrimSpace(input.Draft) == "" || len(input.Draft) > 5_000_000 {
			return externalFailure("INVALID_INPUT", "Submit nonempty prose within 5 MB")
		}
		if session.Run.Draft != "" {
			session.Run.Revisions++
		}
		session.Run.Draft, session.Run.Review, session.Run.ReviewStatus = input.Draft, nil, "pending_external"
		session.Run.CandidateHash = hashText(input.Draft)
		session.Drafts = append(session.Drafts, ExternalDraftVersion{Draft: input.Draft, Hash: hashText(input.Draft), EvidenceVersion: session.EvidenceVersion, Composition: copyWritingComposition(session.Run.Composition), CreatedAt: now()})
		session.Phase = "awaiting_review"
		return nil
	})
}

// SubmitReview accepts source-located external feedback and chooses the next permitted stage.
func (service *ExternalService) SubmitReview(ctx context.Context, input WritingReviewInput) (any, error) {
	return service.advanceExternalWriting(ctx, "writing_submit_review", input.WritingMutation, input, func(project *Project, session *ExternalWritingSession) error {
		if session.Phase != "awaiting_review" {
			return externalFailure("PHASE_ORDER", "Submit a review for the current draft")
		}
		if input.DraftHash != hashText(session.Run.Draft) || input.EvidenceVersion != session.EvidenceVersion {
			return externalFailure("REVIEW_VERSION", "Review the current draft and evidence version")
		}
		body := writingCandidateBody(findChapter(project, session.Target.ID), session.Run.Draft, session.Run.Mode)
		review := input.Review
		if err := externalReviewValid(body, review, session.Run.SourcePrompt); err != nil {
			return err
		}
		review.Structured, review.Warning = true, ""
		appendWritingLanguageIssues(project, session.Run.Draft, &review)
		appendVoiceDriftIssues(project, findChapter(project, session.Target.ID), session.Run.Draft, session.Run.Revisions, &review)
		session.Run.Review, session.Run.ReviewStatus = &review, writingReviewStatus(&review)
		draft := &session.Drafts[len(session.Drafts)-1]
		if len(draft.Reviews) == 0 && draft.Review != nil {
			draft.Reviews = append(draft.Reviews, ExternalDraftReview{EvidenceVersion: draft.EvidenceVersion, Review: *draft.Review, CreatedAt: draft.CreatedAt})
		}
		draft.Review = &review
		draft.Reviews = append(draft.Reviews, ExternalDraftReview{EvidenceVersion: session.EvidenceVersion, Review: review, CreatedAt: now()})
		session.Phase = "reviewed"
		if review.NeedsRevision && writingRevisionScope(&review) != "plan" && session.Run.Revisions < *session.Input.RevisionLimit {
			session.Phase = "awaiting_revision"
		}
		return nil
	})
}

// FinishWriting stages the reviewed compound result exactly once.
func (service *ExternalService) FinishWriting(ctx context.Context, input WritingMutation) (any, error) {
	return service.advanceExternalWriting(ctx, "writing_finish", input, input, func(project *Project, session *ExternalWritingSession) error {
		if session.Phase != "reviewed" {
			return externalFailure("PHASE_ORDER", "Complete the external review before finishing")
		}
		session.Phase = "staged"
		return nil
	})
}

// RefreshWriting retains drafts while rebuilding the evidence and invalidating previous reviews.
func (service *ExternalService) RefreshWriting(ctx context.Context, input WritingMutation) (any, error) {
	return service.advanceExternalWriting(ctx, "writing_refresh", input, input, func(project *Project, session *ExternalWritingSession) error {
		session.BaseRevision = project.Revision
		if session.NewChapter {
			for _, chapter := range project.Chapters {
				if chapter.ID != session.Target.ID && chapter.Number == session.Target.Number {
					return externalFailure("REVISION_CONFLICT", "The reserved chapter number is now occupied")
				}
			}
		} else {
			chapter := findChapter(project, session.Target.ID)
			if chapter == nil {
				return externalFailure("NOT_FOUND", "The writing chapter was deleted")
			}
			session.Target = *chapter
		}
		if session.CompositionSource == "chapter_outline" && session.Run.Draft == "" {
			// Outline-adopted cards follow the author's latest confirmed outline until prose exists.
			adoptOutlineComposition(ctx, project, session)
		}
		if err := service.prepareExternalEvidence(ctx, project, session); err != nil {
			return err
		}
		session.Run.Review, session.Run.ReviewStatus = nil, ""
		session.Phase = "awaiting_composition"
		if session.Run.Composition != nil {
			session.Phase = "awaiting_draft"
		}
		if session.Run.Draft != "" {
			session.Phase = "awaiting_review"
		}
		return nil
	})
}

// AbandonWriting ends a durable session while retaining its draft history for inspection.
func (service *ExternalService) AbandonWriting(ctx context.Context, input WritingMutation) (any, error) {
	return service.advanceExternalWriting(ctx, "writing_abandon", input, input, func(project *Project, session *ExternalWritingSession) error { session.Phase = "abandoned"; return nil })
}

// externalWritingCandidate reuses the compound candidate builder without an executing model job.
func externalWritingCandidate(project *Project, session *ExternalWritingSession) (ChangeSet, error) {
	working := externalWritingProject(*project, session)
	job := externalWritingJob(context.Background(), session)
	proposals := writingCandidateProposals(job, &working, &session.Run)
	candidate := proposalsToChangeSet(&working, &job, proposals)
	if candidate == nil {
		return ChangeSet{}, externalFailure("INVALID_INPUT", "This writing result contains no changes")
	}
	candidate.Source, candidate.JobID, candidate.SessionID, candidate.ProposalLinksVersion = "external", "", "", 0
	candidate.BaseRevision, candidate.CommitPolicy = project.Revision, "review"
	candidate.Provenance = &ExternalProvenance{Client: session.Client, Transport: session.Transport, WritingSessionID: session.ID, DraftHash: hashText(session.Run.Draft), EvidenceVersion: session.EvidenceVersion, Review: session.Run.Review, Revisions: session.Run.Revisions}
	for i := range candidate.Operations {
		op := &candidate.Operations[i]
		op.ProposalID = ""
		if session.NewChapter && op.TargetKind == TargetChapter {
			number, title := session.Target.Number, session.Target.Title
			op.TargetID, op.OpType, op.BeforeValue = session.Target.ID, OpUpsert, nil
			op.AfterValue = rawMessage(chapterProsePatch{Title: &title, Number: &number, Body: session.Run.Draft})
		}
	}
	copy := cloneProject(*project)
	check := *candidate
	check.Operations = append([]ChangeOperation{}, candidate.Operations...)
	if err := applyChangeSetToProject(&copy, &check); err != nil {
		return ChangeSet{}, err
	}
	return *candidate, nil
}

// externalWritingPayload validates one typed writing command supplied by a transport.
func externalWritingPayload[T any](raw json.RawMessage, call func(T) (any, error)) (any, error) {
	var input T
	if err := externalDecode(raw, &input); err != nil {
		return nil, err
	}
	return call(input)
}
