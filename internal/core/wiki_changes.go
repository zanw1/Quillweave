package core

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// wikiCandidate builds a stable, source-scoped compound patch after every fact has a current review.
func wikiCandidate(project *Project, session *ExternalWikiSession) (ChangeSet, bool, error) {
	provenance := &ExternalProvenance{Client: session.Client, Transport: session.Transport, WikiSessionID: session.ID, EvidenceVersion: session.EvidenceVersion}
	patch := WikiPatch{SourceID: session.SourceID, SourceHash: session.SourceHash, Chapter: session.Chapter, RulesHash: session.RulesHash, EntityHash: session.EntityHash, FactsHash: session.FactsHash, Entities: session.Entities, Facts: []Fact{}, Reviews: session.Reviews}
	for _, fact := range session.Facts {
		index := slices.IndexFunc(session.Reviews, func(review WikiFactReview) bool {
			return review.FactID == fact.ID && review.FactHash == wikiFactHash(fact)
		})
		if index < 0 || session.Reviews[index].Decision == "needs_revision" {
			return ChangeSet{}, false, externalFailure("REVIEW_VERSION", "Every staged fact needs its current review")
		}
		fact.Status = "confirmed"
		if session.Reviews[index].Decision == "reject" {
			fact.Status = "rejected"
		}
		original := slices.IndexFunc(session.BaseFacts, func(value Fact) bool { return value.ID == fact.ID })
		if original >= 0 && jsonText(fact) == jsonText(session.BaseFacts[original]) {
			continue
		}
		fact.Provenance = provenance
		patch.Facts = append(patch.Facts, fact)
	}
	if len(session.Sections) > 0 {
		evidence := []string{}
		for _, fact := range wikiAcceptedFacts(session) {
			evidence = append(evidence, wikiFactHash(fact))
		}
		fingerprint := hashText(jsonText([]any{"external-wiki-1", session.SourceHash, session.RulesHash, evidence, session.Sections}))
		duplicate := false
		for _, previous := range latestCompilations(project) {
			if previous.SourceID == session.SourceID && previous.Fingerprint == fingerprint && compilationApproved(project, previous) {
				duplicate = true
			}
		}
		if !duplicate {
			patch.Compilation = &WikiCompilation{ID: newID(), SourceID: session.SourceID, SourceHash: session.SourceHash, RulesHash: session.RulesHash, Fingerprint: fingerprint, CompilerVersion: CompilerVersion, Model: "external:" + session.Client, Chunks: uint64(len(session.Chunks)), Sections: session.Sections, Status: "confirmed", CreatedAt: now(), Provenance: provenance}
		}
	}
	if len(patch.Entities) == 0 && len(patch.Facts) == 0 && patch.Compilation == nil {
		return ChangeSet{}, false, nil
	}
	candidate := ChangeSet{ID: newID(), ProjectID: project.ID, Source: "external", CommitPolicy: "review", BaseRevision: project.Revision, Status: ChangeSetStaged, Summary: fmt.Sprintf("Wiki · %s · %d facts", sourceTitle(project, session.SourceID), len(patch.Facts)), CreatedAt: now(), UpdatedAt: now(), Provenance: provenance,
		Operations: []ChangeOperation{{ID: newID(), TargetKind: TargetWiki, TargetID: session.SourceID, OpType: OpUpsert, Title: sourceTitle(project, session.SourceID), Summary: "Apply source-backed facts, reviewed decisions and evidence summaries", AfterValue: rawMessage(patch)}}}
	preview, err := previewChangeSet(*project, candidate, nil)
	if err != nil {
		return ChangeSet{}, false, err
	}
	candidate.Operations = preview.Operations
	return candidate, true, nil
}

// mergeWikiFact inserts or updates exactly one fact identity without changing unrelated source records.
func mergeWikiFact(facts []Fact, fact Fact) []Fact {
	index := slices.IndexFunc(facts, func(value Fact) bool { return value.ID == fact.ID })
	if index < 0 {
		return append(facts, fact)
	}
	facts[index] = fact
	return facts
}

// applyWikiChange revalidates the complete source dependency set before touching any live records.
func applyWikiChange(project *Project, operation *ChangeOperation) error {
	var patch WikiPatch
	if err := externalDecode(operation.AfterValue, &patch); err != nil {
		return err
	}
	text, chapter, exists := project.SourceText(patch.SourceID)
	if !exists || patch.SourceID != operation.TargetID || hashText(text) != patch.SourceHash || chapter != patch.Chapter || rulesHash(project) != patch.RulesHash {
		return externalFailure("SOURCES_CHANGED", "Refresh Wiki source text and rules before applying this candidate")
	}
	for _, source := range project.Sources {
		if source.ID == patch.SourceID && source.Category == "craft" {
			return externalFailure("SOURCES_CHANGED", "This source is now a writing reference")
		}
	}
	base := wikiSourceFacts(project, patch.SourceID, patch.SourceHash)
	if hashText(jsonText(base)) != patch.FactsHash {
		return externalFailure("SOURCES_CHANGED", "The source facts or author review decisions changed")
	}
	combined := append([]Fact{}, base...)
	for _, fact := range patch.Facts {
		combined = mergeWikiFact(combined, fact)
	}
	if wikiEntityHash(project, combined) != patch.EntityHash {
		return externalFailure("SOURCES_CHANGED", "Referenced entity names or aliases changed")
	}
	before := cloneProject(*project)
	for _, entity := range patch.Entities {
		if entity.ID == "" || project.ResolveEntity(entity.Name) != nil || slices.ContainsFunc(project.Entities, func(value Entity) bool { return value.ID == entity.ID }) {
			return externalFailure("INVALID_INPUT", "Wiki entity identities must be new and unambiguous")
		}
		if err := saveEntity(project, rawMessage(entity)); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, fact := range patch.Facts {
		if err := validateWikiFact(project, patch, fact, seen); err != nil {
			return err
		}
		seen[fact.ID] = true
		project.Facts = mergeWikiFact(project.Facts, fact)
	}
	if patch.Compilation != nil {
		compilation := patch.Compilation
		if compilation.ID == "" || compilation.SourceID != patch.SourceID || compilation.SourceHash != patch.SourceHash || compilation.RulesHash != patch.RulesHash || compilation.CompilerVersion != CompilerVersion || slices.ContainsFunc(project.Compilations, func(value WikiCompilation) bool { return value.ID == compilation.ID }) {
			return externalFailure("INVALID_INPUT", "Wiki compilation identity and source dependencies must match")
		}
		accepted := []Fact{}
		for _, fact := range wikiSourceFacts(project, patch.SourceID, patch.SourceHash) {
			if fact.Status == "confirmed" {
				accepted = append(accepted, fact)
			}
		}
		for _, section := range compilation.Sections {
			if _, err := parseMemory(jsonText(section), accepted); err != nil {
				return externalFailure("INVALID_INPUT", err.Error())
			}
		}
		if !compilationApproved(project, compilation) {
			return externalFailure("INVALID_INPUT", "Wiki summaries require current author-confirmed supporting facts")
		}
		project.Compilations = append(project.Compilations, *compilation)
	}
	operation.BeforeValue = rawMessage(captureChangeEffects(&before, project))
	return nil
}

// validateWikiFact validates a candidate record's source, semantic identity and exact reviewer decision.
func validateWikiFact(project *Project, patch WikiPatch, fact Fact, seen map[string]bool) error {
	if fact.ID == "" || seen[fact.ID] || fact.SourceID != patch.SourceID || fact.SourceHash != patch.SourceHash || fact.Chapter != patch.Chapter || !contains([]string{"confirmed", "rejected"}, fact.Status) {
		return externalFailure("INVALID_INPUT", "Wiki facts need unique identities, matching source versions and explicit review states")
	}
	if project.CanonicalName(fact.Entity) != fact.Entity {
		return externalFailure("INVALID_INPUT", "Use the canonical entity name in each Wiki fact")
	}
	existing := slices.IndexFunc(project.Facts, func(value Fact) bool { return value.ID == fact.ID })
	if existing >= 0 && (project.Facts[existing].SourceID != patch.SourceID || project.Facts[existing].SourceHash != patch.SourceHash) {
		return externalFailure("INVALID_INPUT", "A fact identity belongs to another source version")
	}
	if existing < 0 && fact.ID != hashText(strings.Join([]string{patch.SourceID, patch.SourceHash, fact.Entity, fact.Kind, strings.TrimSpace(fact.Claim)}, "|")) {
		return externalFailure("INVALID_INPUT", "New Wiki fact identities must match their source and claim")
	}
	text, _, _ := project.SourceText(patch.SourceID)
	candidate := Candidate{Entity: fact.Entity, Kind: fact.Kind, Claim: fact.Claim, Quote: fact.Quote, Epistemic: fact.Epistemic}
	unchangedRejection := existing >= 0 && fact.Status == "rejected" && wikiFactHash(project.Facts[existing]) == wikiFactHash(fact)
	if !unchangedRejection {
		if err := validateWikiCandidate(candidate, text); err != nil {
			return externalFailure("INVALID_INPUT", err.Error())
		}
	}
	decision := "accept"
	if fact.Status == "rejected" {
		decision = "reject"
	}
	if !slices.ContainsFunc(patch.Reviews, func(review WikiFactReview) bool {
		return review.FactID == fact.ID && review.FactHash == wikiFactHash(fact) && review.Decision == decision && strings.TrimSpace(review.Reason) != ""
	}) {
		return externalFailure("REVIEW_VERSION", "A fact changed after its external review")
	}
	return nil
}

// undoWikiChange restores only the records captured by this compound operation.
func undoWikiChange(project *Project, operation ChangeOperation) error {
	var effects []ChangeEffect
	if err := decodeJSON(operation.BeforeValue, &effects); err != nil {
		return err
	}
	for _, effect := range effects {
		if err := restoreEffect(project, effect); err != nil {
			return err
		}
	}
	return nil
}

// wikiTargetValue projects the current records addressed by one Wiki patch for desktop comparison.
func wikiTargetValue(project *Project, operation ChangeOperation) json.RawMessage {
	var patch WikiPatch
	if decodeJSON(operation.AfterValue, &patch) != nil {
		return json.RawMessage("null")
	}
	entities, facts := []Entity{}, []Fact{}
	for _, entity := range patch.Entities {
		for _, current := range project.Entities {
			if current.ID == entity.ID {
				entities = append(entities, current)
			}
		}
	}
	for _, fact := range patch.Facts {
		for _, current := range project.Facts {
			if current.ID == fact.ID {
				facts = append(facts, current)
			}
		}
	}
	var compilation *WikiCompilation
	if patch.Compilation != nil {
		for i := range project.Compilations {
			if project.Compilations[i].ID == patch.Compilation.ID {
				compilation = &project.Compilations[i]
			}
		}
	}
	return rawMessage(map[string]any{"entities": entities, "facts": facts, "compilation": compilation})
}

// writeWikiReview expands compound changes into every affected record and source-located review reason.
func writeWikiReview(text *strings.Builder, operation ChangeOperation) error {
	var effects []ChangeEffect
	if err := decodeJSON(operation.BeforeValue, &effects); err != nil {
		return err
	}
	for _, effect := range effects {
		fmt.Fprintf(text, "\nWiki %s (%s)\n", effect.Kind, effect.ID)
		before, after := effect.Before, effect.After
		if reviewNull(before) {
			before = nil
		}
		if reviewNull(after) {
			after = nil
		}
		writeReviewFields(text, before, after)
	}
	var patch WikiPatch
	if err := decodeJSON(operation.AfterValue, &patch); err != nil {
		return err
	}
	for _, review := range patch.Reviews {
		fmt.Fprintf(text, "\nFact review %s · %s\n%s\n", review.FactID, review.Decision, review.Reason)
	}
	return nil
}
