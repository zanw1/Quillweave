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
			`{"choices":[{"delta":{"reasoning_content":"PRIVATE_NATIVE"}}]}`,
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
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"PRIVATE_NATIVE"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"native-signature"}}`,
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
		return fmt.Sprintf("{\"message\":{\"role\":\"assistant\",\"thinking\":\"PRIVATE_NATIVE\",\"tool_calls\":[{\"function\":{\"name\":\"quillweave_step\",\"arguments\":%s}}]},\"done\":true,\"done_reason\":\"stop\"}\n", arguments)
	default:
		panic("unknown native fixture protocol")
	}
	return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
}

// assertNativeResult checks result correlation in the actual HTTP request, including vendor-specific envelopes.
func assertNativeResult(t *testing.T, protocol string, body map[string]any) {
	t.Helper()
	serialized := jsonText(body)
	assert(t, strings.Contains(serialized, "PRIVATE_NATIVE") && strings.Contains(serialized, "Native plan evidence"), "Native history or the tool result was lost")
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
			_, err := streamModelOutput(context.Background(), config, nil, func(string) {})
			assert(t, err != nil, "Invalid native parameters reached the business layer")
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
