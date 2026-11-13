package core

import (
	"encoding/json"
	"errors"
	"strings"
)

// normalizeChangeSet rewrites model field names into the records the author can confirm.
func normalizeChangeSet(cs *ChangeSet) error {
	if err := normalizeMemoryOperations(cs); err != nil {
		return err
	}
	var setup, goals *ChangeOperation
	for i := range cs.Operations {
		op := &cs.Operations[i]
		if op.OpType == OpDelete || len(op.AfterValue) == 0 {
			continue
		}
		switch op.TargetKind {
		case TargetEntity:
			if err := rewriteStringField(op, "kind", canonicalEntityKind); err != nil {
				return err
			}
		case TargetPlan:
			if err := rewriteStringField(op, "level", canonicalPlanLevel); err != nil {
				return err
			}
		case TargetProjectMeta:
			setup = op
		case TargetStudioGoal:
			if goals == nil {
				goals = op
			}
		}
	}
	return foldStoryFields(setup, goals, cs)
}

// normalizeMemoryOperations canonicalizes raw memory payloads before staging or replaying an old preview.
func normalizeMemoryOperations(cs *ChangeSet) error {
	for i := range cs.Operations {
		op := &cs.Operations[i]
		if op.TargetKind != TargetMemory || op.OpType == OpDelete {
			continue
		}
		fields, err := objectFields(op)
		if err != nil {
			return err
		}
		content, exists := fields["content"]
		if exists {
			if _, valid := content.(string); !valid {
				return errors.New("Story memory content must be a string")
			}
		}
		if alias, present := fields["text"]; present {
			text, valid := alias.(string)
			if !valid {
				return errors.New("Story memory text must be a string")
			}
			canonical, _ := content.(string)
			if strings.TrimSpace(canonical) != "" && strings.TrimSpace(text) != "" && canonical != text {
				return errors.New("Story memory content and text conflict. Choose one value before confirming")
			}
			if strings.TrimSpace(canonical) == "" {
				fields["content"] = text
			}
			delete(fields, "text")
		}
		text, _ := fields["content"].(string)
		if strings.TrimSpace(text) == "" {
			return errors.New("Story memory content is required. Use the content field")
		}
		if err := writeObject(op, fields); err != nil {
			return err
		}
	}
	return nil
}

// foldStoryFields moves setup aliases onto the goal record and keeps prose in the premise.
func foldStoryFields(setup, goals *ChangeOperation, cs *ChangeSet) error {
	setupFields, err := objectFields(setup)
	if err != nil {
		return err
	}
	goalFields, err := objectFields(goals)
	if err != nil {
		return err
	}
	if goalFields == nil {
		goalFields = map[string]any{}
	}
	for alias, canonical := range map[string]string{
		"pace":               "pacing",
		"narrative_emphasis": "narrative_focus",
		"naming_preference":  "naming_style",
	} {
		if text, ok := takeString(setupFields, alias); ok && goalFields[canonical] == nil {
			goalFields[canonical] = text
		}
	}
	if text, ok := takeString(setupFields, "form"); ok {
		if mode, known := canonicalStoryMode(text); known && goalFields["mode"] == nil {
			goalFields["mode"] = mode
		}
	}
	title, _ := takeString(goalFields, "title")
	content, _ := takeString(goalFields, "content")
	prose := strings.TrimSpace(title)
	if content != "" {
		if prose != "" {
			prose += "\n\n"
		}
		prose += content
	}
	if prose != "" && setupFields != nil {
		if existing, _ := setupFields["premise"].(string); strings.TrimSpace(existing) == "" {
			setupFields["premise"] = prose
		}
	}
	if err := writeObject(setup, setupFields); err != nil {
		return err
	}
	if goals == nil {
		if len(goalFields) == 0 {
			return nil
		}
		cs.Operations = append(cs.Operations, ChangeOperation{
			ID: newID(), TargetKind: TargetStudioGoal, TargetID: "studio_goal", OpType: OpUpsert, AfterValue: rawMessage(goalFields),
		})
		return nil
	}
	if len(goalFields) == 0 {
		kept := make([]ChangeOperation, 0, len(cs.Operations))
		for _, op := range cs.Operations {
			if op.ID != goals.ID {
				kept = append(kept, op)
			}
		}
		cs.Operations = kept
		return nil
	}
	return writeObject(goals, goalFields)
}

// rewriteStringField applies one canonical value when the field is present.
func rewriteStringField(op *ChangeOperation, key string, canonical func(string) string) error {
	fields, err := objectFields(op)
	if err != nil || fields == nil {
		return err
	}
	value, ok := fields[key].(string)
	if !ok {
		return nil
	}
	fields[key] = canonical(value)
	return writeObject(op, fields)
}

// objectFields reads one proposed JSON object. A missing operation yields nil.
func objectFields(op *ChangeOperation) (map[string]any, error) {
	if op == nil || len(op.AfterValue) == 0 {
		return nil, nil
	}
	var fields map[string]any
	if err := json.Unmarshal(op.AfterValue, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		fields = map[string]any{}
	}
	return fields, nil
}

// takeString removes a string field and reports whether it had text.
func takeString(fields map[string]any, key string) (string, bool) {
	if fields == nil {
		return "", false
	}
	value, ok := fields[key].(string)
	if !ok {
		return "", false
	}
	delete(fields, key)
	value = strings.TrimSpace(value)
	return value, value != ""
}

// writeObject stores the edited proposal. A nil map leaves the operation unchanged.
func writeObject(op *ChangeOperation, fields map[string]any) error {
	if op == nil || fields == nil {
		return nil
	}
	op.AfterValue = rawMessage(fields)
	return nil
}

// canonicalEntityKind maps memory-style labels onto the entry types an author can save.
func canonicalEntityKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "rule", "world_rule", "mechanic", "ability", "power":
		return "concept"
	case "person", "protagonist", "npc":
		return "character"
	default:
		return strings.TrimSpace(kind)
	}
}

// canonicalPlanLevel maps outline-shaped labels onto the five stored plan levels.
func canonicalPlanLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "outline", "overview", "arc", "story", "series", "book", "总纲", "大纲", "全书":
		return "book"
	case "volume", "卷":
		return "volume"
	case "unit", "单元", "篇":
		return "unit"
	case "chapter", "章节", "章":
		return "chapter"
	case "scene", "场景":
		return "scene"
	default:
		return strings.TrimSpace(level)
	}
}

// afterValueID reads the record id carried by an after_value and is empty when the caller omitted it.
func afterValueID(raw json.RawMessage) string {
	var head struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &head)
	return strings.TrimSpace(head.ID)
}

// decodeOverStored decodes an after_value over a deep copy of the stored record so that fields the caller
// omitted keep their stored values, while fields it supplied, including empty strings, replace them.
// A nil stored record decodes onto the zero value, which is the behavior for a new record.
func decodeOverStored[T any](raw json.RawMessage, stored *T) (T, error) {
	var value T
	if stored != nil {
		encoded, err := json.Marshal(stored)
		if err != nil {
			return value, err
		}
		if err := json.Unmarshal(encoded, &value); err != nil {
			return value, err
		}
	}
	err := json.Unmarshal(raw, &value)
	return value, err
}

// keepClearedFields restores the fields a caller explicitly set to an empty value after a record was
// re-encoded for replay. omitempty drops them, so replaying the candidate over the stored record would bring
// the old values back and the confirmed result would differ from the preview. Null keeps meaning "omitted".
func keepClearedFields(supplied, encoded json.RawMessage) json.RawMessage {
	var input, fields map[string]json.RawMessage
	if json.Unmarshal(supplied, &input) != nil || json.Unmarshal(encoded, &fields) != nil || fields == nil {
		return encoded
	}
	restored := false
	for key, value := range input {
		if _, present := fields[key]; !present && isEmptyJSONValue(value) {
			fields[key], restored = value, true
		}
	}
	if !restored {
		return encoded
	}
	return rawMessage(fields)
}

// isEmptyJSONValue reports the non-null zero values that omitempty removes from an encoded record.
func isEmptyJSONValue(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return typed == ""
	case float64:
		return typed == 0
	case bool:
		return !typed
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	}
	return false
}

// entityRecord mirrors Entity without its strict whole-record decoder, so a partial after_value can be overlaid.
type entityRecord struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
}

// storedEntityRecord copies a stored entry into the shape used for overlaying partial updates.
func storedEntityRecord(entity Entity) *entityRecord {
	return &entityRecord{ID: entity.ID, Name: entity.Name, Kind: entity.Kind, Aliases: entity.Aliases, Description: entity.Description}
}

// canonicalStoryMode accepts only the story forms the studio can store.
func canonicalStoryMode(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "serial", "连载", "webnovel":
		return "serial", true
	case "traditional", "传统":
		return "traditional", true
	case "hybrid", "混合":
		return "hybrid", true
	case "undecided":
		return "undecided", true
	default:
		return "", false
	}
}
