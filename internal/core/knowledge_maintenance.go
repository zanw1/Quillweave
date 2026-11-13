package core

import (
	"errors"
	"strings"
)

// KnowledgeMaintenanceEntry describes a reversible status change or a question requiring interpretation.
type KnowledgeMaintenanceEntry struct {
	RecordKind   string `json:"record_kind"`
	ID           string `json:"id"`
	Entity       string `json:"entity"`
	Title        string `json:"title"`
	Chapter      uint32 `json:"chapter"`
	SourceID     string `json:"source_id"`
	Reason       string `json:"reason"`
	Safe         bool   `json:"safe"`
	BeforeStatus string `json:"before_status"`
	Fingerprint  string `json:"fingerprint"`
}

// KnowledgeMaintenance preserves only the affected record identities and their original review states.
type KnowledgeMaintenance struct {
	ID        string                      `json:"id"`
	CreatedAt string                      `json:"created_at"`
	UndoneAt  string                      `json:"undone_at,omitempty"`
	Entries   []KnowledgeMaintenanceEntry `json:"entries"`
}

// KnowledgeMaintenancePreview binds deterministic maintenance findings to the inspected project revision.
type KnowledgeMaintenancePreview struct {
	Revision     uint64                      `json:"revision"`
	Digest       string                      `json:"digest"`
	PendingCount int                         `json:"pending_count"`
	ArchiveCount int                         `json:"archive_count"`
	ReviewCount  int                         `json:"review_count"`
	Entries      []KnowledgeMaintenanceEntry `json:"entries"`
}

// validateKnowledgeMaintenance bounds imported receipts and permits restoration only to candidate states.
func validateKnowledgeMaintenance(receipts []KnowledgeMaintenance) error {
	if len(receipts) > 100 {
		return errors.New("Too many knowledge maintenance receipts")
	}
	seen, total := map[string]bool{}, 0
	for _, receipt := range receipts {
		if receipt.ID == "" || seen[receipt.ID] || len(receipt.Entries) == 0 {
			return errors.New("Invalid knowledge maintenance receipt")
		}
		seen[receipt.ID] = true
		total += len(receipt.Entries)
		if total > 100000 {
			return errors.New("Knowledge maintenance history exceeds its record limit")
		}
		for _, entry := range receipt.Entries {
			validStatus := entry.RecordKind == "fact" && entry.BeforeStatus == "pending" || entry.RecordKind == "memory" && entry.BeforeStatus == "proposed"
			if !entry.Safe || !validStatus || entry.ID == "" || entry.Fingerprint == "" || !contains([]string{"missing_source", "stale_source", "quote_missing", "duplicate"}, entry.Reason) {
				return errors.New("A maintenance receipt contains an unsupported restoration")
			}
		}
	}
	return nil
}

// knowledgeFactFingerprint excludes review bookkeeping while protecting the complete evidence record.
func knowledgeFactFingerprint(fact Fact) string {
	fact.Status, fact.CreatedAt = "", ""
	return hashText(jsonText(fact))
}

// knowledgeMemoryFingerprint protects memory content, chronology and provenance during undo.
func knowledgeMemoryFingerprint(memory StoryMemory) string {
	memory.Status, memory.UpdatedAt = "", ""
	return hashText(jsonText(memory))
}

// knowledgeSourceIssue distinguishes a changed source from a legacy record without revision binding.
func knowledgeSourceIssue(sources map[string]knowledgeSource, sourceID, sourceHash, quote string, required bool) (string, bool) {
	if sourceID == "" && !required {
		return "", false
	}
	source, exists := sources[sourceID]
	if !exists {
		return "missing_source", true
	}
	if sourceHash != "" && sourceHash != source.Hash {
		return "stale_source", true
	}
	if strings.TrimSpace(quote) == "" && required || quote != "" && !strings.Contains(source.Text, quote) {
		return "quote_missing", true
	}
	if sourceHash == "" {
		return "unbound_source", false
	}
	return "", false
}

// knowledgeMaintenancePreview identifies obsolete and exact repeated candidates without approving interpretations.
func knowledgeMaintenancePreview(project *Project) KnowledgeMaintenancePreview {
	preview := KnowledgeMaintenancePreview{Revision: project.Revision, Entries: []KnowledgeMaintenanceEntry{}}
	sources := knowledgeSources(project)
	facts, memories, slots := map[string]bool{}, map[string]bool{}, map[string]string{}
	for _, fact := range project.Facts {
		if fact.Status == "confirmed" && knowledgeSourceCurrent(sources, fact.SourceID, fact.SourceHash, fact.Quote, true) {
			copy := fact
			copy.ID, copy.Model = "", ""
			facts[knowledgeFactFingerprint(copy)] = true
		}
	}
	for _, memory := range project.Studio.Memories {
		if memory.Status == "confirmed" && memoryCurrent(project, memory) {
			copy := memory
			copy.ID, copy.SessionID = "", ""
			memories[knowledgeMemoryFingerprint(copy)] = true
			slot := jsonText([]any{memory.Kind, project.CanonicalName(memory.Entity), memory.Title, memory.Chapter, uniqueSorted(memory.KnownBy)})
			slots[slot] = knowledgeTextKey(memory.Content + "|" + memory.State)
		}
	}
	for _, fact := range project.Facts {
		if fact.Status != "pending" {
			continue
		}
		preview.PendingCount++
		reason, safe := knowledgeSourceIssue(sources, fact.SourceID, fact.SourceHash, fact.Quote, true)
		copy := fact
		copy.ID, copy.Model = "", ""
		key := knowledgeFactFingerprint(copy)
		if reason == "" && facts[key] {
			reason, safe = "duplicate", true
		}
		facts[key] = true
		if reason != "" {
			preview.Entries = append(preview.Entries, KnowledgeMaintenanceEntry{RecordKind: "fact", ID: fact.ID, Entity: fact.Entity, Title: fact.Claim, Chapter: fact.Chapter, SourceID: fact.SourceID, Reason: reason, Safe: safe, BeforeStatus: fact.Status, Fingerprint: knowledgeFactFingerprint(fact)})
		}
	}
	for _, memory := range project.Studio.Memories {
		if memory.Status != "proposed" {
			continue
		}
		preview.PendingCount++
		required := memory.SourceID != "" || contains([]string{"fact", "inference"}, memory.Kind)
		reason, safe := knowledgeSourceIssue(sources, memory.SourceID, memory.SourceHash, memory.Quote, required)
		copy := memory
		copy.ID, copy.SessionID = "", ""
		key := knowledgeMemoryFingerprint(copy)
		slot := jsonText([]any{memory.Kind, project.CanonicalName(memory.Entity), memory.Title, memory.Chapter, uniqueSorted(memory.KnownBy)})
		if reason == "" && memories[key] {
			reason, safe = "duplicate", true
		} else if old, found := slots[slot]; reason == "" && found && old != knowledgeTextKey(memory.Content+"|"+memory.State) {
			reason = "slot_conflict"
		}
		memories[key] = true
		if reason != "" {
			preview.Entries = append(preview.Entries, KnowledgeMaintenanceEntry{RecordKind: "memory", ID: memory.ID, Entity: memory.Entity, Title: memory.Title, Chapter: memory.Chapter, SourceID: memory.SourceID, Reason: reason, Safe: safe, BeforeStatus: memory.Status, Fingerprint: knowledgeMemoryFingerprint(memory)})
		}
	}
	for _, entry := range preview.Entries {
		if entry.Safe {
			preview.ArchiveCount++
		} else {
			preview.ReviewCount++
		}
	}
	preview.Digest = hashText(jsonText([]any{project.ID, preview.Revision, preview.Entries}))
	return preview
}

// applyKnowledgeMaintenance archives the exact previewed candidates inside the caller's project transaction.
func applyKnowledgeMaintenance(project *Project, digest string) error {
	preview := knowledgeMaintenancePreview(project)
	if digest == "" || digest != preview.Digest {
		return errors.New("Knowledge maintenance preview changed. Refresh it before applying")
	}
	if preview.ArchiveCount == 0 {
		return errors.New("No obsolete or duplicate candidates to archive")
	}
	if len(project.Studio.KnowledgeMaintenance) >= 100 {
		return errors.New("Knowledge maintenance receipt limit reached")
	}
	receipt := KnowledgeMaintenance{ID: newID(), CreatedAt: now(), Entries: []KnowledgeMaintenanceEntry{}}
	for _, entry := range preview.Entries {
		if entry.Safe {
			setKnowledgeRecordStatus(project, entry.RecordKind, entry.ID, "rejected")
			receipt.Entries = append(receipt.Entries, entry)
		}
	}
	project.Studio.KnowledgeMaintenance = append(project.Studio.KnowledgeMaintenance, receipt)
	return nil
}

// knowledgeRecordState checks record content before a receipt may restore its earlier review status.
func knowledgeRecordState(project *Project, kind, id string) (string, string) {
	if kind == "fact" {
		for _, fact := range project.Facts {
			if fact.ID == id {
				return fact.Status, knowledgeFactFingerprint(fact)
			}
		}
	}
	if kind == "memory" {
		for _, memory := range project.Studio.Memories {
			if memory.ID == id {
				return memory.Status, knowledgeMemoryFingerprint(memory)
			}
		}
	}
	return "", ""
}

// setKnowledgeRecordStatus changes review bookkeeping while retaining original evidence and identifiers.
func setKnowledgeRecordStatus(project *Project, kind, id, status string) {
	if kind == "fact" {
		for i := range project.Facts {
			if project.Facts[i].ID == id {
				project.Facts[i].Status = status
				return
			}
		}
	}
	if kind == "memory" {
		if index := memoryIndex(project, id); index >= 0 {
			project.Studio.Memories[index].Status = status
		}
	}
}

// undoKnowledgeMaintenance validates the full receipt before restoring any record, preserving later edits.
func undoKnowledgeMaintenance(project *Project, receiptID string) error {
	index := -1
	for i, receipt := range project.Studio.KnowledgeMaintenance {
		if receipt.ID == receiptID {
			index = i
			break
		}
	}
	if index < 0 || project.Studio.KnowledgeMaintenance[index].UndoneAt != "" {
		return errors.New("Knowledge maintenance receipt is unavailable")
	}
	receipt := &project.Studio.KnowledgeMaintenance[index]
	for _, entry := range receipt.Entries {
		status, fingerprint := knowledgeRecordState(project, entry.RecordKind, entry.ID)
		if !entry.Safe || !contains([]string{"pending", "proposed"}, entry.BeforeStatus) || status != "rejected" || fingerprint != entry.Fingerprint {
			return errors.New("A maintained record changed. Review it before undoing this receipt")
		}
	}
	for _, entry := range receipt.Entries {
		setKnowledgeRecordStatus(project, entry.RecordKind, entry.ID, entry.BeforeStatus)
	}
	receipt.UndoneAt = now()
	return nil
}
