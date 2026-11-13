package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
)

// decodeToolRecord rejects unknown fields and malformed JSON before decoding model-authored records.
func decodeToolRecord(text string, destination any) error {
	var value any
	if err := decodeJSON([]byte(text), &value); err != nil {
		return err
	}
	if _, ok := value.(map[string]any); !ok {
		return errors.New("Expected one JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
}

// recordToolSchema derives allowed JSON fields and types from the same records used by the apply path.
func recordToolSchema(record any, required ...string) map[string]any {
	schema := toolTypeSchema(reflect.TypeOf(record))
	schema["required"] = append([]string{}, required...)
	return schema
}

// toolTypeSchema covers the scalar, array and record types used by writing tools without open objects.
func toolTypeSchema(typ reflect.Type) map[string]any {
	if typ.Kind() == reflect.Pointer {
		return nullableToolSchema(toolTypeSchema(typ.Elem()))
	}
	switch typ.Kind() {
	case reflect.Struct:
		properties := map[string]any{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if field.PkgPath == "" && name != "" && name != "-" {
				properties[name] = toolTypeSchema(field.Type)
			}
		}
		return map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": toolTypeSchema(typ.Elem())}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer", "minimum": 0}
	default:
		return map[string]any{"type": "string"}
	}
}

// nullableToolSchema represents an omitted optional value as null in strict function calls.
func nullableToolSchema(schema map[string]any) map[string]any {
	if contains(toolSchemaStrings(schema["type"]), "null") {
		return schema
	}
	if kind, ok := schema["type"].(string); ok {
		copy := map[string]any{}
		for key, value := range schema {
			copy[key] = value
		}
		copy["type"] = []string{kind, "null"}
		if values, exists := copy["enum"]; exists {
			copy["enum"] = append(toolSchemaValues(values), nil)
		}
		return copy
	}
	return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
}

// strictToolSchema closes every object and requires every property while retaining optional null values.
func strictToolSchema(schema map[string]any) map[string]any {
	var copy map[string]any
	_ = decodeJSON(rawMessage(schema), &copy)
	strictToolSchemaObject(copy)
	return copy
}

// strictToolSchemaObject recursively applies the provider's strict-schema rules to a private schema copy.
func strictToolSchemaObject(schema map[string]any) {
	if properties, ok := schema["properties"].(map[string]any); ok {
		required := toolSchemaStrings(schema["required"])
		keys := []string{}
		for key, raw := range properties {
			child := raw.(map[string]any)
			strictToolSchemaObject(child)
			if !contains(required, key) {
				properties[key] = nullableToolSchema(child)
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		schema["required"], schema["additionalProperties"] = keys, false
	}
	if items, ok := schema["items"].(map[string]any); ok {
		strictToolSchemaObject(items)
	}
	for _, raw := range toolSchemaValues(schema["anyOf"]) {
		if child, ok := raw.(map[string]any); ok {
			strictToolSchemaObject(child)
		}
	}
}

// nativeTargetValueSchema supplies the exact record contract for each supported ChangeSet target.
func nativeTargetValueSchema(kind ChangeTargetKind) map[string]any {
	switch kind {
	case TargetProjectMeta:
		return recordToolSchema(projectMetaPatch{})
	case TargetStudioGoal:
		return recordToolSchema(studioGoalPatch{})
	case TargetDecision:
		return recordToolSchema(StoryDecision{}, "title", "content")
	case TargetEntity:
		return recordToolSchema(Entity{}, "name")
	case TargetMemory:
		return recordToolSchema(StoryMemory{}, "kind", "title", "content")
	case TargetPlan:
		schema := recordToolSchema(StoryPlan{}, "level", "title")
		schema["properties"].(map[string]any)["scene_cards"] = map[string]any{"type": "array", "maxItems": 12, "items": writingSceneSchema()}
		return schema
	case TargetChapter:
		return recordToolSchema(chapterProsePatch{}, "body")
	case TargetLocation:
		return recordToolSchema(WorldLocation{}, "name")
	case TargetRoute:
		return recordToolSchema(WorldRoute{}, "from_id", "to_id", "travel_hours")
	default:
		return nil
	}
}

// nativeChangeTargetKinds keeps schema assembly stable across requests and provider cache prefixes.
func nativeChangeTargetKinds() []ChangeTargetKind {
	return []ChangeTargetKind{TargetProjectMeta, TargetStudioGoal, TargetDecision, TargetEntity, TargetMemory, TargetPlan, TargetChapter, TargetLocation, TargetRoute}
}

// nativeAfterValueSchema shares field declarations while the host enforces each target's precise record contract.
func nativeAfterValueSchema() map[string]any {
	properties := map[string]any{}
	for _, kind := range nativeChangeTargetKinds() {
		for key, field := range nativeTargetValueSchema(kind)["properties"].(map[string]any) {
			if _, exists := properties[key]; !exists {
				properties[key] = field
			}
		}
	}
	for _, key := range []string{"source_hash", "status", "session_id", "updated_at", "created_at"} {
		delete(properties, key)
	}
	return map[string]any{
		"type": "object", "properties": properties, "additionalProperties": false,
		"description": "Only fill fields for target_kind. plan needs level,title; entity and location need name; route needs from_id,to_id,travel_hours; memory needs kind,title,content; chapter needs body; decision needs title,content. Null means omitted. Omitted fields of an existing record keep their values. Other target fields are rejected by the host.",
	}
}

// validateAgentOperations checks typed payloads for native and text-compatible tool entry points.
// Null optional fields are removed only after every operation passes, preserving record defaults.
func validateAgentOperations(step agentStepEnvelope) error {
	groups := [][]ChangeOperation{step.Operations}
	if step.ChangeSet != nil {
		groups = append(groups, step.ChangeSet.Operations)
	}
	cleaned := make([][]ChangeOperation, len(groups))
	for i, operations := range groups {
		var err error
		cleaned[i], err = validateTypedToolOperations(operations)
		if err != nil {
			return err
		}
	}
	for i, operations := range groups {
		copy(operations, cleaned[i])
	}
	return nil
}

// validateTypedToolOperations validates original field names, documented legacy aliases and normalized types.
func validateTypedToolOperations(operations []ChangeOperation) ([]ChangeOperation, error) {
	if len(operations) > 50 {
		return nil, errors.New("Use at most 50 operations per tool call")
	}
	cleaned := append([]ChangeOperation{}, operations...)
	for i, op := range cleaned {
		schema := nativeTargetValueSchema(op.TargetKind)
		if schema == nil {
			return nil, fmt.Errorf("Operation %d has an unknown target_kind %q", i+1, op.TargetKind)
		}
		if op.OpType == OpDelete {
			if strings.TrimSpace(op.TargetID) == "" {
				return nil, fmt.Errorf("Operation %d needs target_id for deletion", i+1)
			}
			if raw := strings.TrimSpace(string(op.AfterValue)); raw != "" && raw != "null" {
				return nil, fmt.Errorf("Operation %d deletion accepts only an omitted or null after_value", i+1)
			}
			continue
		}
		if op.OpType != OpUpsert && (op.TargetKind != TargetChapter || !contains([]string{string(OpAppendProse), string(OpReplaceProse)}, string(op.OpType))) {
			return nil, fmt.Errorf("Operation %d has an invalid op_type for %s", i+1, op.TargetKind)
		}
		var fields map[string]any
		if err := decodeJSON(op.AfterValue, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("Operation %d after_value must be one record object", i+1)
		}
		declaredFields := nativeAfterValueSchema()["properties"].(map[string]any)
		for key, value := range fields {
			_, declared := declaredFields[key]
			if value == nil && declared && !contains(toolSchemaStrings(schema["required"]), key) {
				delete(fields, key)
			}
		}
		if err := validateToolFieldNames(fields, schema, legacyToolFields(op.TargetKind), "after_value"); err != nil {
			return nil, fmt.Errorf("Operation %d %w", i+1, err)
		}
		cleaned[i].AfterValue = rawMessage(fields)
	}
	normalized := ChangeSet{Operations: append([]ChangeOperation{}, cleaned...)}
	if err := normalizeChangeSet(&normalized); err != nil {
		return nil, err
	}
	for i, op := range normalized.Operations {
		if op.OpType == OpDelete {
			continue
		}
		var fields any
		if err := decodeJSON(op.AfterValue, &fields); err != nil {
			return nil, err
		}
		if err := validateToolSchemaValue(fields, nativeTargetValueSchema(op.TargetKind), "after_value"); err != nil {
			return nil, fmt.Errorf("Operation %d (%s) %w", i+1, op.TargetKind, err)
		}
	}
	return cleaned, nil
}

// legacyToolFields retains aliases that the existing normalization path can convert losslessly.
func legacyToolFields(kind ChangeTargetKind) []string {
	switch kind {
	case TargetMemory:
		return []string{"text"}
	case TargetProjectMeta:
		return []string{"pace", "narrative_emphasis", "naming_preference", "form"}
	case TargetStudioGoal:
		return []string{"title", "content"}
	default:
		return nil
	}
}

// validateToolFieldNames gives a repairable field-specific error before normalization can discard a typo.
func validateToolFieldNames(fields map[string]any, schema map[string]any, aliases []string, path string) error {
	properties, _ := schema["properties"].(map[string]any)
	for key := range fields {
		if _, allowed := properties[key]; !allowed && !contains(aliases, key) {
			keys := []string{}
			for name := range properties {
				keys = append(keys, name)
			}
			sort.Strings(keys)
			return fmt.Errorf("%s.%s is unknown; allowed fields are %s", path, key, strings.Join(keys, ", "))
		}
	}
	return nil
}

// validateNativeToolArguments enforces declared types locally even when a provider ignores its schema.
func validateNativeToolArguments(name, arguments string) error {
	var value map[string]any
	if err := decodeJSON([]byte(arguments), &value); err != nil || value == nil {
		return errors.New("Function arguments must be one complete JSON object")
	}
	action := strings.TrimPrefix(name, "quillweave_")
	legacy := name == nativeAgentFunction
	if legacy {
		action, _ = value["tool"].(string)
		delete(value, "tool")
	}
	for _, spec := range nativeActionSpecs() {
		if spec.Tool != action {
			continue
		}
		schema := nativeActionSchema(spec)
		if legacy && contains([]string{"propose", "propose_changes"}, action) {
			properties := schema["properties"].(map[string]any)
			alias, primary := "proposals", "proposal"
			if action == "propose_changes" {
				alias, primary = "changeset", "operations"
			}
			properties[alias] = nativeFieldSchemas()[alias]
			schema["required"] = []string{}
			if value[alias] == nil && value[primary] == nil {
				return fmt.Errorf("Provide %s or %s", primary, alias)
			}
		}
		if err := validateToolSchemaValue(value, schema, "arguments"); err != nil {
			return err
		}
		if len(spec.AnyOf) > 0 && !toolAlternativePresent(value, spec.AnyOf) {
			return errors.New("Provide at least one of this tool's selector fields with a non-null value")
		}
		var step agentStepEnvelope
		if err := decodeJSON([]byte(arguments), &step); err != nil {
			return err
		}
		return validateAgentOperations(step)
	}
	return nil
}

// toolAlternativePresent checks selector requirements after strict-mode optional null values are received.
func toolAlternativePresent(value map[string]any, alternatives []any) bool {
	for _, raw := range alternatives {
		branch, _ := raw.(map[string]any)
		present := true
		for _, key := range toolSchemaStrings(branch["required"]) {
			if value[key] == nil {
				present = false
			}
		}
		if present {
			return true
		}
	}
	return false
}

// validateToolSchemaValue evaluates the closed schema subset emitted by this package on the host.
func validateToolSchemaValue(value any, schema map[string]any, path string) error {
	if branches := toolSchemaValues(schema["anyOf"]); len(branches) > 0 {
		matched := false
		for _, raw := range branches {
			if branch, ok := raw.(map[string]any); ok && validateToolSchemaValue(value, branch, path) == nil {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s does not match its allowed record type", path)
		}
	}
	if kinds := toolSchemaStrings(schema["type"]); len(kinds) > 0 {
		matched := false
		for _, kind := range kinds {
			matched = matched || toolSchemaTypeMatches(value, kind)
		}
		if !matched {
			return fmt.Errorf("%s must be %s", path, strings.Join(kinds, " or "))
		}
	}
	if allowed := toolSchemaValues(schema["enum"]); len(allowed) > 0 {
		matched := false
		for _, item := range allowed {
			matched = matched || reflect.DeepEqual(item, value)
		}
		if !matched {
			return fmt.Errorf("%s is outside the allowed values", path)
		}
	}
	if fields, ok := value.(map[string]any); ok {
		return validateToolObject(fields, schema, path)
	}
	if values, ok := value.([]any); ok {
		if err := validateToolNumber(float64(len(values)), schema, "minItems", "maxItems", path); err != nil {
			return err
		}
		items, _ := schema["items"].(map[string]any)
		for i, item := range values {
			if err := validateToolSchemaValue(item, items, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	if number, ok := value.(json.Number); ok {
		numeric, err := number.Float64()
		if err != nil || math.IsInf(numeric, 0) || math.IsNaN(numeric) {
			return fmt.Errorf("%s must be a finite number", path)
		}
		return validateToolNumber(numeric, schema, "minimum", "maximum", path)
	}
	return nil
}

// validateToolObject checks required and allowed fields recursively, accepting null for optional selectors.
func validateToolObject(fields map[string]any, schema map[string]any, path string) error {
	required := toolSchemaStrings(schema["required"])
	for _, key := range required {
		if _, exists := fields[key]; !exists {
			return fmt.Errorf("%s.%s is required", path, key)
		}
	}
	if closed, exists := schema["additionalProperties"].(bool); exists && !closed {
		if err := validateToolFieldNames(fields, schema, nil, path); err != nil {
			return err
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for key, value := range fields {
		if value == nil && !contains(required, key) {
			continue
		}
		if child, exists := properties[key].(map[string]any); exists {
			if err := validateToolSchemaValue(value, child, path+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateToolNumber enforces the numeric and array-size bounds present in emitted schemas.
func validateToolNumber(number float64, schema map[string]any, minKey, maxKey, path string) error {
	for _, bound := range []struct {
		key     string
		minimum bool
	}{{minKey, true}, {maxKey, false}} {
		raw, exists := schema[bound.key]
		if !exists {
			continue
		}
		limit, _ := json.Number(fmt.Sprint(raw)).Float64()
		if (bound.minimum && number < limit) || (!bound.minimum && number > limit) {
			return fmt.Errorf("%s exceeds its %s constraint", path, bound.key)
		}
	}
	return nil
}

// toolSchemaTypeMatches checks JSON values without coercing strings into numbers or dropping array items.
func toolSchemaTypeMatches(value any, kind string) bool {
	switch kind {
	case "null":
		return value == nil
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number", "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		parsed, err := number.Float64()
		return err == nil && (kind == "number" || math.Trunc(parsed) == parsed)
	default:
		return false
	}
}

// toolSchemaValues handles native schema slices and slices decoded from JSON copies uniformly.
func toolSchemaValues(value any) []any {
	switch values := value.(type) {
	case []any:
		return values
	case []string:
		result := make([]any, len(values))
		for i, item := range values {
			result[i] = item
		}
		return result
	default:
		return nil
	}
}

// toolSchemaStrings reads a scalar type name or a list of type names and required property names.
func toolSchemaStrings(value any) []string {
	if text, ok := value.(string); ok {
		return []string{text}
	}
	result := []string{}
	for _, raw := range toolSchemaValues(value) {
		if text, ok := raw.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
