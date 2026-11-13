package core

import "strings"

// WritingSourceChange identifies a changed writing dependency and its known content versions.
type WritingSourceChange struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Title        string `json:"title,omitempty"`
	ExpectedHash string `json:"expected_hash,omitempty"`
	CurrentHash  string `json:"current_hash,omitempty"`
	Deleted      bool   `json:"deleted,omitempty"`
}

// externalWritingSourceChanges compares all dependencies instead of stopping at the aggregate revision marker.
func externalWritingSourceChanges(project *Project, session *ExternalWritingSession) []WritingSourceChange {
	changes := []WritingSourceChange{}
	if session.BaseRevision != project.Revision {
		changes = append(changes, WritingSourceChange{ID: project.ID, Kind: "project_revision", Title: project.Title})
	}
	if session.NewChapter {
		for _, chapter := range project.Chapters {
			if chapter.ID == session.Target.ID || chapter.Number == session.Target.Number {
				changes = append(changes, WritingSourceChange{ID: chapter.ID, Kind: "chapter_target", Title: chapter.Title})
			}
		}
	}
	prompt := session.Run.SourcePrompt
	if prompt == nil {
		return changes
	}
	seen := map[string]bool{}
	// compareSource groups multiple excerpts of the same source into one inspectable dependency change.
	compareSource := func(id, expected, title string) {
		if id == "" || expected == "" || seen[id] {
			return
		}
		seen[id] = true
		text, _, exists := project.SourceText(id)
		current := ""
		if exists {
			current = hashText(text)
		}
		if !exists || current != expected {
			changes = append(changes, WritingSourceChange{ID: id, Kind: "source", Title: title, ExpectedHash: expected, CurrentHash: current, Deleted: !exists})
		}
	}
	compareSource(prompt.SourceID, prompt.SourceHash, session.Target.Title)
	for _, item := range prompt.Context {
		compareSource(item.SourceID, item.SourceHash, item.Title)
		if !strings.HasPrefix(item.ID, "P:") {
			continue
		}
		found, matches := false, false
		currentHash := ""
		for _, plan := range project.Studio.Plans {
			if item.ID == "P:"+plan.ID {
				found, matches, currentHash = true, strings.HasSuffix(item.Text, jsonText(plan)), hashText(jsonText(plan))
				break
			}
		}
		if !matches {
			changes = append(changes, WritingSourceChange{ID: item.ID, Kind: "plan", Title: item.Title, ExpectedHash: hashText(item.Text), CurrentHash: currentHash, Deleted: !found})
		}
	}
	return changes
}

// externalSessionSources reports bounded change details and preserves strict project snapshot validation.
func externalSessionSources(project *Project, session *ExternalWritingSession) error {
	changes := externalWritingSourceChanges(project, session)
	if len(changes) == 0 {
		return nil
	}
	return externalFailure("SOURCES_CHANGED", "Writing sources changed; refresh this session", map[string]any{"changes": changes[:min(50, len(changes))], "change_count": len(changes), "current_revision": project.Revision})
}
