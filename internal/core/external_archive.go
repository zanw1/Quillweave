package core

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
)

// rekeyChangeEffects preserves valid conversation provenance inside archived before-and-after snapshots.
func rekeyChangeEffects(effects []ChangeEffect, sessions map[string]string) {
	for index := range effects {
		for _, value := range []*json.RawMessage{&effects[index].Before, &effects[index].After} {
			var record map[string]json.RawMessage
			if len(*value) == 0 || decodeJSON(*value, &record) != nil || record == nil {
				continue
			}
			var old string
			if raw, exists := record["session_id"]; exists && decodeJSON(raw, &old) == nil {
				record["session_id"] = rawMessage(sessions[old])
				*value = rawMessage(record)
			}
		}
	}
}

// validateArchivedWiki verifies source coverage, fact locations and review identities before publication.
func validateArchivedWiki(session *ExternalWikiSession) error {
	if session.ID == "" || session.ProjectID == "" || session.SourceID == "" || session.Revision == 0 || !contains([]string{"compile", "review"}, session.Mode) || !contains([]string{"awaiting_extraction", "awaiting_review", "awaiting_compilation", "ready_to_stage", "staged", "completed", "abandoned", "stale"}, session.Phase) || session.SourceText == "" || hashText(session.SourceText) != session.SourceHash {
		return errors.New("Invalid archived Wiki session")
	}
	characters := []rune(session.SourceText)
	expected := 0
	for _, chunk := range session.Chunks {
		if chunk.Offset != expected || chunk.End <= chunk.Offset || chunk.End > len(characters) || len(chunk.Candidates) > 100 {
			return errors.New("Invalid archived Wiki source coverage")
		}
		for _, candidate := range chunk.Candidates {
			if err := validateWikiCandidate(candidate, string(characters[chunk.Offset:chunk.End])); err != nil {
				return err
			}
		}
		expected = chunk.End
		if expected < len(characters) {
			expected -= 150
		}
	}
	if expected != len(characters) {
		return errors.New("Incomplete archived Wiki source coverage")
	}
	seen := map[string]bool{}
	for _, fact := range session.Facts {
		if fact.ID == "" || seen[fact.ID] || fact.SourceID != session.SourceID || fact.SourceHash != session.SourceHash {
			return errors.New("Invalid archived Wiki fact identity")
		}
		seen[fact.ID] = true
		original := slices.ContainsFunc(session.BaseFacts, func(value Fact) bool { return wikiFactHash(value) == wikiFactHash(fact) })
		if !original {
			if err := validateWikiCandidate(Candidate{Entity: fact.Entity, Kind: fact.Kind, Claim: fact.Claim, Quote: fact.Quote, Epistemic: fact.Epistemic}, session.SourceText); err != nil {
				return err
			}
		}
	}
	seen = map[string]bool{}
	for _, review := range session.Reviews {
		index := slices.IndexFunc(session.Facts, func(fact Fact) bool { return fact.ID == review.FactID })
		if index < 0 || seen[review.FactID] || review.FactHash != wikiFactHash(session.Facts[index]) || !contains([]string{"accept", "reject", "needs_revision"}, review.Decision) || review.Reason == "" {
			return errors.New("Invalid archived Wiki review")
		}
		seen[review.FactID] = true
	}
	for _, section := range session.Sections {
		if _, err := parseMemory(jsonText(section), wikiAcceptedFacts(session)); err != nil {
			return err
		}
	}
	return nil
}

// restoreArchivedWiki isolates imported session identities and requires refresh before resumed mutations.
func restoreArchivedWiki(session *ExternalWikiSession, projectID, id string) {
	session.ID, session.ProjectID = id, projectID
	session.Revision++
	session.UpdatedAt = now()
	if !contains([]string{"completed", "abandoned"}, session.Phase) {
		session.Phase = "stale"
	}
}

// rekeyWikiRecordProvenance updates one optional external session reference during import.
func rekeyWikiRecordProvenance(provenance *ExternalProvenance, identities map[string]string) {
	if provenance != nil && provenance.WikiSessionID != "" {
		if next, exists := identities[provenance.WikiSessionID]; exists {
			provenance.WikiSessionID = next
		}
	}
}

// rekeyWikiRecords updates public fact and summary attribution in a newly imported work.
func rekeyWikiRecords(project *Project, identities map[string]string) {
	for i := range project.Facts {
		rekeyWikiRecordProvenance(project.Facts[i].Provenance, identities)
	}
	for i := range project.Compilations {
		rekeyWikiRecordProvenance(project.Compilations[i].Provenance, identities)
	}
}

// rekeyWikiProvenance isolates Wiki references inside candidate metadata, payloads and captured effects.
func rekeyWikiProvenance(candidate *ChangeSet, identities map[string]string) {
	rekeyWikiRecordProvenance(candidate.Provenance, identities)
	for i := range candidate.Operations {
		op := &candidate.Operations[i]
		if op.TargetKind == TargetWiki {
			var patch WikiPatch
			if decodeJSON(op.AfterValue, &patch) == nil {
				for j := range patch.Facts {
					rekeyWikiRecordProvenance(patch.Facts[j].Provenance, identities)
				}
				if patch.Compilation != nil {
					rekeyWikiRecordProvenance(patch.Compilation.Provenance, identities)
				}
				op.AfterValue = rawMessage(patch)
			}
		}
	}
	for i := range candidate.Operations {
		op := &candidate.Operations[i]
		if op.TargetKind == TargetWiki {
			var effects []ChangeEffect
			if decodeJSON(op.BeforeValue, &effects) == nil {
				rekeyWikiEffects(effects, identities)
				op.BeforeValue = rawMessage(effects)
			}
		}
		if op.OpType == "restore" {
			var effects []ChangeEffect
			if decodeJSON(op.AfterValue, &effects) == nil {
				rekeyWikiEffects(effects, identities)
				op.AfterValue = rawMessage(effects)
			}
		}
	}
	rekeyWikiEffects(candidate.Effects, identities)
	rekeyWikiEffects(candidate.RestoreEffects, identities)
}

// rekeyProjectWiki handles the in-memory legacy import using the streaming import's identity rules.
func rekeyProjectWiki(project *Project) {
	identities := map[string]string{}
	for _, session := range project.WikiSessions {
		identities[session.ID] = newID()
	}
	for i := range project.WikiSessions {
		restoreArchivedWiki(&project.WikiSessions[i], project.ID, identities[project.WikiSessions[i].ID])
	}
	rekeyWikiRecords(project, identities)
	for i := range project.Studio.ChangeSets {
		rekeyWikiProvenance(&project.Studio.ChangeSets[i], identities)
	}
	for i := range project.ArchiveJobs {
		for j := range project.ArchiveJobs[i].ChangeSets {
			rekeyWikiProvenance(&project.ArchiveJobs[i].ChangeSets[j], identities)
		}
	}
}

// importProjectWiki publishes validated in-memory Wiki checkpoints with the imported project.
func importProjectWiki(db documentDB, project *Project) error {
	for i := range project.WikiSessions {
		session := &project.WikiSessions[i]
		if err := validateArchivedWiki(session); err != nil {
			return err
		}
		if session.ProjectID != project.ID {
			return externalFailure("PROJECT_SCOPE", "Archived Wiki belongs to another work")
		}
		if err := saveWikiSession(db, session); err != nil {
			return err
		}
	}
	return nil
}

// stageArchivedWiki validates bounded session records before storing them in the import spool.
func (stage *stagedArchive) stageArchivedWiki(input *archiveDecoder) error {
	if stage.wikiIDs == nil {
		stage.wikiIDs = map[string]string{}
	}
	return readArchiveArray[ExternalWikiSession](input, func(session ExternalWikiSession) error {
		if err := validateArchivedWiki(&session); err != nil {
			return err
		}
		if stage.wikiIDs[session.ID] != "" || len(stage.wikiIDs) >= 20000 {
			return errors.New("Duplicate or oversized archived Wiki history")
		}
		stage.wikiIDs[session.ID] = newID()
		return stage.stageRecord("wiki", "", session)
	})
}

// insertArchivedWiki restores a spooled checkpoint only into the newly imported project.
func (stage *stagedArchive) insertArchivedWiki(db *sql.Conn, raw json.RawMessage, oldProjectID string) error {
	var session ExternalWikiSession
	if err := decodeJSON(raw, &session); err != nil {
		return err
	}
	if session.ProjectID != oldProjectID {
		return externalFailure("PROJECT_SCOPE", "Archived Wiki belongs to another work")
	}
	restoreArchivedWiki(&session, stage.project.ID, stage.wikiIDs[session.ID])
	return saveWikiSession(db, &session)
}

// validateArchivedWriting checks durable session bounds before restoring any project data.
func validateArchivedWriting(session *ExternalWritingSession) error {
	if session.ID == "" || session.ProjectID == "" || session.Target.ID == "" || session.Revision == 0 || !contains([]string{"awaiting_composition", "awaiting_draft", "awaiting_review", "awaiting_revision", "reviewed", "staged", "abandoned", "stale"}, session.Phase) {
		return externalFailure("INVALID_INPUT", "Invalid archived external writing session")
	}
	if session.Input.RevisionLimit == nil || *session.Input.RevisionLimit < 0 || session.Input.ContextChars < 4000 || session.Input.ContextChars > 500000 {
		return externalFailure("INVALID_INPUT", "Invalid archived writing limits")
	}
	return nil
}

// restoreArchivedWriting rekeys a session and requires renewed evidence before another creative step.
func restoreArchivedWriting(session *ExternalWritingSession, projectID, id string) {
	session.ID, session.ProjectID, session.Input.ProjectID = id, projectID, projectID
	session.Input.RequestID = ""
	session.Revision++
	session.UpdatedAt = now()
	if !contains([]string{"staged", "abandoned"}, session.Phase) {
		session.Phase = "stale"
	}
}

// rekeyWritingProvenance retains archived external attribution while isolating restored session identities.
func rekeyWritingProvenance(candidate *ChangeSet, identities map[string]string) {
	if candidate.Provenance == nil {
		return
	}
	copy := *candidate.Provenance
	if copy.WritingSessionID != "" {
		copy.WritingSessionID = identities[copy.WritingSessionID]
	}
	candidate.Provenance = &copy
}

// rekeyProjectWriting handles the small in-memory import path with the streaming import's same rules.
func rekeyProjectWriting(project *Project) {
	identities := map[string]string{}
	for _, session := range project.WritingSessions {
		identities[session.ID] = newID()
	}
	for i := range project.WritingSessions {
		restoreArchivedWriting(&project.WritingSessions[i], project.ID, identities[project.WritingSessions[i].ID])
	}
	for i := range project.Studio.ChangeSets {
		rekeyWritingProvenance(&project.Studio.ChangeSets[i], identities)
	}
	for i := range project.ArchiveJobs {
		for j := range project.ArchiveJobs[i].ChangeSets {
			rekeyWritingProvenance(&project.ArchiveJobs[i].ChangeSets[j], identities)
		}
	}
}

// importProjectWriting stores backup sessions in the project's original publication transaction.
func importProjectWriting(db documentDB, project *Project) error {
	for _, session := range project.WritingSessions {
		if err := validateArchivedWriting(&session); err != nil {
			return err
		}
		if session.ProjectID != project.ID {
			return externalFailure("PROJECT_SCOPE", "Archived writing belongs to another project")
		}
		if _, err := db.ExecContext(background(), "INSERT INTO writing_sessions(id,project_id,revision,document) VALUES(?,?,?,?)", session.ID, project.ID, session.Revision, jsonText(session)); err != nil {
			return err
		}
	}
	return nil
}

// stageArchivedWriting streams external sessions to the existing import spool and reserves new identities.
func (stage *stagedArchive) stageArchivedWriting(input *archiveDecoder) error {
	if stage.writingIDs == nil {
		stage.writingIDs = map[string]string{}
	}
	return readArchiveArray[ExternalWritingSession](input, func(session ExternalWritingSession) error {
		if err := validateArchivedWriting(&session); err != nil {
			return err
		}
		if stage.writingIDs[session.ID] != "" {
			return externalFailure("INVALID_INPUT", "Duplicate archived writing session")
		}
		stage.writingIDs[session.ID] = newID()
		return stage.stageRecord("writing", "", session)
	})
}

// insertArchivedWriting restores a spooled session only into its newly imported project.
func (stage *stagedArchive) insertArchivedWriting(db *sql.Conn, raw json.RawMessage, oldProjectID string) error {
	var session ExternalWritingSession
	if err := decodeJSON(raw, &session); err != nil {
		return err
	}
	if session.ProjectID != oldProjectID {
		return externalFailure("PROJECT_SCOPE", "Archived writing belongs to another project")
	}
	restoreArchivedWriting(&session, stage.project.ID, stage.writingIDs[session.ID])
	_, err := db.ExecContext(background(), "INSERT INTO writing_sessions(id,project_id,revision,document) VALUES(?,?,?,?)", session.ID, session.ProjectID, session.Revision, jsonText(session))
	return err
}

// rekeyWikiEffects updates record-level attribution consistently in immutable recovery snapshots.
func rekeyWikiEffects(effects []ChangeEffect, identities map[string]string) {

	for i := range effects {
		for _, value := range []*json.RawMessage{&effects[i].Before, &effects[i].After} {
			var record map[string]json.RawMessage
			if decodeJSON(*value, &record) != nil || record == nil {
				continue
			}
			var provenance *ExternalProvenance
			if raw, exists := record["provenance"]; exists && decodeJSON(raw, &provenance) == nil && provenance != nil {
				rekeyWikiRecordProvenance(provenance, identities)
				record["provenance"] = rawMessage(provenance)
				*value = rawMessage(record)
			}
		}
	}
}
