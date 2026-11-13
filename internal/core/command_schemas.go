package core

// nativeCommandSchema documents typed desktop requests while preserving their established validation rules.
func nativeCommandSchema(name string) map[string]any {
	if name == "start_ai" || name == "prepare_ai" {
		return externalInputSchema(AIRequest{}, "chapter_id", "action", "instruction")
	}
	properties := map[string]any{
		"project_id": map[string]any{"type": "string", "description": "Required unless the CLI connection is bound with --project"},
		"revision":   map[string]any{"type": "integer", "minimum": 0, "description": "The current saved project or conversation revision"},
	}
	required := []string{}
	if contains([]string{"get_job", "cancel_ai"}, name) {
		properties["job_id"] = map[string]any{"type": "string"}
		required = append(required, "job_id")
	}
	if contains([]string{"preview_changeset", "apply_changeset", "undo_changeset", "reject_changeset"}, name) {
		properties["changeset_id"] = map[string]any{"type": "string"}
		properties["review_token"] = map[string]any{"type": "string"}
		required = append(required, "changeset_id")
	}
	if contains([]string{"create_project", "update_project"}, name) {
		for _, field := range []string{"title", "genre", "premise", "style", "outline"} {
			properties[field] = map[string]any{"type": "string"}
		}
		required = []string{"title", "genre", "premise"}
		if name == "update_project" {
			required = append(required, "style", "outline", "revision")
		}
	}
	if name == "save_chapter" || name == "new_chapter" {
		for _, field := range []string{"chapter_id", "title", "body", "synopsis", "status"} {
			properties[field] = map[string]any{"type": "string"}
		}
		properties["target_words"] = map[string]any{"type": []string{"integer", "null"}, "minimum": 0}
		required = []string{"revision", "title"}
		if name == "save_chapter" {
			required = append(required, "chapter_id", "body", "synopsis", "status")
		}
	}
	return map[string]any{"type": "object", "description": "Existing desktop Dispatch request. Operation-specific records and optional fields are validated by the shared domain handler.", "properties": properties, "required": required, "additionalProperties": true}
}
