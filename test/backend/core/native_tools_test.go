//go:build quillweave_whitebox

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// nativeFunctionFixture emits fragmented arguments and opaque native blocks for all six supported adapters.
func nativeFunctionFixture(protocol, arguments string) string {
	first, second := arguments[:len(arguments)/2], arguments[len(arguments)/2:]
	quoted := jsonText(arguments)
	var events []string
	switch protocol {
	case "openai":
		events = []string{
			`{"choices":[{"delta":{"reasoning_content":"PUBLIC_NATIVE_THOUGHT"}}]}`,
			fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-native","type":"function","function":{"name":"quillweave_step","arguments":%s}}]}}]}`, jsonText(first)),
			fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":%s}}]},"finish_reason":"tool_calls"}]}`, jsonText(second)),
			`[DONE]`,
		}
	case "responses":
		events = []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"id":"reasoning-native","type":"reasoning","summary":[],"encrypted_content":"PRIVATE_NATIVE"}}`,
			`{"type":"response.output_item.added","output_index":1,"item":{"id":"function-native","type":"function_call","call_id":"call-native","name":"quillweave_step","arguments":""}}`,
			fmt.Sprintf(`{"type":"response.function_call_arguments.delta","output_index":1,"delta":%s}`, jsonText(first)),
			fmt.Sprintf(`{"type":"response.function_call_arguments.delta","output_index":1,"delta":%s}`, jsonText(second)),
			fmt.Sprintf(`{"type":"response.function_call_arguments.done","output_index":1,"arguments":%s}`, quoted),
			`{"type":"response.completed","response":{"status":"completed"}}`,
		}
	case "anthropic":
		events = []string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"PUBLIC_NATIVE_THOUGHT"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"PRIVATE_NATIVE"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call-native","name":"quillweave_step","input":{}}}`,
			fmt.Sprintf(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":%s}}`, jsonText(first)),
			fmt.Sprintf(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":%s}}`, jsonText(second)),
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
			`{"type":"message_stop"}`,
		}
	case "gemini":
		events = []string{
			`{"event_type":"step.start","index":0,"step":{"type":"thought","summary":[]}}`,
			`{"event_type":"step.delta","index":0,"delta":{"type":"thought_signature","signature":"PRIVATE_NATIVE"}}`,
			`{"event_type":"step.start","index":1,"step":{"type":"function_call","id":"call-native","name":"quillweave_step","arguments":{}}}`,
			fmt.Sprintf(`{"event_type":"step.delta","index":1,"delta":{"type":"arguments_delta","arguments":%s}}`, jsonText(first)),
			fmt.Sprintf(`{"event_type":"step.delta","index":1,"delta":{"type":"arguments_delta","arguments":%s}}`, jsonText(second)),
			`{"event_type":"interaction.completed","interaction":{"status":"requires_action"}}`,
		}
	case "gemini_generate":
		events = []string{fmt.Sprintf(`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call-native","name":"quillweave_step","args":%s},"thoughtSignature":"PRIVATE_NATIVE"}]},"finishReason":"STOP"}]}`, arguments)}
	case "ollama":
		return fmt.Sprintf("{\"message\":{\"role\":\"assistant\",\"thinking\":\"PUBLIC_NATIVE_THOUGHT\",\"tool_calls\":[{\"function\":{\"name\":\"quillweave_step\",\"arguments\":%s}}]},\"done\":true,\"done_reason\":\"stop\"}\n", arguments)
	default:
		panic("unknown native fixture protocol")
	}
	return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
}

// assertNativeResult checks result correlation in the actual HTTP request, including vendor-specific envelopes.
func assertNativeResult(t *testing.T, protocol string, body map[string]any) {
	t.Helper()
	serialized := jsonText(body)
	marker := "PRIVATE_NATIVE"
	if protocol == "openai" || protocol == "ollama" {
		marker = "PUBLIC_NATIVE_THOUGHT"
	}
	assert(t, strings.Contains(serialized, marker) && strings.Contains(serialized, "Native plan evidence"), "Native history or the tool result was lost")
	key, idKey := "messages", "tool_call_id"
	switch protocol {
	case "responses":
		key, idKey = "input", "call_id"
	case "anthropic":
		idKey = "tool_use_id"
	case "gemini":
		key, idKey = "input", "call_id"
	case "gemini_generate":
		key, idKey = "contents", "id"
	case "ollama":
		idKey = "tool_name"
	}
	value := "call-native"
	if protocol == "ollama" {
		value = nativeAgentFunction
	}
	assert(t, strings.Contains(jsonText(body[key]), `"`+idKey+`":"`+value+`"`), "Native function output lost its correlation")
}

// TestNativeAgentProtocolsReplayAfterRestart exercises native requests, pairing, persistence, privacy and model isolation.
func TestNativeAgentProtocolsReplayAfterRestart(t *testing.T) {
	for _, protocol := range []string{"openai", "responses", "anthropic", "gemini", "gemini_generate", "ollama"} {
		t.Run(protocol, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "native.sqlite3")
			engine, err := NewEngine(path)
			assert(t, err == nil, fmt.Sprint(err))
			t.Cleanup(func() { engine.Close() })
			project := testProject()
			insertProject(t, engine, project)
			var calls atomic.Int32
			var mutex sync.Mutex
			bodies := []map[string]any{}
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := decodeJSON(readBodyForStudioTest(t, r), &body); err != nil {
					t.Error(err)
				}
				mutex.Lock()
				bodies = append(bodies, body)
				mutex.Unlock()
				if calls.Add(1) == 1 {
					fmt.Fprint(w, nativeFunctionFixture(protocol, `{"tool":"plan","text":"Native plan evidence"}`))
					return
				}
				fmt.Fprint(w, nativeStreamFixture(protocol, "The plan is ready for discussion."))
			})
			config := nativeConfig(protocol)
			config.BaseURL, config.ContextChars = server.URL, 64000
			configureModel(t, engine, config)
			session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": project.ID})
			args := map[string]any{"project_id": project.ID, "chapter_id": "", "history": []Message{}, "action": "agent", "session_id": session.ID, "instruction": "Plan the investigation"}
			first := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, first.Status == "done" && first.Calls == 2 && len(first.Steps) == 1 && first.Steps[0].Status == "done", fmt.Sprintf("Native execution failed: %s", first.Error))
			assert(t, first.Prompt.ToolChars > 0 && first.Prompt.Chars <= int(config.ContextChars), "Tool declaration missed the input budget")
			assert(t, !strings.Contains(jsonText(first), "PRIVATE_NATIVE"), "Public task exposed private native blocks")
			runs := invoke[[]json.RawMessage](t, engine, "get_runs", map[string]any{"project_id": project.ID})
			assert(t, !strings.Contains(jsonText(runs), "PRIVATE_NATIVE"), "Run listing exposed private continuation data")
			archive := invoke[map[string]string](t, engine, "export", map[string]any{"project_id": project.ID, "format": "archive"})
			assert(t, !strings.Contains(archive["content"], "PRIVATE_NATIVE") && !strings.Contains(archive["content"], "last_request_body"), "Backup exposed private native requests")
			assert(t, engine.Close() == nil, "Could not restart the isolated engine")
			engine, err = NewEngine(path)
			assert(t, err == nil, fmt.Sprint(err))
			args["instruction"] = "Continue the same plan"
			second := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, second.Status == "done", second.Error)
			config.Model = "other-native-model"
			configureModel(t, engine, config)
			third := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, third.Status == "done", third.Error)
			mutex.Lock()
			defer mutex.Unlock()
			assert(t, len(bodies) == 4 && bodies[0]["tools"] != nil, "The ordinary Agent entry point omitted native tools")
			assertNativeResult(t, protocol, bodies[1])
			assertNativeResult(t, protocol, bodies[2])
			assert(t, !strings.Contains(jsonText(bodies[3]), "PRIVATE_NATIVE"), "Signed native data crossed model identities")
			assert(t, second.Metrics[0].SharedPrefixBytes != nil, "Restart lost exact request-comparison evidence")
		})
	}
}

// TestNativeUnsupportedFallback checks one explicit retry, model-scoped caching, accounting and unrelated HTTP errors.
func TestNativeUnsupportedFallback(t *testing.T) {
	for _, unsupported := range []bool{true, false} {
		t.Run(fmt.Sprint(unsupported), func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			insertProject(t, engine, project)
			var native, compatible atomic.Int32
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["tools"] != nil {
					native.Add(1)
					w.WriteHeader(http.StatusBadRequest)
					message := "Temperature is invalid"
					if unsupported {
						message = "This model does not support tools"
					}
					fmt.Fprint(w, jsonText(map[string]any{"error": map[string]string{"message": message}}))
					return
				}
				compatible.Add(1)
				fmt.Fprint(w, streamPayload(`{"tool":"finish","text":"Compatible answer"}`))
			})
			config := nativeConfig("openai")
			config.BaseURL = server.URL
			configureModel(t, engine, config)
			args := map[string]any{"project_id": project.ID, "chapter_id": "", "history": []Message{}, "action": "agent", "instruction": "Discuss"}
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			if !unsupported {
				assert(t, job.Status == "error" && job.Calls == 1 && native.Load() == 1 && compatible.Load() == 0, "An unrelated HTTP error triggered a silent retry")
				return
			}
			assert(t, job.Status == "done" && job.Calls == 2 && job.Prompt.ToolChars == 0, "Explicit native rejection did not fall back within the shared call budget")
			job = finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, job.Status == "done" && job.Calls == 1 && native.Load() == 1 && compatible.Load() == 2, "Native incompatibility was retried for the same model")
			config.Model = "new-model"
			configureModel(t, engine, config)
			job = finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, job.Status == "done" && native.Load() == 2, "A different model inherited a stale fallback")
		})
	}
}

// TestNativeCallValidation rejects malformed payloads before any business tool can run.
func TestNativeCallValidation(t *testing.T) {
	for _, item := range []struct{ name, replacement, value string }{
		{"missing-id", `"id":"call-native",`, ""},
		{"array-arguments", "ARRAY_PLACEHOLDER", "[]"},
		{"invalid-index", `"index":0`, `"index":-1`},
	} {
		t.Run(item.name, func(t *testing.T) {
			payload := nativeFunctionFixture("openai", `{"tool":"plan","text":"safe"}`)
			if item.name == "array-arguments" {
				payload = nativeFunctionFixture("openai", "[]")
			} else {
				payload = strings.ReplaceAll(payload, item.replacement, item.value)
			}
			server := staticModel(t, payload, 0)
			config := nativeConfig("openai")
			config.BaseURL, config.nativeTools = server.URL, true
			output, err := streamModelOutput(context.Background(), config, nil, func(string) {})
			if item.name == "array-arguments" {
				assert(t, err == nil && len(output.ToolCalls) == 1 && output.ToolCalls[0].argumentError != "", "Invalid arguments were not returned as a recoverable tool failure")
			} else {
				assert(t, err != nil, "An uncorrelated native call reached the business layer")
			}
		})
	}
	config := nativeConfig("openai")
	config.nativeTools = true
	state := nativeStreamState{}
	state.initialize()
	state.calls["0"] = &NativeToolCall{ID: "duplicate", Name: nativeAgentFunction, Arguments: `{}`}
	state.calls["1"] = &NativeToolCall{ID: "duplicate", Name: nativeAgentFunction, Arguments: `{}`}
	state.order = []string{"0", "1"}
	_, _, err := state.complete(config.Protocol, "", "", "")
	assert(t, err != nil, "Duplicate call identifiers were accepted")
}

// TestNativeMultipleCallsStageOnce verifies batching, paired results and the preview-only write boundary.
func TestNativeMultipleCallsStageOnce(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	arguments := `{"tool":"propose_changes","operations":[{"target_kind":"entity","op_type":"upsert","title":"Propose Lin","after_value":{"name":"Lin","kind":"character"}}]}`
	var calls atomic.Int32
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if calls.Add(1) == 1 {
			items := []any{}
			for index := 0; index < 2; index++ {
				items = append(items, map[string]any{"index": index, "id": fmt.Sprintf("call-%d", index), "type": "function", "function": map[string]string{"name": nativeAgentFunction, "arguments": arguments}})
			}
			fmt.Fprint(w, "data: "+jsonText(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": items}, "finish_reason": "tool_calls"}}})+"\n\ndata: [DONE]\n\n")
			return
		}
		results := 0
		for _, raw := range body["messages"].([]any) {
			if raw.(map[string]any)["role"] == "tool" {
				results++
			}
		}
		if results != 2 {
			t.Error("A batched native function was missing its result")
		}
		fmt.Fprint(w, streamPayload("Review the character proposal."))
	})
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": "", "history": []Message{}, "action": "agent", "instruction": "Save Lin"}))
	assert(t, job.Status == "done" && job.Calls == 2 && len(job.ChangeSets) == 1, "Batched calls duplicated or lost their candidate: "+job.Error)
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(current.Entities) == 0 && current.Revision == 0, "Native tools bypassed author preview and confirmation")
}

// TestNativeOpaquePacketReplay keeps full Responses items and OpenRouter delta blocks unchanged on the wire.
func TestNativeOpaquePacketReplay(t *testing.T) {
	response := `{"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","id":"rs-1","summary":[],"encrypted_content":"PRIVATE_NATIVE"},{"type":"message","id":"msg-1","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"A plan","annotations":[]}]},{"type":"function_call","id":"fc-1","call_id":"call-native","name":"quillweave_step","arguments":"{\"tool\":\"plan\",\"text\":\"Native plan evidence\"}"}]}}`
	server := staticModel(t, "data: "+response+"\n\n", 0)
	config := nativeConfig("responses")
	config.BaseURL, config.nativeTools = server.URL, true
	output, err := streamModelOutput(context.Background(), config, nil, func(string) {})
	assert(t, err == nil && len(output.ProviderBlocks) == 3, "Responses lost authoritative output items")
	body, _, err := modelRequest(config, []Message{assistantModelMessage(output), nativeResultMessage(output.ToolCalls[0], "Native plan evidence")})
	assert(t, err == nil && strings.Contains(jsonText(body), `"phase":"commentary"`) && strings.Contains(jsonText(body), "PRIVATE_NATIVE"), "Responses stripped phase or private continuation data")
	assertNativeResult(t, "responses", body)
	state := nativeStreamState{}
	for _, chunk := range []string{
		`{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","index":0,"text":"first","signature":null}]}}]}`,
		`{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.encrypted","index":1,"data":"opaque+/="}]}}]}`,
	} {
		value, err := jsonObject(chunk)
		assert(t, err == nil && state.collect("openai", "", chunk, value) == nil, "OpenRouter native details failed to assemble")
	}
	_, blocks, err := state.complete("openai", "Answer", "", "")
	assert(t, err == nil && len(blocks) == 1 && strings.Contains(string(blocks[0]), `"data":"opaque+/="`), "Opaque reasoning detail sequence was changed")
}

// assertClosedToolSchema checks every nested record in the declarations actually sent to a provider.
func assertClosedToolSchema(t *testing.T, schema map[string]any, strict bool) {
	t.Helper()
	if properties, ok := schema["properties"].(map[string]any); ok {
		assert(t, schema["additionalProperties"] == false, "A tool record accepts unknown fields")
		required := toolSchemaStrings(schema["required"])
		if strict {
			assert(t, len(required) == len(properties), "Strict tool record has optional properties outside required")
		}
		for key, raw := range properties {
			if strict {
				assert(t, contains(required, key), "Strict tool property is absent from required: "+key)
			}
			assertClosedToolSchema(t, raw.(map[string]any), strict)
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		assertClosedToolSchema(t, items, strict)
	}
	for _, raw := range toolSchemaValues(schema["anyOf"]) {
		assertClosedToolSchema(t, raw.(map[string]any), strict)
	}
}

// TestNativeClosedSchemas checks strict declarations and exact target fields across all supported transports.
func TestNativeClosedSchemas(t *testing.T) {
	for _, protocol := range []string{"openai", "responses", "anthropic", "gemini", "gemini_generate", "ollama"} {
		t.Run(protocol, func(t *testing.T) {
			config := nativeConfig(protocol)
			config.nativeTools = true
			definitions := nativeToolDefinitions(config)
			assert(t, len(definitions) > 0, "Native tools were not declared")
			strict := protocol == "openai" || protocol == "responses"
			for _, raw := range definitions {
				definition := raw.(map[string]any)
				if function, ok := definition["function"].(map[string]any); ok {
					definition = function
				}
				assert(t, definition["name"] != "quillweave_propose", "Native writes have duplicate proposal interfaces")
				if strict {
					assert(t, definition["strict"] == true, "Supported strict mode was disabled")
				}
				schemaKey := "parameters"
				if protocol == "anthropic" {
					schemaKey = "input_schema"
				}
				if protocol == "gemini_generate" {
					schemaKey = "parametersJsonSchema"
				}
				assertClosedToolSchema(t, definition[schemaKey].(map[string]any), strict)
			}
			for _, spec := range []*resultToolSpec{compositionResultTool(), reviewResultTool()} {
				definition := resultToolDefinitions(config, spec)[0].(map[string]any)
				if function, ok := definition["function"].(map[string]any); ok {
					definition = function
				}
				schemaKey := "parameters"
				if protocol == "anthropic" {
					schemaKey = "input_schema"
				}
				if protocol == "gemini_generate" {
					schemaKey = "parametersJsonSchema"
				}
				assertClosedToolSchema(t, definition[schemaKey].(map[string]any), strict)
				assert(t, !strict || definition["strict"] == true, "A writing result tool did not use strict mode")
			}
			t.Logf("native declaration size %d chars", nativeToolChars(config))
		})
	}
}

// TestNativeTypedOperationFields rejects misspelled fields, cross-target fields and scalar coercion locally.
func TestNativeTypedOperationFields(t *testing.T) {
	cases := []struct {
		name  string
		kind  ChangeTargetKind
		after string
		valid bool
	}{
		{"plan", TargetPlan, `{"level":"chapter","title":"Home","choice":"Stay"}`, true},
		{"plan typo", TargetPlan, `{"level":"chapter","title":"Home","choices":"Stay"}`, false},
		{"entity", TargetEntity, `{"name":"Lin","description":"Keeps the old key"}`, true},
		{"entity typo", TargetEntity, `{"name":"Lin","descripton":"Keeps the old key"}`, false},
		{"location", TargetLocation, `{"name":"Old gate","region_id":"north"}`, true},
		{"location wrong field", TargetLocation, `{"name":"Old gate","parent_id":"north"}`, false},
		{"route", TargetRoute, `{"from_id":"a","to_id":"b","travel_hours":2}`, true},
		{"route typo", TargetRoute, `{"from_id":"a","to_id":"b","travel_time":2}`, false},
		{"route numeric string", TargetRoute, `{"from_id":"a","to_id":"b","travel_hours":"2"}`, false},
		{"chapter", TargetChapter, `{"body":"She comes home."}`, true},
		{"chapter wrong field", TargetChapter, `{"content":"She comes home."}`, false},
		{"memory", TargetMemory, `{"kind":"fact","title":"Key moved","content":"The key is in the drawer."}`, true},
		{"legacy memory alias", TargetMemory, `{"kind":"fact","title":"Key moved","text":"The key is in the drawer."}`, true},
		{"memory typo", TargetMemory, `{"kind":"fact","title":"Key moved","contnet":"The key is in the drawer."}`, false},
		{"null typo", TargetPlan, `{"level":"chapter","title":"Home","typo":null}`, false},
		{"metadata patch", TargetProjectMeta, `{"premise":"A keeper returns home."}`, true},
		{"goal patch", TargetStudioGoal, `{"narrative_focus":"Friendship","target_chapters":40}`, true},
		{"goal numeric string", TargetStudioGoal, `{"target_chapters":"40"}`, false},
		{"decision", TargetDecision, `{"title":"Promise","content":"Keep the key dry."}`, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			step := agentStepEnvelope{Operations: []ChangeOperation{{TargetKind: test.kind, OpType: OpUpsert, AfterValue: json.RawMessage(test.after)}}}
			err := validateAgentOperations(step)
			assert(t, (err == nil) == test.valid, fmt.Sprintf("Unexpected contract validation result: %v", err))
		})
	}
}

// TestNativeStrictNullDefaults retains omission semantics for strict calls and legacy location records.
func TestNativeStrictNullDefaults(t *testing.T) {
	fields := map[string]any{}
	for key := range nativeAfterValueSchema()["properties"].(map[string]any) {
		fields[key] = nil
	}
	fields["name"] = "Old gate"
	step := agentStepEnvelope{Operations: []ChangeOperation{{TargetKind: TargetLocation, OpType: OpUpsert, AfterValue: rawMessage(fields)}}}
	assert(t, validateAgentOperations(step) == nil, "Null unused fields from the shared declaration were rejected")
	var location WorldLocation
	err := decodeJSON(step.Operations[0].AfterValue, &location)
	assert(t, err == nil && location.Kind == "place" && location.EntityIDs != nil, "Strict nulls destroyed legacy location defaults")
	for _, arguments := range []string{
		`{"query":"gate","unexpected":1}`,
		`{"query":12}`,
		`{"query":null}`,
	} {
		assert(t, validateNativeToolArguments("quillweave_recall", arguments) != nil, "Invalid native arguments reached the executor")
	}
	assert(t, validateNativeToolArguments("quillweave_writer", `{"query":"Return home","mode":null,"plan_id":null}`) == nil, "Strict optional null selectors were rejected")
	assert(t, validateNativeToolArguments("quillweave_read_chapters", `{"chapters":null,"chapter_ids":null}`) != nil, "Null selectors satisfied the required chapter selection")
	assert(t, validateNativeToolArguments("quillweave_propose_changes", `{"operations":[{"target_kind":"entity","op_type":"delete","target_id":"old","after_value":null}]}`) == nil, "Deletion incorrectly requires a replacement record")
	assert(t, validateNativeToolArguments(nativeAgentFunction, `{"tool":"propose_changes","changeset":{"summary":"A place","operations":[{"target_kind":"location","op_type":"upsert","after_value":{"name":"Old gate"}}]}}`) == nil, "The historical native changeset envelope became unreadable")
}

// TestNativeUnknownFieldsRecover verifies the ordinary default-window Agent entry point for all transports.
func TestNativeUnknownFieldsRecover(t *testing.T) {
	for _, protocol := range []string{"openai", "responses", "anthropic", "gemini", "gemini_generate", "ollama"} {
		t.Run(protocol, func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			insertProject(t, engine, project)
			var calls atomic.Int32
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				body := string(readBodyForStudioTest(t, r))
				switch index := calls.Add(1); index {
				case 1:
					fixture := nativeFunctionFixture(protocol, `{"tool":"plan","text":"Draft plan","unexpected":"FIELD_TYPO"}`)
					fmt.Fprint(w, strings.ReplaceAll(fixture, "call-native", "call-invalid"))
				case 2:
					assert(t, strings.Contains(body, "unexpected") && strings.Contains(body, "allowed fields"), "The model did not receive a repairable unknown-field error")
					fixture := nativeFunctionFixture(protocol, `{"tool":"plan","text":"Recovery succeeded"}`)
					fmt.Fprint(w, strings.ReplaceAll(fixture, "call-native", "call-corrected"))
				default:
					fmt.Fprint(w, nativeStreamFixture(protocol, "Complete."))
				}
			})
			config := nativeConfig(protocol)
			config.BaseURL, config.ContextChars = server.URL, 24000
			configureModel(t, engine, config)
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "history": []Message{}, "action": "agent", "instruction": "Plan a quiet return home"}))
			assert(t, job.Status == "done" && calls.Load() == 3, "Native correction failed in the default local window: "+job.Error)
			assert(t, len(job.Steps) == 2 && job.Steps[0].Status == "error" && job.Steps[1].Status == "done", "Invalid arguments were executed or a corrected call was lost")
			current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
			assert(t, current.Revision == project.Revision && len(current.Entities) == 0, "Argument correction changed the saved story")
		})
	}
}

// TestNativeAfterValueCorrection checks typed write errors and correction before any candidate or story mutation.
func TestNativeAfterValueCorrection(t *testing.T) {
	for _, protocol := range []string{"openai", "responses", "anthropic", "gemini", "gemini_generate", "ollama"} {
		t.Run(protocol, func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			insertProject(t, engine, project)
			var calls atomic.Int32
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				body := string(readBodyForStudioTest(t, r))
				switch calls.Add(1) {
				case 1:
					fmt.Fprint(w, nativeFunctionFixture(protocol, `{"tool":"propose_changes","operations":[{"target_kind":"plan","op_type":"upsert","after_value":{"level":"chapter","title":"Return","start_chapter":2,"end_chapter":2,"choices":"Stay"}}]}`))
				case 2:
					assert(t, strings.Contains(body, "choices") && strings.Contains(body, "unknown"), "The wrong plan field did not produce a local correction result")
					fmt.Fprint(w, strings.ReplaceAll(nativeFunctionFixture(protocol, `{"tool":"propose_changes","operations":[{"target_kind":"plan","op_type":"upsert","after_value":{"level":"chapter","title":"Return","start_chapter":2,"end_chapter":2,"choice":"Stay"}}]}`), "call-native", "call-corrected"))
				default:
					fmt.Fprint(w, nativeStreamFixture(protocol, "The chapter outline is staged."))
				}
			})
			config := nativeConfig(protocol)
			config.BaseURL, config.ContextChars = server.URL, 24000
			configureModel(t, engine, config)
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "history": []Message{}, "action": "agent", "instruction": "Draft the next chapter outline"}))
			assert(t, job.Status == "done" && calls.Load() == 3 && len(job.ChangeSets) == 1, "The corrected typed write was lost or staged twice: "+job.Error)
			current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
			assert(t, current.Revision == project.Revision && len(current.Studio.Plans) == 0, "A rejected model field altered saved plans")
		})
	}
}

// TestNativeLargeToolResultsKeepOriginals exercises a read, a typed proposal and a final answer in the default window.
func TestNativeLargeToolResultsKeepOriginals(t *testing.T) {
	for _, protocol := range []string{"openai", "responses", "anthropic", "gemini", "gemini_generate", "ollama"} {
		t.Run(protocol, func(t *testing.T) {
			engine := testEngine(t)
			project := testProject()
			project.Chapters[0].Body = strings.Repeat("An original saved paragraph. ", 500) + "ORIGINAL_SOURCE_TAIL"
			insertProject(t, engine, project)
			instruction := strings.Repeat("Keep the quiet scene and its character voices. ", 65) + "EXACT_AUTHOR_REQUEST_END"
			var calls atomic.Int32
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				body := string(readBodyForStudioTest(t, r))
				assert(t, strings.Contains(body, "EXACT_AUTHOR_REQUEST_END"), "Tool-loop packing truncated the author's current request")
				switch calls.Add(1) {
				case 1:
					fmt.Fprint(w, nativeFunctionFixture(protocol, `{"tool":"get_chapter_window","chapter":1}`))
				case 2:
					assert(t, strings.Contains(body, "get_task_step") && strings.Contains(body, "tool_result"), "The bounded read lost its original source address")
					fmt.Fprint(w, strings.ReplaceAll(nativeFunctionFixture(protocol, `{"tool":"propose_changes","operations":[{"target_kind":"plan","op_type":"upsert","title":"Return the letter","after_value":{"level":"chapter","title":"Return the letter","start_chapter":2,"end_chapter":2,"choice":"Wait for the courier"}}]}`), "call-native", "call-next"))
				default:
					fmt.Fprint(w, nativeStreamFixture(protocol, "The next scene is retained as a plan candidate."))
				}
			})
			config := nativeConfig(protocol)
			config.BaseURL, config.ContextChars = server.URL, 24000
			configureModel(t, engine, config)
			job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", AIRequest{ProjectID: project.ID, ChapterID: project.Chapters[0].ID, Action: "agent", Instruction: instruction, History: []Message{}}))
			assert(t, job.Status == "done" && calls.Load() == 3 && len(job.ChangeSets) == 1, "A large source exhausted a normal tool roundtrip: "+job.Error)
			assert(t, strings.Contains(job.Steps[0].FullDetail, "ORIGINAL_SOURCE_TAIL"), "Packing the model preview discarded the stored original")
		})
	}
}

// TestNativeWritingResultHostValidation enforces stage enums and bounds when a provider ignores its schema.
func TestNativeWritingResultHostValidation(t *testing.T) {
	spec := reviewResultTool()
	for _, arguments := range []string{
		`{"summary":"Ready","issues":[],"memories":[],"needs_revision":false,"extra":"ignored"}`,
		`{"summary":"Ready","issues":[{"kind":"invented","scope":"prose","quote":"She stays","explanation":"Opinion","suggestion":"Cut"}],"memories":[],"needs_revision":true}`,
		`{"summary":"Ready","issues":[],"memories":[]}`,
	} {
		output := modelOutput{ToolCalls: []NativeToolCall{{Name: spec.Name, Arguments: arguments}}}
		assert(t, validateResultToolCall(output, spec) != nil, "A schema-invalid native writing review was accepted by the host")
	}
	valid := modelOutput{ToolCalls: []NativeToolCall{{Name: spec.Name, Arguments: `{"summary":"Ready","issues":[],"memories":[],"needs_revision":false}`}}}
	assert(t, validateResultToolCall(valid, spec) == nil, "A valid native writing review was rejected")
}

// TestWritingCompositionContract rejects vague or mistyped scene results and isolates nested checkpoint arrays.
func TestWritingCompositionContract(t *testing.T) {
	value := WritingComposition{Intent: "A quiet return", Approach: "Honor the promise", SceneCards: []WritingScene{{POV: "Lin", Goal: "Return the key", Choice: "Wait for Chen", Action: "Set the key by his bowl", Outcome: "Chen agrees to keep it", Characters: []WritingSceneCharacter{{Name: "Chen", Want: "Finish supper first", Response: "Ask Lin to sit"}}}}}
	parsed, err := parseWritingComposition(jsonText(value))
	assert(t, err == nil && completeWritingComposition(parsed), fmt.Sprint(err))
	copy := copyWritingComposition(parsed)
	copy.SceneCards[0].Characters[0].Want = "Leave"
	assert(t, parsed.SceneCards[0].Characters[0].Want == "Finish supper first", "A mutable scene participant escaped checkpoint isolation")
	for _, invalid := range []string{
		`{"intent":"A","approach":"B"}`,
		strings.Replace(jsonText(value), `"outcome":`, `"outcomes":`, 1),
		strings.Replace(jsonText(value), `"want":"Finish supper first"`, `"want":null`, 1),
		strings.Replace(jsonText(value), `"scene_cards":`, `"scene_cards_typo":`, 1),
	} {
		_, err := parseWritingComposition(invalid)
		assert(t, err != nil, "An incomplete or mistyped scene contract was accepted")
	}
	value.SceneCards = make([]WritingScene, 13)
	_, err = parseWritingComposition(jsonText(value))
	assert(t, err != nil && strings.Contains(err.Error(), "twelve"), "A composition exceeded its bounded local planning size")
}

// TestWritingLanguageConstraintLocations checks exact author constraints on new Chinese prose without a quality score.
func TestWritingLanguageConstraintLocations(t *testing.T) {
	project := testProject()
	project.taskLanguage = "zh-CN"
	body := "只见他推门。似乎是雨。仿佛是风。就在这时，灯灭了。片刻后，人来了。随即他坐下，于是又站起。"
	review := WritingReview{Summary: "Read the scene.", Structured: true}
	appendWritingLanguageIssues(&project, body, &review)
	assert(t, review.NeedsRevision && len(review.Issues) == 5, "Explicit author constraints were omitted or generated excessive feedback")
	for _, issue := range review.Issues {
		assert(t, strings.Contains(body, issue.Quote) && issue.Kind == "reading" && issue.Scope == "prose", "A language issue lacks an exact prose location")
		assert(t, !strings.Contains(issue.Explanation, "AI检测") && strings.Contains(issue.Suggestion, "动作"), "A literal phrase check claimed authorship detection or suggested mechanical synonym replacement")
	}
	english := WritingReview{Summary: "Ready.", Structured: true}
	project.taskLanguage = "en"
	appendWritingLanguageIssues(&project, body, &english)
	assert(t, len(english.Issues) == 0 && !english.NeedsRevision, "Chinese author constraints changed English writing")
	for _, language := range []string{"en", "zh-CN"} {
		project.taskLanguage = language
		for _, stage := range []string{"outline", "composition", "prose", "review"} {
			guidance := writingCraftGuidance(&project, stage)
			for _, card := range writingCraftCards(&project, stage) {
				assert(t, card.Trigger != "" && card.Action != "" && card.Counterexample != "", "A built-in method lacks executable guidance")
			}
			for _, scripted := range []string{"repeats the missed word", "One job per line", "原词", "一句只办一件事", "接住上一句"} {
				assert(t, !strings.Contains(guidance, scripted), "A scripted dialogue rule returned to the "+stage+" stage")
			}
			logic := strings.Contains(guidance, "promised, received") || strings.Contains(guidance, "答应给")
			assert(t, logic == (stage != "prose"), "Story-logic rules must reach planning and review but stay out of prose drafting")
			assert(t, strings.Contains(guidance, "romantic") || strings.Contains(guidance, "雌竞"), "A stage lost the romantic-rivalry boundary")
		}
	}
	assert(t, len(writingCraftCards(&project, "prose")) == 0, "Prose drafting received planning cards instead of following the composition")
	assert(t, strings.Contains(writingCraftGuidance(&project, "prose"), "心里话") && strings.Contains(writingCraftGuidance(&project, "review"), "交接信息"), "Chinese drafting guidance does not ask for the viewpoint character's own voice")
	assert(t, strings.Contains(writingCraftGuidance(&project, "review"), "不当作叙述事实核对") && strings.Contains(writingCraftGuidance(&project, "review"), "当真"), "Chinese review checks the viewpoint character's jokes and guesses as narrated fact")
	project.taskLanguage = "en"
	assert(t, strings.Contains(writingCraftGuidance(&project, "prose"), "private thoughts") && strings.Contains(writingCraftGuidance(&project, "review"), "information hand-off"), "English drafting guidance does not ask for the viewpoint character's own voice")
	assert(t, strings.Contains(writingCraftGuidance(&project, "review"), "not narrated fact") && strings.Contains(writingCraftGuidance(&project, "review"), "takes as true"), "English review checks the viewpoint character's jokes and guesses as narrated fact")
}

// voiceTestProject surrounds an empty sixth chapter with earlier chapters of different editing histories
// and a later, more polished chapter that must never leak into the samples.
func voiceTestProject(block string) Project {
	project := NewProject("Voice", "Fantasy", "Two missing coins")
	project.taskLanguage = "zh-CN"
	lively, flat := strings.Repeat(block, 4), strings.Repeat("他走到门口，把门关上，又走回来坐下。\n", 20)
	setups := []struct {
		body, status string
		versions     int
	}{{lively, "final", 0}, {lively, "draft", 5}, {lively, "draft", 1}, {flat, "draft", 0}, {lively, "draft", 2}, {"", "draft", 0}, {"LATER_SPOILER" + lively, "final", 9}}
	project.Chapters = nil
	for index, setup := range setups {
		chapter := NewChapter(uint32(index+1), fmt.Sprintf("第%d章", index+1))
		chapter.Body, chapter.Status, chapter.Versions = setup.body, setup.status, make([]Version, setup.versions)
		project.Chapters = append(project.Chapters, chapter)
	}
	return project
}

// TestWritingVoiceSamplesAndDrift checks that prose drafting borrows the author's voice only from earlier,
// most-edited chapters, drops the samples when the context is full, and flags flat drafts exactly once.
func TestWritingVoiceSamplesAndDrift(t *testing.T) {
	block := strings.Join([]string{
		"林舟蹲在灶台边数铜板，数到第三遍还是少了两枚。少了就少了吧，总不能真去耗子洞里掏回来？他越想越气，这耗子比他还会过日子呢！",
		"“你又在数那几个铜板了，数一百遍它也不会自己多出来的。”小满靠在门框上笑他。",
		"“我这不是怕师娘明早问起来，我连个说法都编不出来嘛。”林舟头也没抬。",
		"“那你就说是我拿去买糖了，反正她也舍不得骂我。”小满说完自己先乐了。",
		"买糖？她兜里比脸还干净。林舟心说这丫头撒谎都不打草稿，偏偏师娘还真信她，啧，同人不同命啊。",
	}, "\n") + "\n"
	project := voiceTestProject(block)
	target := &project.Chapters[5]
	numbers := []uint32{}
	for _, anchor := range voiceAnchorChapters(&project, target) {
		numbers = append(numbers, anchor.Number)
	}
	assert(t, fmt.Sprint(numbers) == "[1 2 5 3]", "Voice anchors ignored final status, edit history, the earlier-chapter limit or the cap: "+fmt.Sprint(numbers))
	samples, total, chapters := writingVoiceSamples(&project, target), 0, map[uint32]bool{}
	for _, sample := range samples {
		total, chapters[sample.Chapter] = total+charCount(sample.Text), true
		assert(t, strings.Contains(project.Chapters[sample.Chapter-1].Body, sample.Text), "A voice sample was not copied verbatim from its chapter")
	}
	assert(t, len(samples) == 3 && len(chapters) == 2 && total <= 1300, fmt.Sprintf("Voice samples lost their mix or budget: %d samples, %d chapters, %d chars", len(samples), len(chapters), total))
	voice := writingVoiceBlock(&project, target)
	assert(t, strings.HasPrefix(voice, "\n<voice_samples>\n") && strings.Contains(voice, "[第2章]") && strings.Contains(voice, "不要复用") && !strings.Contains(voice, "LATER_SPOILER"), "The voice block lacks its boundary, chapter labels or reuse warning, or leaked a later chapter")
	original := Prompt{Messages: []Message{{Role: "system", Content: "System"}, {Role: "user", Content: "写第六章。"}}}
	full := writingDraftPromptWithVoice(&project, target, original, &WritingRun{}, 1<<20)
	last := full.Messages[len(full.Messages)-1].Content
	assert(t, strings.HasPrefix(last, "[Writing prose stage]") && strings.HasSuffix(last, "</voice_samples>"), "Voice samples did not close the prose instruction")
	tight := writingDraftPromptWithVoice(&project, target, original, &WritingRun{}, 50)
	last = tight.Messages[len(tight.Messages)-1].Content
	assert(t, strings.HasPrefix(last, "[Writing prose stage]") && !strings.Contains(last, "<voice_samples>"), "Voice samples were kept although they overflow the model context")
	flat := strings.Repeat("“走了。”\n“去哪。”\n他推开门，院里有风。他把米放下，转身关门，又把灯挑亮。\n", 25)
	for revisions, requested := range []bool{true, false} {
		review := WritingReview{Structured: true}
		appendVoiceDriftIssues(&project, target, flat, revisions, &review)
		assert(t, len(review.Issues) == 2 && review.NeedsRevision == requested, fmt.Sprintf("Voice drift after %d revisions: %d issues, needs_revision=%v", revisions, len(review.Issues), review.NeedsRevision))
		for _, issue := range review.Issues {
			assert(t, issue.Quote != "" && strings.Contains(flat, issue.Quote) && issue.Kind == "reading" && issue.Scope == "prose", "A voice drift issue lacks an exact prose location")
		}
	}
	for _, draft := range []string{strings.Repeat(block, 5), "“走。”他说。"} {
		review := WritingReview{Structured: true}
		appendVoiceDriftIssues(&project, target, draft, 0, &review)
		assert(t, len(review.Issues) == 0 && !review.NeedsRevision, "A draft in the established voice, or too short to measure, was flagged")
	}
	// Flat-sounding narration alone stays a note, since the count misses deadpan inner voice; clipped dialogue still asks for the revision.
	narrationOnly := strings.Repeat("“你又在数那几个铜板了，数一百遍它也不会自己多出来的。”\n他推开门，院里有风。他把米放下，转身关门，又把灯挑亮。\n", 25)
	dialogueOnly := strings.Repeat("“走了。”\n“去哪。”\n"+strings.SplitN(block, "\n", 2)[0]+"\n", 10)
	for draft, requested := range map[string]bool{narrationOnly: false, dialogueOnly: true} {
		review := WritingReview{Structured: true}
		appendVoiceDriftIssues(&project, target, draft, 0, &review)
		assert(t, len(review.Issues) == 1 && review.NeedsRevision == requested, fmt.Sprintf("A single voice signal gave %d issues and needs_revision=%v, want %v", len(review.Issues), review.NeedsRevision, requested))
	}
	first, review := &project.Chapters[0], WritingReview{Structured: true}
	appendVoiceDriftIssues(&project, first, flat, 0, &review)
	assert(t, len(review.Issues) == 0 && writingVoiceBlock(&project, first) == "", "The first chapter borrowed a voice from later chapters")
}
