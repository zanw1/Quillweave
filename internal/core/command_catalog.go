package core

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// CommandDescriptor is the discoverable contract shared by CLI and external tool adapters.
type CommandDescriptor struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	ReadOnly     bool           `json:"read_only"`
	ModelAccess  bool           `json:"model_access"`
	Modes        []string       `json:"modes"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema"`
}

// externalInputSchema expands embedded request identities using the existing record schema generator.
func externalInputSchema(value any, required ...string) map[string]any {
	typ := reflect.TypeOf(value)
	properties := map[string]any{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Anonymous {
			for key, value := range externalInputSchema(reflect.New(field.Type).Elem().Interface())["properties"].(map[string]any) {
				properties[key] = value
			}
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			properties[name] = externalTypeSchema(field.Type)
		}
	}
	return map[string]any{"type": "object", "properties": properties, "required": append([]string{}, required...), "additionalProperties": false}
}

// CommandCatalog lists every active desktop command and the typed creator operations.
func CommandCatalog() []CommandDescriptor {
	readNames := strings.Fields(`list_projects get_project knowledge_graph wiki_health story_knowledge preview_memory_maintenance search export get_runs writing_presets provider_presets list_skills load_skill writing_skills search_capabilities get_search_settings page_search_profiles get_search_profile get_search_run`)
	writeNames := strings.Fields(`studio get_flow_readiness list_conversations create_conversation get_conversation fork_conversation truncate_conversation archive_conversation update_conversation stage_changeset preview_changeset apply_changeset undo_changeset reject_changeset extract_changeset list_changesets save_studio save_plan delete_plan create_planned_chapters save_memory save_decision review_proposal get_integrations save_integrations search_web test_search set_skill_roots start_ai prepare_ai get_job cancel_ai test_model list_provider_models delete_project create_project create_example update_project new_chapter save_chapter restore_version save_entity add_source review_fact review_memory save_wiki_purpose import_legacy get_model_settings save_model_settings set_project_models apply_memory_maintenance undo_memory_maintenance begin_archive_export read_archive_chunk begin_archive_import write_archive_chunk complete_archive_import finish_archive_transfer import_archive mutate_search_settings start_search_run cancel_search_run disable_search adopt_search_result`)
	modelNames := strings.Fields(`start_ai test_model list_provider_models search_web test_search start_search_run`)
	result := []CommandDescriptor{}
	for _, name := range append(readNames, writeNames...) {
		command := CommandDescriptor{Name: name, Description: "Quillweave desktop domain operation " + name, ReadOnly: contains(readNames, name), ModelAccess: contains(modelNames, name), Modes: []string{"author"}, InputSchema: nativeCommandSchema(name), OutputSchema: CommandEnvelopeSchema()}
		if name == "list_projects" {
			command.Modes = []string{"author", "creator", "read_only"}
			command.Description = "List works with cursor-based pagination."
			command.InputSchema = externalInputSchema(ExternalReadRequest{})
		}
		result = append(result, command)
	}
	mutationFields := []string{"request_id", "writing_session_id", "session_revision"}
	wikiFields := []string{"request_id", "wiki_session_id", "session_revision"}
	typed := []struct {
		name        string
		description string
		value       any
		required    []string
		read        bool
	}{
		{"project_overview", "Read the work overview, current revision and candidate counts.", ExternalReadRequest{}, nil, true},
		{"list_chapters", "List chapters with cursor-based pagination.", ExternalReadRequest{}, nil, true},
		{"read_chapter", "Read a chapter in Unicode character windows with its text hash. Continue with next_offset while truncated.", ExternalReadRequest{}, nil, true},
		{"read_outline", "Read outlines and scene cards. Filter kind by book, volume, unit or chapter, select chapter_id for one chapter, or query by title. max_chars sets the page character budget (default 20000).", ExternalReadRequest{}, nil, true},
		{"story_memory", "Read story memories and their sources through the selected chapter boundary.", ExternalReadRequest{}, nil, true},
		{"search_story", "Search story passages, facts and memories.", ExternalReadRequest{}, nil, true},
		{"get_record", "Read a character, source, author decision, map, fact or compilation. Facts and compilations return resumable JSON text windows.", ExternalReadRequest{}, nil, true},
		{"list_candidates", "List candidate summaries with cursor-based pagination.", ExternalReadRequest{}, nil, true},
		{"get_candidate", "Read candidate differences without changing the candidate.", ExternalReadRequest{}, []string{"id"}, true},
		{"review_candidate", "Read every pending candidate change in windows, including original and proposed values. The final window supplies confirm_token. Show all changes to the author before confirmation.", ExternalReadRequest{}, []string{"id"}, true},
		{"writing_begin", "Begin a durable external-agent writing session. Confirmed chapter-outline scene cards may advance it directly to awaiting_draft. Read the returned stage material before submitting.", WritingBeginInput{}, []string{"request_id", "instruction"}, false},
		{"writing_submit_composition", "Submit scene composition and freeze the requested evidence for drafting.", WritingCompositionInput{}, append(append([]string{}, mutationFields...), "composition"), false},
		{"writing_submit_draft", "Save a new draft and obtain material for external review.", WritingDraftInput{}, append(append([]string{}, mutationFields...), "draft"), false},
		{"writing_submit_review", "Submit an external review bound to the current draft_hash and evidence_version.", WritingReviewInput{}, append(append([]string{}, mutationFields...), "draft_hash", "evidence_version", "review"), false},
		{"writing_status", "Read the current writing session and resume its saved phase.", WritingSessionRequest{}, []string{"writing_session_id"}, true},
		{"writing_material", "Read full writing material in windows. Select kind stage, draft, evidence, review or changes, then continue with next_offset and verify text_hash.", WritingMaterialRequest{}, []string{"writing_session_id"}, true},
		{"writing_refresh", "Refresh evidence while retaining the draft, then review it against the new evidence.", WritingMutation{}, mutationFields, false},
		{"writing_finish", "Atomically create an author-reviewable candidate from the reviewed writing session.", WritingMutation{}, mutationFields, false},
		{"writing_abandon", "Abandon a writing session while retaining its draft history.", WritingMutation{}, mutationFields, false},
		{"stage_changes", "Stage setting, outline or memory changes as a candidate using the current base_revision.", StageChangesInput{}, []string{"request_id", "base_revision", "summary", "operations"}, false},
		{"withdraw_candidate", "Withdraw a pending candidate created by the current client.", CandidateMutation{}, []string{"request_id", "candidate_id"}, false},
		{"prepare_revert", "Prepare a revert candidate that exposes conflicts with subsequent edits.", CandidateMutation{}, []string{"request_id", "candidate_id"}, false},
		{"confirm_candidate", "Apply a candidate with confirm_token after the author has seen every review_candidate change and explicitly approved it in this conversation. Call only with that approval.", CandidateConfirmation{}, []string{"request_id", "candidate_id", "confirm_token"}, false},
		{"resolve_candidate_conflicts", "Save author-selected keep or restore decisions for the exact reviewed recovery snapshot. Read the updated review before confirming.", CandidateConflictInput{}, []string{"request_id", "candidate_id", "expected_revision", "review_hash", "resolutions"}, false},
		{"wiki_begin", "Begin a durable external Wiki session for one chapter or world source. compile processes all source blocks. review re-examines existing facts. Read wiki_material before submitting.", WikiBeginInput{}, []string{"request_id", "source_id"}, false},
		{"wiki_material", "Read exact Wiki source, chunks, facts, review, compilation, changes or stage instructions in Unicode windows. Continue with next_offset and verify text_hash.", WikiMaterialRequest{}, []string{"wiki_session_id"}, true},
		{"wiki_status", "Resume a saved Wiki session with its phase, coverage, versions and dependency changes.", WikiSessionRequest{}, []string{"wiki_session_id"}, true},
		{"wiki_submit_extraction", "Checkpoint one source block's extracted facts. Empty arrays record complete coverage. Invalid records return diagnostics and keep the block pending for correction.", WikiExtractionInput{}, append(append([]string{}, wikiFields...), "chunk_id", "source_hash", "facts"), false},
		{"wiki_submit_review", "Review current facts with accept, reject or needs_revision, fact_hash and a reason. Bind the batch to evidence_version. Compile mode preserves existing author decisions.", WikiReviewInput{}, append(append([]string{}, wikiFields...), "evidence_version", "reviews"), false},
		{"wiki_submit_compilation", "Submit evidence-supported summary sections after fact review. Every evidence_id must identify a currently accepted fact from this source version.", WikiCompilationInput{}, append(append([]string{}, wikiFields...), "evidence_version", "sections"), false},
		{"wiki_refresh", "Refresh changed Wiki dependencies, withdraw the previous pending candidate and rebuild extraction or review material.", WikiMutation{}, wikiFields, false},
		{"wiki_finish", "Atomically stage reviewed Wiki facts, decisions and summaries as one candidate. A source with no changes completes without a candidate. Author approval applies the result through confirm_candidate.", WikiMutation{}, wikiFields, false},
		{"wiki_abandon", "End the Wiki session, retain its stage history and withdraw its pending candidate.", WikiMutation{}, wikiFields, false},
		{"list_wiki_records", "List fact, compilation or session records with source, status, text and pagination filters. Read full fact or compilation JSON windows through get_record.", WikiRecordsRequest{}, nil, true},
	}
	for _, entry := range typed {
		modes := []string{"author", "creator"}
		if entry.read {
			modes = append(modes, "read_only")
		}
		schema := externalInputSchema(entry.value, entry.required...)
		configureWikiSchema(entry.name, schema)
		if entry.name == "writing_submit_review" {
			review := schema["properties"].(map[string]any)["review"].(map[string]any)
			memory := review["properties"].(map[string]any)["memories"].(map[string]any)["items"].(map[string]any)
			memory["properties"].(map[string]any)["kind"] = map[string]any{"type": "string", "enum": writingMemoryKinds()}
		}
		if entry.name == "stage_changes" {
			schema["properties"].(map[string]any)["operations"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 50, "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"id": map[string]any{"type": "string"}, "target_kind": map[string]any{"type": "string", "enum": []string{"project_meta", "studio_goal", "decision", "entity", "memory", "plan", "location", "route"}}, "target_id": map[string]any{"type": "string"}, "op_type": map[string]any{"type": "string", "enum": []string{"upsert", "delete"}}, "title": map[string]any{"type": "string"}, "summary": map[string]any{"type": "string"}, "after_value": nativeAfterValueSchema()},
				"required":   []string{"target_kind", "op_type", "title"},
			}}
		}
		result = append(result, CommandDescriptor{Name: entry.name, Description: entry.description, ReadOnly: entry.read, Modes: modes, InputSchema: schema, OutputSchema: CommandEnvelopeSchema()})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// DescribeCommand returns one concrete operation contract without opening the database.
func DescribeCommand(name string) (CommandDescriptor, error) {
	for _, command := range CommandCatalog() {
		if command.Name == name {
			return command, nil
		}
	}
	return CommandDescriptor{}, externalFailure("NOT_FOUND", "Unknown command: "+name)
}

// Execute routes CLI and MCP calls through the same typed creator boundary and author dispatch.
func (service *ExternalService) Execute(ctx context.Context, operation string, raw json.RawMessage) (any, error) {
	if err := validateExternalContext(ctx); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	descriptor, err := DescribeCommand(operation)
	if err != nil {
		return nil, err
	}
	if !descriptor.ReadOnly {
		if err := service.writable(); err != nil {
			return nil, err
		}
	}
	if contains(descriptor.Modes, "creator") {
		if err := validateCreatorInput(descriptor.InputSchema, raw); err != nil {
			return nil, err
		}
		return service.executeCreator(ctx, operation, raw)
	}
	if !service.options.Author {
		return nil, externalFailure("CAPABILITY_UNAVAILABLE", "This operation belongs to the author CLI")
	}
	if service.options.ReadOnly {
		return nil, externalFailure("READ_ONLY_MODE", "Use the read-only creator operations for this connection")
	}
	var fields map[string]json.RawMessage
	if err := externalDecode(raw, &fields); err != nil {
		return nil, err
	}
	if service.options.ProjectID != "" {
		var supplied string
		_ = decodeJSON(fields["project_id"], &supplied)
		if _, err := service.projectID(supplied); err != nil {
			return nil, err
		}
		fields["project_id"] = rawMessage(service.options.ProjectID)
	}
	if descriptor.ModelAccess {
		service.engine.mu.Lock()
		err = service.engine.refreshModelsLocked()
		if err == nil {
			err = service.engine.refreshIntegrationsLocked()
		}
		service.engine.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	return service.engine.Dispatch(operation, rawMessage(fields))
}

// executeCreator decodes the public request into the domain type accepted by each operation.
func (service *ExternalService) executeCreator(ctx context.Context, operation string, raw json.RawMessage) (any, error) {
	switch operation {
	case "wiki_begin":
		return externalWritingPayload(raw, func(input WikiBeginInput) (any, error) { return service.BeginWiki(ctx, input) })
	case "wiki_material":
		return externalWritingPayload(raw, func(input WikiMaterialRequest) (any, error) { return service.WikiMaterial(ctx, input) })
	case "wiki_status":
		return externalWritingPayload(raw, func(input WikiSessionRequest) (any, error) { return service.WikiStatus(ctx, input) })
	case "wiki_submit_extraction":
		return externalWritingPayload(raw, func(input WikiExtractionInput) (any, error) { return service.SubmitWikiExtraction(ctx, input) })
	case "wiki_submit_review":
		return externalWritingPayload(raw, func(input WikiReviewInput) (any, error) { return service.SubmitWikiReview(ctx, input) })
	case "wiki_submit_compilation":
		return externalWritingPayload(raw, func(input WikiCompilationInput) (any, error) { return service.SubmitWikiCompilation(ctx, input) })
	case "wiki_refresh":
		return externalWritingPayload(raw, func(input WikiMutation) (any, error) { return service.RefreshWiki(ctx, input) })
	case "wiki_finish":
		return externalWritingPayload(raw, func(input WikiMutation) (any, error) { return service.FinishWiki(ctx, input) })
	case "wiki_abandon":
		return externalWritingPayload(raw, func(input WikiMutation) (any, error) { return service.AbandonWiki(ctx, input) })
	case "list_wiki_records":
		return externalWritingPayload(raw, func(input WikiRecordsRequest) (any, error) { return service.ListWikiRecords(ctx, input) })
	case "resolve_candidate_conflicts":
		return externalWritingPayload(raw, func(input CandidateConflictInput) (any, error) { return service.ResolveCandidateConflicts(ctx, input) })
	case "writing_begin":
		return externalWritingPayload(raw, func(input WritingBeginInput) (any, error) { return service.BeginWriting(ctx, input) })
	case "writing_submit_composition":
		return externalWritingPayload(raw, func(input WritingCompositionInput) (any, error) { return service.SubmitComposition(ctx, input) })
	case "writing_submit_draft":
		return externalWritingPayload(raw, func(input WritingDraftInput) (any, error) { return service.SubmitDraft(ctx, input) })
	case "writing_submit_review":
		return externalWritingPayload(raw, func(input WritingReviewInput) (any, error) { return service.SubmitReview(ctx, input) })
	case "writing_status":
		return externalWritingPayload(raw, func(input WritingSessionRequest) (any, error) { return service.WritingStatus(ctx, input) })
	case "writing_material":
		return externalWritingPayload(raw, func(input WritingMaterialRequest) (any, error) { return service.WritingMaterial(ctx, input) })
	case "writing_refresh":
		return externalWritingPayload(raw, func(input WritingMutation) (any, error) { return service.RefreshWriting(ctx, input) })
	case "writing_finish":
		return externalWritingPayload(raw, func(input WritingMutation) (any, error) { return service.FinishWriting(ctx, input) })
	case "writing_abandon":
		return externalWritingPayload(raw, func(input WritingMutation) (any, error) { return service.AbandonWriting(ctx, input) })
	case "stage_changes":
		return externalWritingPayload(raw, func(input StageChangesInput) (any, error) { return service.StageChanges(ctx, input) })
	case "withdraw_candidate":
		return externalWritingPayload(raw, func(input CandidateMutation) (any, error) { return service.WithdrawCandidate(ctx, input) })
	case "prepare_revert":
		return externalWritingPayload(raw, func(input CandidateMutation) (any, error) { return service.PrepareRevert(ctx, input) })
	case "confirm_candidate":
		return externalWritingPayload(raw, func(input CandidateConfirmation) (any, error) { return service.ConfirmCandidate(ctx, input) })
	default:
		return externalWritingPayload(raw, func(input ExternalReadRequest) (any, error) { return service.Read(ctx, operation, input) })
	}
}
