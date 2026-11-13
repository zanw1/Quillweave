package core

import (
	"errors"
	"reflect"
	"strings"
)

// memoryIndex locates an owned memory without accepting identifiers from other works.
func memoryIndex(project *Project, id string) int {
	for i, memory := range project.Studio.Memories {
		if id != "" && memory.ID == id {
			return i
		}
	}
	return -1
}

// memoryMeaning excludes bookkeeping while comparing every narrative and provenance field.
func memoryMeaning(memory StoryMemory) StoryMemory {
	memory.ID, memory.Status, memory.UpdatedAt, memory.SessionID, memory.Supersedes = "", "", "", "", ""
	names := []string{}
	for _, name := range memory.KnownBy {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	memory.KnownBy = uniqueSorted(names)
	return memory
}

// validateMemorySource checks accepted evidence while leaving retirement of old evidence possible.
func validateMemorySource(project *Project, memory *StoryMemory) error {
	memory.Content = strings.TrimSpace(memory.Content)
	if memory.SourceID != "" {
		source, chapter, ok := project.SourceText(memory.SourceID)
		if !ok {
			return errors.New("Memory source not found")
		}
		if memory.Quote != "" && !strings.Contains(source, memory.Quote) {
			return errors.New("Memory quote cannot be located in the source")
		}
		if memory.SourceHash != "" && memory.SourceHash != hashText(source) {
			return errors.New("Memory source changed; review the current passage")
		}
		memory.SourceHash = hashText(source)
		memory.Chapter = max(memory.Chapter, chapter)
	} else {
		memory.SourceHash = ""
	}
	if contains([]string{"fact", "inference"}, memory.Kind) && (memory.SourceID == "" || memory.Quote == "") {
		return errors.New("Source facts and inferences require a source and verbatim quote")
	}
	return nil
}

// upsertMemory versions confirmed meaning changes and preserves the stored evidence when retiring a record.
func upsertMemory(project *Project, memory StoryMemory) error {
	if memory.Status == "" {
		memory.Status = "confirmed"
	}
	index := memoryIndex(project, memory.ID)
	replaceAt := -1
	state := project.Studio
	state.Memories = append([]StoryMemory{}, state.Memories...)
	if memory.Status == "rejected" {
		if index < 0 {
			return errors.New("Select an existing memory to reject")
		}
		retired := state.Memories[index]
		retired.Status, retired.UpdatedAt = "rejected", now()
		state.Memories[index] = retired
	} else {
		if err := validateMemorySource(project, &memory); err != nil {
			return err
		}
		if index >= 0 {
			old := state.Memories[index]
			if contains([]string{"superseded", "rejected"}, old.Status) {
				return errors.New("This memory version is archived. Edit the current version or create a new memory")
			}
			if old.Status == "confirmed" && memory.Status != "confirmed" {
				return errors.New("Reject the confirmed memory instead of turning it into a proposal")
			}
			if old.Status == "confirmed" && !reflect.DeepEqual(memoryMeaning(old), memoryMeaning(memory)) {
				memory.Supersedes, memory.ID = old.ID, newID()
			} else if old.Status == "proposed" && memory.Status == "confirmed" && old.Supersedes != "" {
				memory.Supersedes, replaceAt = old.Supersedes, index
			} else {
				memory.Supersedes = old.Supersedes
				memory.UpdatedAt = now()
				state.Memories[index] = memory
				if err := validateStudio(&state); err != nil {
					return err
				}
				project.Studio = state
				return nil
			}
		}
		if memory.Supersedes != "" {
			target := memoryIndex(project, memory.Supersedes)
			if target < 0 || state.Memories[target].Status != "confirmed" || memory.ID == memory.Supersedes {
				return errors.New("The memory update target changed. Review the current version")
			}
			if memory.Status == "confirmed" {
				old := state.Memories[target]
				old.Status = "superseded"
				if memory.Chapter > old.Chapter && (old.ThroughChapter == 0 || old.ThroughChapter >= memory.Chapter) {
					old.ThroughChapter = memory.Chapter - 1
				}
				state.Memories[target] = old
			}
		}
		if memory.ID == "" {
			memory.ID = newID()
		}
		memory.UpdatedAt = now()
		if replaceAt >= 0 {
			state.Memories[replaceAt] = memory
		} else {
			state.Memories = append(state.Memories, memory)
		}
	}
	if err := validateStudio(&state); err != nil {
		return err
	}
	project.Studio = state
	return nil
}

// validateMemoryLinks rejects broken or cyclic version chains in imported and edited state.
func validateMemoryLinks(memories []StoryMemory) error {
	byID := map[string]StoryMemory{}
	for _, memory := range memories {
		byID[memory.ID] = memory
	}
	done := map[string]bool{}
	for _, memory := range memories {
		path := []string{}
		seen := map[string]bool{}
		id := memory.ID
		for id != "" && !done[id] {
			item, exists := byID[id]
			if !exists || seen[id] {
				return errors.New("Memory versions contain a missing predecessor or cycle")
			}
			seen[id] = true
			path = append(path, id)
			id = item.Supersedes
		}
		for _, id := range path {
			done[id] = true
		}
	}
	return nil
}

// memorySuccessors identifies effective replacement boundaries, including same-chapter corrections.
func memorySuccessors(memories []StoryMemory) map[string]uint32 {
	result := map[string]uint32{}
	for _, memory := range memories {
		if memory.Supersedes == "" || memory.Status == "proposed" {
			continue
		}
		previous, exists := result[memory.Supersedes]
		if !exists || memory.Chapter < previous {
			result[memory.Supersedes] = memory.Chapter
		}
	}
	return result
}

// memoryVisibleAt retains historical intervals while excluding interpretations revoked by a legacy rebuild.
func memoryVisibleAt(memory StoryMemory, through uint32, successors map[string]uint32) bool {
	if memory.Chapter > through || (memory.ThroughChapter > 0 && memory.ThroughChapter < through) {
		return false
	}
	if memory.Status == "confirmed" {
		return true
	}
	if memory.Status != "superseded" {
		return false
	}
	// A whole-work retirement revokes the interpretation, rather than dating a story event.
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(memory.State)), "superseded_by_") {
		return false
	}
	if start, exists := successors[memory.ID]; exists {
		return through < start
	}
	return memory.ThroughChapter > 0
}

// memoryUpdateCandidates suggests only one identifiable attribute, never every fact about a character.
func memoryUpdateCandidates(project *Project, next StoryMemory) []StoryMemory {
	result := []StoryMemory{}
	for _, old := range project.Studio.Memories {
		if old.Status != "confirmed" || old.Kind != next.Kind || lowerText(strings.TrimSpace(old.Entity)) != lowerText(strings.TrimSpace(next.Entity)) {
			continue
		}
		sameTitle := strings.TrimSpace(next.Title) != "" && lowerText(old.Title) == lowerText(next.Title)
		life := next.Kind == "character" && contains([]string{"alive", "dead"}, next.State) && contains([]string{"alive", "dead"}, old.State)
		if life || (sameTitle && contains([]string{"rule", "setting", "character", "relationship", "knowledge", "clue", "promise"}, next.Kind)) {
			result = append(result, old)
		}
	}
	return result
}

// acceptMemoryProposal resolves the author-reviewed target and records the resulting version on the task.
func acceptMemoryProposal(project *Project, proposal *StudioProposal, job Job, args *arguments) error {
	if proposal.Memory == nil {
		return errors.New("Missing proposed memory")
	}
	value := *proposal.Memory
	target := value.Supersedes
	if raw, specified := args.fields["supersedes_id"]; specified {
		if err := decodeJSON(raw, &target); err != nil {
			return errors.New("Select a valid memory update target")
		}
	} else if target == "" {
		if index := memoryIndex(project, value.ID); index >= 0 {
			target = value.ID
		} else if candidates := memoryUpdateCandidates(project, value); len(candidates) == 1 {
			target = candidates[0].ID
		}
	}
	if target != "" {
		index := memoryIndex(project, target)
		if index < 0 || project.Studio.Memories[index].Status != "confirmed" || project.Studio.Memories[index].Kind != value.Kind || lowerText(strings.TrimSpace(project.Studio.Memories[index].Entity)) != lowerText(strings.TrimSpace(value.Entity)) {
			return errors.New("The memory update target changed. Review the current version")
		}
	}
	value.ID, value.Supersedes, value.Status, value.SessionID = newID(), target, "confirmed", job.SessionID
	if err := upsertMemory(project, value); err != nil {
		return err
	}
	saved := project.Studio.Memories[len(project.Studio.Memories)-1]
	proposal.Memory = &saved
	return nil
}
