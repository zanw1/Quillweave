package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// deletedRecord retains ordering as well as content for an exact deletion undo.
type deletedRecord[T any] struct {
	Position int `json:"position"`
	Value    T   `json:"value"`
}

type deletedChapterSnapshot struct {
	Chapter        Chapter                          `json:"chapter"`
	Position       int                              `json:"position"`
	Facts          []deletedRecord[Fact]            `json:"facts"`
	Compilations   []deletedRecord[WikiCompilation] `json:"compilations"`
	Memories       []deletedRecord[StoryMemory]     `json:"memories"`
	LinkedMemories []StoryMemory                    `json:"linked_memories"`
	Locations      []WorldLocation                  `json:"locations"`
}

// hasChapterDeletion identifies destructive manuscript operations before applying a batch.
func hasChapterDeletion(operations []ChangeOperation) bool {
	return slices.ContainsFunc(operations, func(operation ChangeOperation) bool {
		return operation.TargetKind == TargetChapter && operation.OpType == OpDelete
	})
}

// detachRecords archives matching values without retaining invalid source links.
func detachRecords[T any](values []T, matches func(T) bool) ([]T, []deletedRecord[T]) {
	kept := make([]T, 0, len(values))
	removed := []deletedRecord[T]{}
	for i, value := range values {
		if matches(value) {
			removed = append(removed, deletedRecord[T]{Position: i, Value: value})
		} else {
			kept = append(kept, value)
		}
	}
	return kept, removed
}

// restoreRecords reinserts archived values at their original positions.
func restoreRecords[T any](values []T, records []deletedRecord[T]) []T {
	for _, record := range records {
		position := min(max(record.Position, 0), len(values))
		values = append(values, record.Value)
		copy(values[position+1:], values[position:len(values)-1])
		values[position] = record.Value
	}
	return values
}

// deleteChapter archives the manuscript and derived source records in its changeset.
func deleteChapter(project *Project, operation *ChangeOperation) error {
	index := slices.IndexFunc(project.Chapters, func(chapter Chapter) bool { return chapter.ID == operation.TargetID })
	if index < 0 {
		return errors.New("Chapter not found")
	}
	snapshot := deletedChapterSnapshot{Chapter: project.Chapters[index], Position: index}
	project.Chapters = slices.Delete(project.Chapters, index, index+1)
	project.Facts, snapshot.Facts = detachRecords(project.Facts, func(fact Fact) bool { return fact.SourceID == operation.TargetID })
	project.Compilations, snapshot.Compilations = detachRecords(project.Compilations, func(record WikiCompilation) bool { return record.SourceID == operation.TargetID })
	project.Studio.Memories, snapshot.Memories = detachRecords(project.Studio.Memories, func(memory StoryMemory) bool { return memory.SourceID == operation.TargetID })
	removedMemoryIDs := map[string]bool{}
	for _, record := range snapshot.Memories {
		removedMemoryIDs[record.Value.ID] = true
	}
	for i, memory := range project.Studio.Memories {
		if removedMemoryIDs[memory.Supersedes] {
			snapshot.LinkedMemories = append(snapshot.LinkedMemories, memory)
			project.Studio.Memories[i].Supersedes = ""
		}
	}
	for i, location := range project.Studio.Locations {
		if slices.Contains(location.ChapterIDs, operation.TargetID) {
			snapshot.Locations = append(snapshot.Locations, location)
			project.Studio.Locations[i].ChapterIDs = slices.DeleteFunc(slices.Clone(location.ChapterIDs), func(id string) bool { return id == operation.TargetID })
		}
	}
	operation.BeforeValue = rawMessage(snapshot)
	operation.AfterValue = json.RawMessage("null")
	return nil
}

// restoreDeletedChapter restores the full chapter, versions, sources and map links.
func restoreDeletedChapter(project *Project, operation ChangeOperation) error {
	var snapshot deletedChapterSnapshot
	if err := decodeJSON(operation.BeforeValue, &snapshot); err != nil {
		return err
	}
	if snapshot.Chapter.ID != operation.TargetID || snapshot.Chapter.ID == "" {
		return errors.New("The deleted chapter snapshot is incomplete")
	}
	for _, chapter := range project.Chapters {
		if chapter.ID == snapshot.Chapter.ID || chapter.Number == snapshot.Chapter.Number {
			return errors.New("A chapter already occupies the deleted chapter's identity or number")
		}
	}
	project.Chapters = restoreRecords(project.Chapters, []deletedRecord[Chapter]{{Position: snapshot.Position, Value: snapshot.Chapter}})
	project.Facts = restoreRecords(project.Facts, snapshot.Facts)
	project.Compilations = restoreRecords(project.Compilations, snapshot.Compilations)
	project.Studio.Memories = restoreRecords(project.Studio.Memories, snapshot.Memories)
	for _, previous := range snapshot.LinkedMemories {
		for i := range project.Studio.Memories {
			if project.Studio.Memories[i].ID == previous.ID {
				project.Studio.Memories[i] = previous
			}
		}
	}
	for _, previous := range snapshot.Locations {
		for i := range project.Studio.Locations {
			if project.Studio.Locations[i].ID == previous.ID {
				project.Studio.Locations[i] = previous
			}
		}
	}
	return nil
}

// chapterDeletionImpacts describes the source records included in a reversible deletion.
func chapterDeletionImpacts(project *Project, operations []ChangeOperation) []ChangeImpact {
	result := []ChangeImpact{}
	for _, operation := range operations {
		if operation.TargetKind != TargetChapter || operation.OpType != OpDelete {
			continue
		}
		var snapshot deletedChapterSnapshot
		if json.Unmarshal(operation.BeforeValue, &snapshot) != nil {
			continue
		}
		for _, item := range []struct {
			kind, english, chinese string
			count                  int
		}{
			{"version", "%d saved versions", "%d 个历史版本", len(snapshot.Chapter.Versions)},
			{"fact", "%d source evidence records", "%d 条来源证据", len(snapshot.Facts)},
			{"compilation", "%d compiled memory records", "%d 条编译记忆", len(snapshot.Compilations)},
			{"memory", "%d story memory records", "%d 条故事记忆", len(snapshot.Memories)},
			{"location", "%d map references", "%d 处地图引用", len(snapshot.Locations)},
		} {
			if item.count > 0 {
				result = append(result, ChangeImpact{Kind: item.kind, ID: operation.TargetID, Title: fmt.Sprintf(localText(project, item.english, item.chinese), item.count)})
			}
		}
	}
	return result
}
