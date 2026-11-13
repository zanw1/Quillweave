package core

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// wikiSourceFacts selects the complete current source version in deterministic identity order.
func wikiSourceFacts(project *Project, sourceID, sourceHash string) []Fact {
	facts := []Fact{}
	for _, fact := range project.Facts {
		if fact.SourceID == sourceID && fact.SourceHash == sourceHash {
			facts = append(facts, fact)
		}
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].ID < facts[j].ID })
	return facts
}

// wikiEntityHash tracks only names used by this source's facts, including previously unresolved names.
func wikiEntityHash(project *Project, facts []Fact) string {
	names := map[string]bool{}
	for _, fact := range facts {
		names[fact.Entity] = true
		if fact.RelatedEntity != "" {
			names[fact.RelatedEntity] = true
		}
	}
	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	values := []any{}
	for _, name := range keys {
		entity := project.ResolveEntity(name)
		if entity == nil {
			values = append(values, []any{name, nil})
		} else {
			values = append(values, []any{name, entity.ID, entity.Name, entity.Kind, entity.Aliases})
		}
	}
	return hashText(jsonText(values))
}

// wikiSourceChanges compares actual source, rule, fact and entity dependencies independently of unrelated saves.
func wikiSourceChanges(project *Project, session *ExternalWikiSession) []WritingSourceChange {
	changes := []WritingSourceChange{}
	text, chapter, exists := project.SourceText(session.SourceID)
	if !exists || hashText(text) != session.SourceHash || chapter != session.Chapter {
		changes = append(changes, WritingSourceChange{ID: session.SourceID, Kind: "source", ExpectedHash: session.SourceHash, CurrentHash: hashText(text), Deleted: !exists})
	}
	for _, source := range project.Sources {
		if source.ID == session.SourceID && source.Category == "craft" {
			changes = append(changes, WritingSourceChange{ID: source.ID, Kind: "source_category"})
		}
	}
	for _, dependency := range []struct{ kind, expected, current string }{
		{"rules", session.RulesHash, rulesHash(project)},
		{"facts", session.FactsHash, hashText(jsonText(wikiSourceFacts(project, session.SourceID, session.SourceHash)))},
		{"entities", session.EntityHash, wikiEntityHash(project, session.Facts)},
	} {
		if dependency.expected != dependency.current {
			changes = append(changes, WritingSourceChange{ID: session.SourceID, Kind: dependency.kind, ExpectedHash: dependency.expected, CurrentHash: dependency.current})
		}
	}
	return changes
}

// validateWikiSources gives callers the precise dependencies that require renewed extraction or review.
func validateWikiSources(project *Project, session *ExternalWikiSession) error {
	changes := wikiSourceChanges(project, session)
	if len(changes) > 0 {
		return externalFailure("SOURCES_CHANGED", "Wiki dependencies changed; refresh this session", map[string]any{"changes": changes})
	}
	return nil
}

// prepareWikiSession freezes a complete source and resets all derived stage data on begin or refresh.
func prepareWikiSession(project *Project, session *ExternalWikiSession) error {
	prompt, err := wikiSourcePrompt(project, AIRequest{SourceID: session.SourceID})
	if err != nil {
		return externalFailure("INVALID_INPUT", err.Error())
	}
	session.SourceText, session.Chapter, _ = project.SourceText(session.SourceID)
	session.SourceHash, session.RulesHash = prompt.SourceHash, rulesHash(project)
	session.BaseFacts = wikiSourceFacts(project, session.SourceID, session.SourceHash)
	session.FactsHash = hashText(jsonText(session.BaseFacts))
	session.KnownEntities = append([]Entity{}, project.Entities...)
	for i := range session.KnownEntities {
		session.KnownEntities[i].Description = ""
	}
	session.Chunks = []WikiChunk{}
	characters := []rune(session.SourceText)
	for offset := 0; offset < len(characters); {
		end := min(offset+6000, len(characters))
		session.Chunks = append(session.Chunks, WikiChunk{ID: hashText(fmt.Sprintf("%s/%d/%d", session.SourceHash, offset, end)), Offset: offset, End: end, Submitted: session.Mode == "review", Candidates: []Candidate{}, Errors: []string{}})
		if end == len(characters) {
			break
		}
		offset = end - 150
	}
	session.Facts, session.Entities, session.Reviews, session.Sections = append([]Fact{}, session.BaseFacts...), []Entity{}, []WikiFactReview{}, []MemorySection{}
	session.CandidateID = ""
	return rebuildWikiFacts(project, session)
}

// rebuildWikiFacts shares ingestion while keeping newly assigned entity identities stable across checkpoints.
func rebuildWikiFacts(project *Project, session *ExternalWikiSession) error {
	working := cloneProject(*project)
	candidates := []Candidate{}
	for _, chunk := range session.Chunks {
		candidates = append(candidates, chunk.Candidates...)
	}
	if _, err := ingest(&working, session.SourceID, session.SourceHash, candidates, "external:"+session.Client); err != nil {
		return externalFailure("INVALID_INPUT", err.Error())
	}
	if session.Mode == "review" {
		for _, candidate := range candidates {
			name := working.CanonicalName(strings.TrimSpace(candidate.Entity))
			for i := range working.Facts {
				fact := &working.Facts[i]
				if fact.SourceID == session.SourceID && fact.SourceHash == session.SourceHash && fact.Entity == name && fact.Kind == candidate.Kind && strings.TrimSpace(fact.Claim) == strings.TrimSpace(candidate.Claim) {
					fact.Quote, fact.Epistemic = candidate.Quote, candidate.Epistemic
					fact.StoryTime, fact.Location, fact.RelatedEntity = candidate.StoryTime, candidate.Location, working.CanonicalName(candidate.RelatedEntity)
				}
			}
		}
	}
	entities := []Entity{}
	for _, entity := range working.Entities {
		if project.ResolveEntity(entity.Name) != nil {
			continue
		}
		for _, previous := range session.Entities {
			if previous.Name == entity.Name {
				entity.ID = previous.ID
			}
		}
		entities = append(entities, entity)
	}
	session.Entities, session.Facts = entities, wikiSourceFacts(&working, session.SourceID, session.SourceHash)
	session.EntityHash = wikiEntityHash(project, session.Facts)
	session.EvidenceVersion = hashText(jsonText([]any{session.SourceHash, session.RulesHash, session.EntityHash, session.FactsHash, session.Facts}))
	session.Reviews, session.Sections = []WikiFactReview{}, []MemorySection{}
	if session.Mode == "compile" {
		for _, fact := range session.BaseFacts {
			if contains([]string{"confirmed", "rejected"}, fact.Status) {
				decision := "accept"
				if fact.Status == "rejected" {
					decision = "reject"
				}
				session.Reviews = append(session.Reviews, WikiFactReview{FactID: fact.ID, FactHash: wikiFactHash(fact), Decision: decision, Reason: "Preserved existing author decision"})
			}
		}
	}
	advanceWikiPhase(session)
	return nil
}

// advanceWikiPhase derives the next permitted stage from complete block coverage and per-fact review.
func advanceWikiPhase(session *ExternalWikiSession) {
	session.Phase = "awaiting_extraction"
	for _, chunk := range session.Chunks {
		if !chunk.Submitted {
			return
		}
	}
	session.Phase = "awaiting_review"
	for _, fact := range session.Facts {
		index := slices.IndexFunc(session.Reviews, func(review WikiFactReview) bool {
			return review.FactID == fact.ID && review.FactHash == wikiFactHash(fact)
		})
		if index < 0 || session.Reviews[index].Decision == "needs_revision" {
			return
		}
	}
	session.Phase = "ready_to_stage"
	if len(wikiAcceptedFacts(session)) > 0 && len(session.Sections) == 0 {
		session.Phase = "awaiting_compilation"
	}
}

// wikiAcceptedFacts returns only facts accepted against their exact current semantic hash.
func wikiAcceptedFacts(session *ExternalWikiSession) []Fact {
	facts := []Fact{}
	for _, fact := range session.Facts {
		if slices.ContainsFunc(session.Reviews, func(review WikiFactReview) bool {
			return review.FactID == fact.ID && review.FactHash == wikiFactHash(fact) && review.Decision == "accept"
		}) {
			facts = append(facts, fact)
		}
	}
	return facts
}

// wikiSessionView keeps routine replies compact and points to exact, resumable material windows.
func wikiSessionView(session *ExternalWikiSession) map[string]any {
	processed, invalid := 0, 0
	chunks := []map[string]any{}
	for _, chunk := range session.Chunks {
		if chunk.Submitted {
			processed++
		}
		invalid += len(chunk.Errors)
		chunks = append(chunks, map[string]any{"id": chunk.ID, "offset": chunk.Offset, "end": chunk.End, "submitted": chunk.Submitted, "invalid_records": len(chunk.Errors)})
	}
	next := map[string]string{"awaiting_extraction": "qw_wiki_submit_extraction", "awaiting_review": "qw_wiki_submit_review", "awaiting_compilation": "qw_wiki_submit_compilation", "ready_to_stage": "qw_wiki_finish", "staged": "qw_review_candidate", "stale": "qw_wiki_refresh"}[session.Phase]
	remaining, revisions := 0, 0
	for _, fact := range session.Facts {
		index := slices.IndexFunc(session.Reviews, func(review WikiFactReview) bool {
			return review.FactID == fact.ID && review.FactHash == wikiFactHash(fact)
		})
		if index < 0 {
			remaining++
		} else if session.Reviews[index].Decision == "needs_revision" {
			remaining++
			revisions++
		}
	}
	if session.Phase == "awaiting_extraction" {
		remaining = len(session.Chunks) - processed
	}
	if session.Phase == "awaiting_review" && revisions > 0 {
		next = "qw_wiki_submit_extraction"
	}
	if contains([]string{"awaiting_compilation", "ready_to_stage", "staged", "stale"}, session.Phase) {
		remaining = 1
	}
	result := map[string]any{"wiki_session_id": session.ID, "project_id": session.ProjectID, "source_id": session.SourceID, "session_revision": session.Revision, "phase": session.Phase, "mode": session.Mode, "source_hash": session.SourceHash, "evidence_version": session.EvidenceVersion, "fact_count": len(session.Facts), "review_count": len(session.Reviews), "section_count": len(session.Sections), "processed_chunks": processed, "total_chunks": len(session.Chunks), "invalid_records": invalid, "candidate_id": session.CandidateID, "next_tool": next, "material_operation": "wiki_material"}
	result["remaining_items"], result["needs_revision_count"], result["no_changes"] = remaining, revisions, session.Phase == "completed" && session.CandidateID == ""
	result["chunks"] = chunks[:min(50, len(chunks))]
	result["chunks_truncated"] = len(chunks) > 50
	return result
}

// BeginWiki creates a credential-free compilation session and its replay receipt in one transaction.
func (service *ExternalService) BeginWiki(ctx context.Context, input WikiBeginInput) (any, error) {
	if err := service.writable(); err != nil {
		return nil, err
	}
	id, err := service.projectID(input.ProjectID)
	if err != nil {
		return nil, err
	}
	input.ProjectID = id
	fingerprint := hashText(jsonText(input))
	var result any
	err = service.engine.store.withWriteTransaction(func(db *sql.Conn) error {
		if prior, found, err := externalReceipt(db, id, "wiki_begin", input.RequestID, fingerprint); found || err != nil {
			result = prior
			return err
		}
		if err := validateExternalContext(ctx); err != nil {
			return err
		}
		if input.Mode == "" {
			input.Mode = "compile"
		}
		if !contains([]string{"compile", "review"}, input.Mode) {
			return externalFailure("INVALID_INPUT", "Choose compile or review mode")
		}
		project, err := readProject(db, id)
		if err != nil {
			return err
		}
		session := ExternalWikiSession{ID: newID(), ProjectID: id, SourceID: input.SourceID, Revision: 1, Mode: input.Mode, Instruction: input.Instruction, Language: externalSessionLanguage(&project, input.Language), Client: service.options.Client, Transport: service.options.Transport, CreatedAt: now(), UpdatedAt: now()}
		if err := prepareWikiSession(&project, &session); err != nil {
			return err
		}
		if err := saveWikiSession(db, &session); err != nil {
			return err
		}
		result = wikiSessionView(&session)
		return saveExternalReceipt(db, id, "wiki_begin", input.RequestID, fingerprint, result)
	})
	return result, err
}

// mutateWiki applies a small local stage transition under one transaction and revision check.
func (service *ExternalService) mutateWiki(ctx context.Context, operation string, input WikiMutation, payload any, change func(*sql.Conn, *Project, *ExternalWikiSession) error) (any, error) {
	if err := service.writable(); err != nil {
		return nil, err
	}
	id, err := service.projectID(input.ProjectID)
	if err != nil {
		return nil, err
	}
	raw := rawMessage(payload)
	if len(raw) > 8_000_000 {
		return nil, externalFailure("INVALID_INPUT", "The structured input exceeds 8 MB")
	}
	fingerprint := hashText(string(raw))
	var result any
	err = service.engine.store.withWriteTransaction(func(db *sql.Conn) error {
		if prior, found, err := externalReceipt(db, id, operation, input.RequestID, fingerprint); found || err != nil {
			result = prior
			return err
		}
		if err := validateExternalContext(ctx); err != nil {
			return err
		}
		session, err := readWikiSession(db, id, input.WikiSessionID)
		if err != nil {
			return err
		}
		if session.Revision != input.SessionRevision {
			return externalFailure("REVISION_CONFLICT", "Reload the Wiki session before continuing", map[string]any{"session_revision": session.Revision})
		}
		if contains([]string{"completed", "abandoned"}, session.Phase) {
			return externalFailure("PHASE_ORDER", "This Wiki session has ended")
		}
		project, err := readProject(db, id)
		if err != nil {
			return err
		}
		if operation != "wiki_refresh" && operation != "wiki_abandon" {
			if session.Phase == "stale" {
				return externalFailure("SOURCES_CHANGED", "Refresh this restored Wiki session before continuing")
			}
			if err := validateWikiSources(&project, &session); err != nil {
				return err
			}
		}
		if session.Phase == "staged" && !contains([]string{"wiki_refresh", "wiki_abandon", "wiki_finish"}, operation) {
			return externalFailure("PHASE_ORDER", "Refresh or review the saved Wiki candidate")
		}
		if err := change(db, &project, &session); err != nil {
			return err
		}
		session.Revision++
		session.UpdatedAt = now()
		if err := saveWikiSession(db, &session); err != nil {
			return err
		}
		result = wikiSessionView(&session)
		return saveExternalReceipt(db, id, operation, input.RequestID, fingerprint, result)
	})
	return result, err
}

// SubmitWikiExtraction saves valid records and diagnostics while incomplete or invalid blocks stay pending.
func (service *ExternalService) SubmitWikiExtraction(ctx context.Context, input WikiExtractionInput) (any, error) {
	return service.mutateWiki(ctx, "wiki_submit_extraction", input.WikiMutation, input, func(_ *sql.Conn, project *Project, session *ExternalWikiSession) error {
		if input.SourceHash != session.SourceHash {
			return externalFailure("INVALID_INPUT", "Submit extraction for the current source hash")
		}
		index := slices.IndexFunc(session.Chunks, func(chunk WikiChunk) bool { return chunk.ID == input.ChunkID })
		if index < 0 {
			return externalFailure("INVALID_INPUT", "Choose a chunk_id from wiki_material kind chunks")
		}
		chunk := &session.Chunks[index]
		text := string([]rune(session.SourceText)[chunk.Offset:chunk.End])
		accepted, skipped, err := parseCandidates(jsonText(map[string]any{"facts": input.Facts}), text)
		if err != nil {
			return externalFailure("INVALID_INPUT", err.Error())
		}
		chunk.Candidates, chunk.Errors, chunk.Submitted = accepted, skipped, len(skipped) == 0
		return rebuildWikiFacts(project, session)
	})
}

// SubmitWikiReview retains author decisions during compilation and supports explicit review-mode changes.
func (service *ExternalService) SubmitWikiReview(ctx context.Context, input WikiReviewInput) (any, error) {
	return service.mutateWiki(ctx, "wiki_submit_review", input.WikiMutation, input, func(_ *sql.Conn, _ *Project, session *ExternalWikiSession) error {
		if session.Phase == "awaiting_extraction" || input.EvidenceVersion != session.EvidenceVersion {
			return externalFailure("REVIEW_VERSION", "Finish extraction and review the current evidence version")
		}
		if len(input.Reviews) < 1 || len(input.Reviews) > 100 {
			return externalFailure("INVALID_INPUT", "Submit 1–100 fact reviews per request")
		}
		seen := map[string]bool{}
		for _, review := range input.Reviews {
			index := slices.IndexFunc(session.Facts, func(fact Fact) bool { return fact.ID == review.FactID })
			if index < 0 || seen[review.FactID] || review.FactHash != wikiFactHash(session.Facts[index]) {
				return externalFailure("REVIEW_VERSION", "Review each current fact exactly once per batch")
			}
			seen[review.FactID] = true
			if !contains([]string{"accept", "reject", "needs_revision"}, review.Decision) || strings.TrimSpace(review.Reason) == "" || charCount(review.Reason) > 2000 {
				return externalFailure("INVALID_INPUT", "Each review needs a valid decision and a reason within 2000 characters")
			}
			for _, original := range session.BaseFacts {
				if session.Mode == "compile" && original.ID == review.FactID && (original.Status == "confirmed" && review.Decision != "accept" || original.Status == "rejected" && review.Decision != "reject") {
					return externalFailure("NEEDS_AUTHOR", "Use review mode to propose changing an existing author decision")
				}
			}
			position := slices.IndexFunc(session.Reviews, func(existing WikiFactReview) bool { return existing.FactID == review.FactID })
			if position < 0 {
				session.Reviews = append(session.Reviews, review)
			} else {
				session.Reviews[position] = review
			}
		}
		session.Sections = []MemorySection{}
		advanceWikiPhase(session)
		return nil
	})
}

// SubmitWikiCompilation accepts only summaries referencing the fully reviewed, accepted fact set.
func (service *ExternalService) SubmitWikiCompilation(ctx context.Context, input WikiCompilationInput) (any, error) {
	return service.mutateWiki(ctx, "wiki_submit_compilation", input.WikiMutation, input, func(_ *sql.Conn, _ *Project, session *ExternalWikiSession) error {
		if !contains([]string{"awaiting_compilation", "ready_to_stage"}, session.Phase) || input.EvidenceVersion != session.EvidenceVersion {
			return externalFailure("REVIEW_VERSION", "Complete reviews for the current evidence before compiling")
		}
		facts := wikiAcceptedFacts(session)
		if len(facts) == 0 || len(input.Sections) == 0 {
			return externalFailure("INVALID_INPUT", "Compilation needs accepted evidence and nonempty sections")
		}
		for _, section := range input.Sections {
			if _, err := parseMemory(jsonText(section), facts); err != nil {
				return externalFailure("INVALID_INPUT", err.Error())
			}
		}
		session.Sections = input.Sections
		advanceWikiPhase(session)
		return nil
	})
}

// FinishWiki stages the complete source atomically, or records a genuinely empty source as completed.
func (service *ExternalService) FinishWiki(ctx context.Context, input WikiMutation) (any, error) {
	return service.mutateWiki(ctx, "wiki_finish", input, input, func(db *sql.Conn, project *Project, session *ExternalWikiSession) error {
		if session.Phase == "staged" {
			return nil
		}
		if session.Phase != "ready_to_stage" {
			return externalFailure("PHASE_ORDER", "Complete source coverage, fact review and compilation first")
		}
		candidate, changed, err := wikiCandidate(project, session)
		if err != nil {
			return err
		}
		if !changed {
			session.Phase = "completed"
			return nil
		}
		if err := persistStagedChangeSetsInDB(db, project.ID, []ChangeSet{candidate}); err != nil {
			return err
		}
		session.CandidateID, session.Phase = candidate.ID, "staged"
		return nil
	})
}

// retireWikiCandidate withdraws a session's pending result before refresh or abandonment.
func retireWikiCandidate(db documentDB, project *Project, session *ExternalWikiSession) error {
	if session.CandidateID == "" {
		return nil
	}
	candidate := findChangeSet(project, session.CandidateID)
	if candidate == nil || !contains([]string{"staged", "conflicted", "rejected"}, string(candidate.Status)) {
		return externalFailure("PHASE_ORDER", "The Wiki candidate was applied; start a new session for further changes")
	}
	candidate.Status, candidate.ReviewToken, candidate.UpdatedAt = ChangeSetRejected, "", now()
	return writeChangeSetRecord(db, project.ID, *candidate)
}

// RefreshWiki replaces stale dependencies and withdraws the previous pending candidate in the same transaction.
func (service *ExternalService) RefreshWiki(ctx context.Context, input WikiMutation) (any, error) {
	return service.mutateWiki(ctx, "wiki_refresh", input, input, func(db *sql.Conn, project *Project, session *ExternalWikiSession) error {
		if err := retireWikiCandidate(db, project, session); err != nil {
			return err
		}
		return prepareWikiSession(project, session)
	})
}

// AbandonWiki ends work while retaining stage material and withdrawing any pending session candidate.
func (service *ExternalService) AbandonWiki(ctx context.Context, input WikiMutation) (any, error) {
	return service.mutateWiki(ctx, "wiki_abandon", input, input, func(db *sql.Conn, project *Project, session *ExternalWikiSession) error {
		if err := retireWikiCandidate(db, project, session); err != nil {
			return err
		}
		session.Phase = "abandoned"
		return nil
	})
}
