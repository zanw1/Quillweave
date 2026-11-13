package core

import (
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	"reflect"
)

// externalTypeSchema matches Go JSON semantics, including nullable collections and raw target records.
func externalTypeSchema(typ reflect.Type) map[string]any {
	if typ == reflect.TypeFor[json.RawMessage]() {
		return map[string]any{}
	}
	if typ.Kind() == reflect.Pointer {
		return nullableToolSchema(externalTypeSchema(typ.Elem()))
	}
	switch typ.Kind() {
	case reflect.Struct:
		return externalInputSchema(reflect.New(typ).Elem().Interface())
	case reflect.Array:
		return map[string]any{"type": "array", "items": externalTypeSchema(typ.Elem())}
	case reflect.Slice:
		return map[string]any{"type": []string{"array", "null"}, "items": externalTypeSchema(typ.Elem())}
	case reflect.Map:
		return map[string]any{"type": []string{"object", "null"}, "additionalProperties": externalTypeSchema(typ.Elem())}
	}
	return toolTypeSchema(typ)
}

// validateCreatorInput enforces the published contract identically for CLI and MCP requests.
func validateCreatorInput(schema map[string]any, raw json.RawMessage) error {
	var declared jsonschema.Schema
	if err := json.Unmarshal(rawMessage(schema), &declared); err != nil {
		return externalFailure("SCHEMA_INVALID", err.Error())
	}
	resolved, err := declared.Resolve(nil)
	if err != nil {
		return externalFailure("SCHEMA_INVALID", err.Error())
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return externalFailure("INVALID_INPUT", err.Error())
	}
	if err := resolved.Validate(value); err != nil {
		return externalFailure("INVALID_INPUT", err.Error())
	}
	return nil
}
