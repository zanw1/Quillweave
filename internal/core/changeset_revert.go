package core

import (
	"bytes"
	"encoding/json"
	"sort"
)

// ChangeEffect captures one actual field or normalized record changed by an applied candidate.
type ChangeEffect struct {
	Kind          string          `json:"kind"`
	ID            string          `json:"id"`
	Before        json.RawMessage `json:"before"`
	After         json.RawMessage `json:"after"`
	Resolution    string          `json:"resolution,omitempty"`
	ForceConflict bool            `json:"force_conflict,omitempty"`
}

// RevertConflict exposes later edits for an explicit keep-or-restore author decision.
type RevertConflict struct {
	Kind       string          `json:"kind"`
	ID         string          `json:"id"`
	Current    json.RawMessage `json:"current"`
	Expected   json.RawMessage `json:"expected"`
	Restore    json.RawMessage `json:"restore"`
	Resolution string          `json:"resolution,omitempty"`
}

// projectEffectValues projects independently restorable records and metadata fields.
func projectEffectValues(project *Project) map[string]json.RawMessage {
	metadata, parts := splitProject(project)
	values := map[string]json.RawMessage{}
	for _, part := range parts {
		if part.kind != "changeset" {
			values[part.kind+"/"+part.id] = json.RawMessage(part.document)
		}
	}
	var fields map[string]json.RawMessage
	_ = decodeJSON(rawMessage(metadata), &fields)
	for _, key := range []string{"title", "genre", "premise", "style", "outline", "wiki_purpose", "writing_style", "default_chapter_words", "narrative_pov", "model_overrides"} {
		values["project_field/"+key] = fields[key]
	}
	_ = decodeJSON(rawMessage(metadata.Studio), &fields)
	for key, value := range fields {
		if !contains([]string{"plans", "memories", "decisions", "changesets", "locations", "routes"}, key) {
			values["studio_field/"+key] = value
		}
	}
	return values
}

// sameEffectValue compares canonical JSON values while treating absent and null records equally.
func sameEffectValue(left, right json.RawMessage) bool {
	if len(left) == 0 {
		left = json.RawMessage("null")
	}
	if len(right) == 0 {
		right = json.RawMessage("null")
	}
	var a, b any
	if decodeJSON(left, &a) != nil || decodeJSON(right, &b) != nil {
		return bytes.Equal(left, right)
	}
	for _, value := range []any{a, b} {
		if record, ok := value.(map[string]any); ok {
			if _, chapter := record["body"]; chapter {
				delete(record, "versions")
			}
		}
	}
	return jsonText(a) == jsonText(b)
}

// captureChangeEffects records actual dependent-reference edits as well as explicit operation targets.
func captureChangeEffects(before, after *Project) []ChangeEffect {
	previous, current := projectEffectValues(before), projectEffectValues(after)
	keys := map[string]bool{}
	for key := range previous {
		keys[key] = true
	}
	for key := range current {
		keys[key] = true
	}
	ordered := []string{}
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	effects := []ChangeEffect{}
	for _, key := range ordered {
		if sameEffectValue(previous[key], current[key]) {
			continue
		}
		kind, id := splitEffectKey(key)
		effect := ChangeEffect{Kind: kind, ID: id, Before: previous[key], After: current[key]}
		if kind == "chapter" {
			if len(effect.After) > 0 {
				effect.Before = compactChapterEffect(effect.Before)
			}
			effect.After = compactChapterEffect(effect.After)
		}
		effects = append(effects, effect)
	}
	return effects
}

// compactChapterEffect avoids duplicating chapter history when restoration can append to the live history.
func compactChapterEffect(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var value map[string]json.RawMessage
	if decodeJSON(raw, &value) != nil {
		return raw
	}
	value["versions"] = json.RawMessage("[]")
	return rawMessage(value)
}

// splitEffectKey separates a trusted record kind from its complete identifier.
func splitEffectKey(key string) (string, string) {
	for i, character := range key {
		if character == '/' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

// applyChangeSetToProject captures domain effects for later conflict-aware recovery.
func applyChangeSetToProject(project *Project, candidate *ChangeSet) error {
	if err := validateChangeSet(candidate); err != nil {
		return err
	}
	before := cloneProject(*project)
	if len(candidate.Operations) == 1 && candidate.Operations[0].OpType == "restore" {
		if err := applyRestoration(project, candidate); err != nil {
			return err
		}
		candidate.Status, candidate.UpdatedAt = ChangeSetApplied, now()
	} else if err := applyChangeSetOperations(project, candidate); err != nil {
		return err
	}
	candidate.Effects = captureChangeEffects(&before, project)
	return nil
}

// undoChangeSetOnProject shares existing undo behavior and supports recovery candidates themselves.
func undoChangeSetOnProject(project *Project, candidate *ChangeSet) error {
	if len(candidate.Operations) != 1 || candidate.Operations[0].OpType != "restore" {
		return undoChangeSetOperations(project, candidate)
	}
	if candidate.Status != ChangeSetApplied {
		return externalFailure("INVALID_INPUT", "Only applied candidates can be undone")
	}
	for _, effect := range candidate.Effects {
		if err := restoreEffect(project, effect); err != nil {
			return err
		}
	}
	candidate.Status, candidate.UpdatedAt = ChangeSetUndone, now()
	return validateProject(project)
}

// restoreEffectRecord replaces one record by identity while retaining all unrelated records.
func restoreEffectRecord[T any](records *[]T, effect ChangeEffect) error {
	result := []T{}
	found := false
	for _, record := range *records {
		var identity struct {
			ID string `json:"id"`
		}
		if err := decodeJSON(rawMessage(record), &identity); err != nil {
			return err
		}
		if identity.ID != effect.ID {
			result = append(result, record)
			continue
		}
		found = true
		if len(effect.Before) > 0 && string(effect.Before) != "null" {
			var restored T
			if err := decodeJSON(effect.Before, &restored); err != nil {
				return err
			}
			result = append(result, restored)
		}
	}
	if !found && len(effect.Before) > 0 && string(effect.Before) != "null" {
		var restored T
		if err := decodeJSON(effect.Before, &restored); err != nil {
			return err
		}
		result = append(result, restored)
	}
	*records = result
	return nil
}

// restoreEffect restores a single captured value and keeps chapter version history append-only.
func restoreEffect(project *Project, effect ChangeEffect) error {
	switch effect.Kind {
	case "chapter":
		current := findChapter(project, effect.ID)
		if current != nil && len(effect.Before) > 0 && string(effect.Before) != "null" {
			var previous Chapter
			if err := decodeJSON(effect.Before, &previous); err != nil {
				return err
			}
			current.Number = previous.Number
			current.SetContent(previous.Title, previous.Body, previous.Synopsis, previous.Status, previous.TargetWords)
			return nil
		}
		if err := restoreEffectRecord(&project.Chapters, effect); err != nil {
			return err
		}
		sort.SliceStable(project.Chapters, func(i, j int) bool { return project.Chapters[i].Number < project.Chapters[j].Number })
		return nil
	case "entity":
		return restoreEffectRecord(&project.Entities, effect)
	case "fact":
		return restoreEffectRecord(&project.Facts, effect)
	case "source":
		return restoreEffectRecord(&project.Sources, effect)
	case "compilation":
		return restoreEffectRecord(&project.Compilations, effect)
	case "memory":
		return restoreEffectRecord(&project.Studio.Memories, effect)
	case "plan":
		return restoreEffectRecord(&project.Studio.Plans, effect)
	case "decision":
		return restoreEffectRecord(&project.Studio.Decisions, effect)
	case "location":
		return restoreEffectRecord(&project.Studio.Locations, effect)
	case "route":
		return restoreEffectRecord(&project.Studio.Routes, effect)
	case "project_field":
		return restoreJSONField(project, effect)
	case "studio_field":
		return restoreJSONField(&project.Studio, effect)
	}
	return externalFailure("INVALID_INPUT", "Unsupported recovery effect")
}

// restoreJSONField preserves every other metadata property when reverting one field.
func restoreJSONField(destination any, effect ChangeEffect) error {
	var fields map[string]json.RawMessage
	if err := decodeJSON(rawMessage(destination), &fields); err != nil {
		return err
	}
	if len(effect.Before) == 0 {
		delete(fields, effect.ID)
	} else {
		fields[effect.ID] = effect.Before
	}
	return decodeJSON(rawMessage(fields), destination)
}

// applyRestoration computes a reversible preview while recording every unresolved later edit.
func applyRestoration(project *Project, candidate *ChangeSet) error {
	if candidate.RevertsChangeSetID == "" || len(candidate.RestoreEffects) == 0 {
		return externalFailure("INVALID_INPUT", "A recovery candidate requires its original effects")
	}
	var effects []ChangeEffect
	if err := decodeJSON(candidate.Operations[0].AfterValue, &effects); err != nil {
		return err
	}
	if len(effects) != len(candidate.RestoreEffects) {
		return externalFailure("INVALID_INPUT", "Recovery effects must retain their original identities")
	}
	values := projectEffectValues(project)
	candidate.RevertConflicts = nil
	for i, effect := range effects {
		original := candidate.RestoreEffects[i]
		original.Resolution, effect.Resolution = "", ""
		if jsonText(original) != jsonText(effect) {
			return externalFailure("INVALID_INPUT", "Only keep or restore decisions may edit a recovery effect")
		}
		effect = effects[i]
		if !contains([]string{"", "keep", "restore"}, effect.Resolution) {
			return externalFailure("INVALID_INPUT", "Choose keep or restore for a recovery conflict")
		}
		current := values[effect.Kind+"/"+effect.ID]
		if effect.ForceConflict || !sameEffectValue(current, effect.After) {
			candidate.RevertConflicts = append(candidate.RevertConflicts, RevertConflict{Kind: effect.Kind, ID: effect.ID, Current: current, Expected: effect.After, Restore: effect.Before, Resolution: effect.Resolution})
		}
		if effect.Resolution != "keep" {
			if err := restoreEffect(project, effect); err != nil {
				return err
			}
		}
	}
	return validateProject(project)
}

// validateRevertConfirmation enforces explicit conflict decisions on the exact preview being applied.
func validateRevertConfirmation(candidate *ChangeSet) error {
	for _, conflict := range candidate.RevertConflicts {
		if conflict.Resolution == "" {
			return externalFailure("REVERT_CONFLICT", "Choose keep or restore for every recovery conflict before confirming")
		}
	}
	return nil
}
