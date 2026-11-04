package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// nativeConfig provides valid configuration for local protocol fixtures.
func nativeConfig(protocol string) ProviderConfig {
	config := defaultProvider()
	config.Protocol, config.Model, config.Temperature = protocol, "fixture-model", nil
	return config
}

// nativeStreamFixture constructs answer, reasoning, and terminal events for protocol contract tests.
func nativeStreamFixture(protocol, text string) string {
	switch protocol {
	case "openai":
		return "data: " + jsonText(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning_content": "PRIVATE_THOUGHT"}}}}) + "\n\n" + streamPayload(text)
	case "responses":
		return "event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"PRIVATE_THOUGHT\"}\n\nevent: response.output_text.delta\ndata: " + jsonText(map[string]any{"type": "response.output_text.delta", "delta": text}) + "\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
	case "anthropic":
		return "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"PRIVATE_THOUGHT\"}}\n\nevent: content_block_delta\ndata: " + jsonText(map[string]any{"type": "content_block_delta", "delta": map[string]string{"type": "text_delta", "text": text}}) + "\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	case "gemini":
		return "event: step.start\ndata: {\"event_type\":\"step.start\",\"index\":0,\"step\":{\"type\":\"thought\"}}\n\nevent: step.delta\ndata: {\"event_type\":\"step.delta\",\"index\":0,\"delta\":{\"type\":\"thought_summary\",\"content\":{\"type\":\"text\",\"text\":\"PRIVATE_THOUGHT\"}}}\n\nevent: step.start\ndata: {\"event_type\":\"step.start\",\"index\":1,\"step\":{\"type\":\"model_output\"}}\n\nevent: step.delta\ndata: " + jsonText(map[string]any{"event_type": "step.delta", "index": 1, "delta": map[string]string{"type": "text", "text": text}}) + "\n\nevent: interaction.completed\ndata: {\"event_type\":\"interaction.completed\",\"interaction\":{\"status\":\"completed\"}}\n\nevent: done\ndata: [DONE]\n\n"
	case "gemini_generate":
		return "data: " + jsonText(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "PRIVATE_THOUGHT", "thought": true}, map[string]any{"text": text}}}, "finishReason": "STOP"}}}) + "\n\n"
	case "ollama":
		return jsonText(map[string]any{"message": map[string]string{"thinking": "PRIVATE_THOUGHT"}, "done": false}) + "\n" + jsonText(map[string]any{"message": map[string]string{"content": text}, "done": true, "done_reason": "stop"}) + "\n"
	default:
		panic("unknown test protocol")
	}
}

// TestProviderPresetsCoverElevenServices checks provider coverage, credential-free defaults, and legacy configuration.
func TestProviderPresetsCoverElevenServices(t *testing.T) {
	providers, ids := map[string]bool{}, map[string]bool{}
	for _, preset := range providerPresets() {
		assert(t, !ids[preset.ID], "Duplicate preset ID")
		ids[preset.ID], providers[preset.Config.ProviderID] = true, true
		assert(t, preset.Config.APIKey == "" && strings.HasPrefix(preset.DocsURL, "https://"), "A preset contains credentials or lacks an official source")
		if _, err := validateConfig(preset.Config); err != nil {
			t.Fatalf("%s: %v", preset.ID, err)
		}
	}
	assert(t, len(providers) == 11 && len(ids) == 15, "Incorrect provider or preset count")
	legacy := defaultProvider()
	legacy.Model = "legacy"
	decoded := ProviderConfig{}
	assert(t, decodeJSON(rawMessage(legacy), &decoded) == nil && decoded.ProviderID == "", "A legacy configuration was assigned a new preset")
	engine := testEngine(t)
	catalog := invoke[struct {
		CheckedAt string           `json:"checked_at"`
		Presets   []ProviderPreset `json:"presets"`
	}](t, engine, "provider_presets", nil)
	assert(t, catalog.CheckedAt == ProviderDocsDate && len(catalog.Presets) == 15, "Presets are unavailable through the command entry point")
}

// TestNativeRequestContracts checks real HTTP paths, headers, roles, and output budget fields.
func TestNativeRequestContracts(t *testing.T) {
	cases := []struct{ protocol, path, keyHeader, budgetField string }{
		{"openai", "/v1/chat/completions", "Authorization", "max_tokens"},
		{"responses", "/v1/responses", "Authorization", "max_output_tokens"},
		{"anthropic", "/v1/messages", "Authorization", "max_tokens"},
		{"gemini", "/v1beta/interactions", "X-Goog-Api-Key", "generation_config"},
		{"gemini_generate", "/v1beta/models/fixture-model:streamGenerateContent", "X-Goog-Api-Key", "generationConfig"},
		{"ollama", "/api/chat", "Authorization", "options"},
	}
	for _, test := range cases {
		t.Run(test.protocol, func(t *testing.T) {
			var received map[string]any
			server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path {
					t.Errorf("path=%s", request.URL.Path)
				}
				key := "test-key"
				if test.keyHeader == "Authorization" {
					key = "Bearer " + key
				}
				if request.Header.Get(test.keyHeader) != key {
					t.Errorf("Incorrect authentication header: %s", test.keyHeader)
				}
				if strings.Contains(request.URL.RawQuery, "test-key") {
					t.Error("An API key appeared in the URL")
				}
				if test.protocol == "gemini_generate" && request.URL.Query().Get("alt") != "sse" {
					t.Error("Missing SSE parameter")
				}
				if test.protocol == "anthropic" && request.Header.Get("anthropic-version") != AnthropicVersion {
					t.Error("Incorrect API version header")
				}
				if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
					t.Error(err)
				}
				fmt.Fprint(writer, nativeStreamFixture(test.protocol, "\u4e2d\u6587🙂\u6b63\u6587"))
			})
			config := nativeConfig(test.protocol)
			config.APIKey = "test-key"
			config.BaseURL = server.URL + "/v1"
			if strings.HasPrefix(test.protocol, "gemini") {
				config.BaseURL = server.URL + "/v1beta"
			}
			if test.protocol == "ollama" {
				config.BaseURL = server.URL
			}
			text, err := streamModel(context.Background(), config, []Message{{Role: "system", Content: "System instruction"}, {Role: "user", Content: "Question"}, {Role: "assistant", Content: "Earlier reply", SourceJobID: "internal-job"}, {Role: "user", Content: "Continue"}}, func(string) {})
			assert(t, err == nil && text == "\u4e2d\u6587🙂\u6b63\u6587", fmt.Sprintf("Streaming response error: %v", err))
			assert(t, received[test.budgetField] != nil, "The request used the wrong output budget")
			assert(t, !strings.Contains(jsonText(received), "source_job_id") && !strings.Contains(jsonText(received), "test-key"), "Internal identifiers or credentials entered the request body")
			switch test.protocol {
			case "responses":
				assert(t, received["store"] == false && len(received["input"].([]any)) == 4, "Incorrect Responses input or store parameter")
			case "anthropic":
				assert(t, received["system"] != nil && len(received["messages"].([]any)) == 3, "Claude system instructions were not separated")
			case "gemini":
				assert(t, received["system_instruction"] == "System instruction" && received["store"] == false && objectField(received, "generation_config")["thinking_summaries"] == "none", "Incorrect Gemini parameters")
				assert(t, received["input"].([]any)[1].(map[string]any)["type"] == "model_output", "Incorrect Gemini history roles")
			case "gemini_generate":
				assert(t, received["model"] == nil && received["stream"] == nil && received["systemInstruction"] != nil, "Incorrect Generate Content parameters")
				assert(t, received["contents"].([]any)[1].(map[string]any)["role"] == "model", "Incorrect Generate Content history roles")
			}
		})
	}
}

// TestProviderSpecificParameters checks distinct provider parameters within compatible protocols.
func TestProviderSpecificParameters(t *testing.T) {
	for _, preset := range providerPresets() {
		t.Run(preset.ID, func(t *testing.T) {
			config := preset.Config
			if len(preset.ThinkingModes) > 0 {
				config.ThinkingMode = "enabled"
			}
			if len(preset.ReasoningEfforts) > 0 {
				config.ReasoningEffort = preset.ReasoningEfforts[0]
			}
			body, headers, err := modelRequest(config, []Message{{Role: "system", Content: "system"}, {Role: "user", Content: "test"}})
			assert(t, err == nil, "A preset could not create a request")
			var wire map[string]any
			assert(t, decodeJSON(rawMessage(body), &wire) == nil, "The request body could not be encoded as JSON")
			body = wire
			assert(t, body["temperature"] == nil && headers.Get("Authorization") == "", "A preset included temperature or an absent key")
			switch preset.ID {
			case "openai":
				assert(t, objectField(body, "reasoning")["effort"] == "low", "Incorrect OpenAI reasoning effort")
			case "openai-chat":
				assert(t, body["max_completion_tokens"] != nil && body["max_tokens"] == nil, "Incorrect OpenAI Chat token field")
				assert(t, body["messages"].([]any)[0].(map[string]any)["role"] == "developer", "Incorrect OpenAI developer role")
			case "anthropic":
				assert(t, objectField(body, "output_config")["effort"] == "low", "Missing Claude reasoning effort")
			case "bailian":
				assert(t, body["enable_thinking"] == true && body["extra_body"] == nil, "Bailian parameters were not at the top level of the HTTP body")
				assert(t, body["max_completion_tokens"] != nil && body["max_tokens"] == nil && body["reasoning_effort"] == "low", "Incorrect Bailian total budget or effort field")
			case "ark":
				assert(t, body["max_completion_tokens"] != nil && body["max_tokens"] == nil, "Incorrect Ark total budget field")
			case "minimax":
				assert(t, body["reasoning_split"] == true && body["reasoning_effort"] == "low", "Missing MiniMax reasoning separation or effort")
			case "moonshot":
				assert(t, body["max_completion_tokens"] != nil && body["max_tokens"] == nil, "Kimi K3 used an outdated budget field")
			case "minimax-m3":
				assert(t, strings.Contains(jsonText(body), `"type":"adaptive"`), "MiniMax thinking mode did not use adaptive")
			case "ollama":
				assert(t, body["think"] == true && body["options"] != nil, "Incorrect Ollama options")
			}
		})
	}
}

// TestProviderOptionValidation rejects conflicting or unsupported options and retains explicit Anthropic legacy support.
func TestProviderOptionValidation(t *testing.T) {
	for _, preset := range providerPresets() {
		config := preset.Config
		if preset.ID == "anthropic" || preset.ID == "minimax" || preset.ID == "moonshot" || preset.ID == "deepseek" {
			temperature := 0.7
			config.Temperature = &temperature
			_, err := validateConfig(config)
			assert(t, err != nil, "Unsupported temperature was accepted")
		}
	}
	config := nativeConfig("anthropic")
	config.APIVersion = "2023-06-01"
	config.APIKey = "legacy-key"
	temperature := 0.5
	config.Temperature = &temperature
	body, headers, err := modelRequest(config, []Message{{Role: "user", Content: "test"}})
	assert(t, err == nil && body["temperature"] == 0.5 && headers.Get("x-api-key") == "legacy-key" && headers.Get("Authorization") == "", "Anthropic legacy compatibility failed")
	config = nativeConfig("openai")
	config.ProviderID = "minimax"
	config.Model = "MiniMax-M3.1-Flash"
	config.ThinkingMode = "disabled"
	_, err = validateConfig(config)
	assert(t, err != nil, "MiniMax Flash accepted disabled thinking")
	config = nativeConfig("gemini_generate")
	config.BaseURL = "https://generativelanguage.googleapis.com/v1beta/models/old:generateContent"
	endpoint, err := validateConfig(config)
	assert(t, err == nil && strings.Contains(endpoint, "/models/fixture-model:streamGenerateContent?alt=sse"), "A complete Gemini endpoint used an outdated model")
	config = nativeConfig("openai")
	config.BaseURL = "https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"
	_, err = validateConfig(config)
	assert(t, err != nil, "An unresolved workspace placeholder was accepted")
}

// TestSSEFramesPreserveUnicodeAndMultiline checks framing with real HTTP bytewise, CRLF, and multiline input.
func TestSSEFramesPreserveUnicodeAndMultiline(t *testing.T) {
	payload := ": heartbeat\r\nevent: delta\r\ndata: {\r\ndata: \"choices\":[{\"delta\":{\"content\":\"\u4e2d\u6587🙂\"}}]}\r\n\r\ndata: [DONE]"
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		for _, value := range []byte(payload) {
			writer.Write([]byte{value})
			writer.(http.Flusher).Flush()
		}
	})
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	text, err := streamModel(context.Background(), config, []Message{}, func(string) {})
	assert(t, err == nil && text == "\u4e2d\u6587🙂", fmt.Sprintf("SSE framing error: %v", err))
}

// TestSSETransportVariants checks five SSE protocols with valid line endings and a leading BOM.
func TestSSETransportVariants(t *testing.T) {
	text := "\u7b2c\u4e00\u6bb5。\ufeff\u7b2c\u4e8c\u6bb5。"
	quoted := jsonText(text)
	cases := []struct {
		protocol string
		events   []string
	}{
		{"openai", []string{fmt.Sprintf(`{"choices":[{"delta":{"content":%s}}]}`, quoted), `{"choices":[{"delta":{},"finish_reason":"stop"}]}`}},
		{"responses", []string{fmt.Sprintf(`{"type":"response.output_text.delta","delta":%s}`, quoted), `{"type":"response.completed","response":{"status":"completed"}}`}},
		{"anthropic", []string{fmt.Sprintf(`{"type":"content_block_delta","delta":{"type":"text_delta","text":%s}}`, quoted), `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`}},
		{"gemini", []string{`{"event_type":"step.start","index":0,"step":{"type":"model_output"}}`, fmt.Sprintf(`{"event_type":"step.delta","index":0,"delta":{"type":"text","text":%s}}`, quoted), `{"event_type":"interaction.completed","interaction":{"status":"completed"}}`}},
		{"gemini_generate", []string{fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"text":%s}]},"finishReason":"STOP"}]}`, quoted)}},
	}
	variants := []struct {
		name, newline, prefix string
		bytewise              bool
	}{
		{"lf", "\n", "", false},
		{"crlf", "\r\n", "", false},
		{"cr", "\r", "", false},
		{"bom-lf", "\n", "\ufeff", false},
		{"bom-crlf-bytewise", "\r\n", "\ufeff", true},
		{"bom-cr-bytewise", "\r", "\ufeff", true},
	}
	for _, test := range cases {
		for _, variant := range variants {
			t.Run(test.protocol+"/"+variant.name, func(t *testing.T) {
				payload := variant.prefix
				for _, event := range test.events {
					payload += "data: " + event + variant.newline + variant.newline
				}
				server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
					_, _ = io.Copy(io.Discard, request.Body)
					writer.Header().Set("Content-Type", "text/event-stream")
					if variant.bytewise {
						for _, value := range []byte(payload) {
							_, _ = writer.Write([]byte{value})
							writer.(http.Flusher).Flush()
						}
					} else {
						_, _ = io.WriteString(writer, payload)
					}
				})
				config := nativeConfig(test.protocol)
				config.BaseURL = server.URL
				var received strings.Builder
				output, err := streamModel(context.Background(), config, []Message{{Role: "user", Content: "Generate prose"}}, func(delta string) { received.WriteString(delta) })
				assert(t, err == nil && output == text && received.String() == text, fmt.Sprintf("Incomplete SSE text: output=%q err=%v", output, err))
			})
		}
	}
}

// TestProviderTerminalFailures rejects truncation, missing terminal events, tool requirements, and stream errors.
func TestProviderTerminalFailures(t *testing.T) {
	cases := []struct{ name, protocol, payload string }{
		{"chat-length", "openai", "data: {\"choices\":[{\"delta\":{\"content\":\"Partial prose\"},\"finish_reason\":\"length\"}]}\n\n"},
		{"chat-tool", "openai", "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"},
		{"responses-incomplete", "responses", "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\"}}\n\n"},
		{"responses-error", "responses", "data: {\"type\":\"error\",\"message\":\"SECRET_NOT_FOR_LOGS\"}\n\n"},
		{"claude-length", "anthropic", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n"},
		{"claude-missing-reason", "anthropic", "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Partial\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"gemini-incomplete", "gemini", "data: {\"event_type\":\"interaction.completed\",\"interaction\":{\"status\":\"incomplete\"}}\n\n"},
		{"gemini-unowned-text", "gemini", "data: {\"event_type\":\"step.delta\",\"index\":0,\"delta\":{\"type\":\"text\",\"text\":\"Undeclared text\"}}\n\n"},
		{"generate-length", "gemini_generate", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Partial\"}]},\"finishReason\":\"MAX_TOKENS\"}]}\n\n"},
		{"ollama-length", "ollama", "{\"message\":{\"content\":\"Partial\"},\"done\":true,\"done_reason\":\"length\"}\n"},
		{"vendor-code", "openai", "data: {\"base_resp\":{\"status_code\":2013,\"status_msg\":\"SECRET_NOT_FOR_LOGS\"}}\n\n"},
		{"unfinished", "openai", "data: {\"choices\":[{\"delta\":{\"content\":\"Partial\"}}]}\n\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := staticModel(t, test.payload, 0)
			config := nativeConfig(test.protocol)
			config.BaseURL = server.URL
			var partial strings.Builder
			_, err := streamModel(context.Background(), config, []Message{}, func(text string) { partial.WriteString(text) })
			assert(t, err != nil, "An unsuccessful response was treated as successful")
			assert(t, !strings.Contains(err.Error(), "SECRET_NOT_FOR_LOGS"), "A raw provider error leaked into history")
			if test.name == "chat-length" {
				assert(t, partial.String() == "Partial prose", "Text received in the truncated frame was lost")
			}
		})
	}
}

// TestEveryProtocolCanCancel checks cancellation of real requests and blocking reads across all six protocols.
func TestEveryProtocolCanCancel(t *testing.T) {
	for _, protocol := range []string{"openai", "responses", "anthropic", "gemini", "gemini_generate", "ollama"} {
		t.Run(protocol, func(t *testing.T) {
			entered := make(chan struct{})
			server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
				io.Copy(io.Discard, request.Body)
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.WriteHeader(200)
				writer.(http.Flusher).Flush()
				close(entered)
				<-request.Context().Done()
			})
			config := nativeConfig(protocol)
			config.BaseURL = server.URL
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := streamModel(ctx, config, []Message{}, func(string) {}); done <- err }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("The request did not reach the service")
			}
			cancel()
			select {
			case err := <-done:
				assert(t, err != nil && strings.Contains(err.Error(), "cancelled"), "The request did not end as cancelled")
			case <-time.After(3 * time.Second):
				t.Fatal("Cancellation did not stop the network read")
			}
		})
	}
}

// TestWikiWorksWithEveryNativeProtocol checks atomic pending-knowledge publication through both Wiki stages for each protocol.
func TestWikiWorksWithEveryNativeProtocol(t *testing.T) {
	for _, protocol := range []string{"openai", "responses", "anthropic", "gemini", "gemini_generate", "ollama"} {
		for _, invalid := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/invalid=%t", protocol, invalid), func(t *testing.T) {
				engine := testEngine(t)
				project := testProject()
				insertProject(t, engine, project)
				staged := cloneProject(project)
				ingestCandidate(t, &staged)
				responses := []string{jsonText(map[string]any{"facts": []Candidate{testCandidate()}}), jsonText(MemorySection{Summary: "Lin Wu found a letter.", EvidenceIDs: []string{staged.Facts[0].ID}, Questions: []string{}})}
				if invalid {
					responses[1] = "Invalid JSON"
				}
				var calls atomic.Int32
				server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
					index := int(calls.Add(1)) - 1
					fmt.Fprint(writer, nativeStreamFixture(protocol, responses[index]))
				})
				config := nativeConfig(protocol)
				config.BaseURL = server.URL
				configureModel(t, engine, config)
				job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "wiki", "instruction": ""})
				done := finishJob(t, engine, job)
				latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
				assert(t, calls.Load() == 2 && !strings.Contains(done.Text, "PRIVATE_THOUGHT"), "Incorrect stage call count or reasoning mixed into prose")
				if invalid {
					assert(t, done.Status == "error" && latest.Revision == 0 && len(latest.Facts) == 0 && len(latest.Compilations) == 0, "Failure produced a partial knowledge write")
				} else {
					assert(t, done.Status == "done" && len(latest.Facts) == 1 && len(latest.Compilations) == 1, "Native protocol compilation failed: "+done.Error)
					assert(t, latest.Facts[0].Status == "pending", "Knowledge bypassed author review")
				}
			})
		}
	}
}

// TestPrivateProviderHistorySurvivesRestart checks private context restoration, public redaction, model isolation, and budgeting.
func TestPrivateProviderHistorySurvivesRestart(t *testing.T) {
	t.Setenv("QUILLWEAVE_API_KEY", "")
	path := filepath.Join(t.TempDir(), "history.sqlite3")
	engine, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	project := testProject()
	insertProject(t, engine, project)
	private := strings.Repeat("PRIVATE_REASONING ", 360)
	var calls atomic.Int32
	var forwarded atomic.Bool
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		json.NewDecoder(request.Body).Decode(&body)
		if calls.Add(1) > 1 {
			forwarded.Store(strings.Contains(jsonText(body), private))
		}
		fmt.Fprint(writer, "data: "+jsonText(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning_content": private}}}})+"\n\n"+streamPayload("Final answer"))
	})
	config := nativeConfig("openai")
	config.ProviderID = "moonshot"
	config.Model = "kimi-k3.1-pro"
	config.BaseURL = server.URL
	config.ContextChars = 24000
	configureModel(t, engine, config)
	args := map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "ask", "instruction": "First turn"}
	job := invoke[Job](t, engine, "start_ai", args)
	done := finishJob(t, engine, job)
	assert(t, done.Status == "done" && !strings.Contains(jsonText(done), private), "The public task exposed private context")
	runs := invoke[[]json.RawMessage](t, engine, "get_runs", map[string]any{"project_id": project.ID})
	assert(t, !strings.Contains(jsonText(runs), private), "Public history exposed private context")
	engine.Close()
	engine, err = NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	history := []Message{{Role: "user", Content: "First turn"}, {Role: "assistant", Content: "Final answer", SourceJobID: job.ID}}
	args["history"], args["instruction"] = history, "Second turn"
	job2 := invoke[Job](t, engine, "start_ai", args)
	assert(t, finishJob(t, engine, job2).Status == "done" && forwarded.Load(), "Compatible private history was not restored after restart")
	config.ContextChars = 4000
	configureModel(t, engine, config)
	_, err = engine.Dispatch("prepare_ai", rawMessage(args))
	assert(t, err != nil && strings.Contains(err.Error(), "4000"), "Reasoning history was absent from the context budget")
	config.ContextChars = 24000
	configureModel(t, engine, config)
	otherProject := testProject()
	insertProject(t, engine, otherProject)
	args["project_id"] = otherProject.ID
	forwarded.Store(false)
	otherJob := invoke[Job](t, engine, "start_ai", args)
	finishJob(t, engine, otherJob)
	assert(t, !forwarded.Load(), "Private history crossed project boundaries")
	args["project_id"] = project.ID
	settings := invoke[settingsResponse](t, engine, "get_model_settings", nil).Settings
	copy := settings.Profiles[0]
	copy.ID, copy.Name = "other-profile", "Other profile"
	settings.Profiles = append(settings.Profiles, copy)
	settings.DefaultProfile = copy.ID
	invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": settings})
	forwarded.Store(false)
	otherProfileJob := invoke[Job](t, engine, "start_ai", args)
	finishJob(t, engine, otherProfileJob)
	assert(t, !forwarded.Load(), "Private history crossed profile boundaries")
	config.Model = "kimi-k3"
	configureModel(t, engine, config)
	forwarded.Store(false)
	job3 := invoke[Job](t, engine, "start_ai", args)
	finishJob(t, engine, job3)
	assert(t, !forwarded.Load(), "Private history crossed model boundaries")
}

// TestArkEncryptedContextRoundTrip checks intact encrypted-context round trips within a matching model identity.
func TestArkEncryptedContextRoundTrip(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	var calls atomic.Int32
	var preserved atomic.Bool
	opaque := "opaque-signed-block+/="
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		json.NewDecoder(request.Body).Decode(&body)
		if calls.Add(1) > 1 {
			for _, raw := range body["messages"].([]any) {
				message := raw.(map[string]any)
				if message["role"] == "assistant" {
					preserved.Store(message["encrypted_content"] == opaque && message["reasoning_content"] == "Reasoning summary")
				}
			}
		}
		fmt.Fprint(writer, "data: "+jsonText(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"reasoning_content": "Reasoning summary"}}}})+"\n\ndata: "+jsonText(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"encrypted_content": opaque}}}})+"\n\n"+streamPayload("Answer text"))
	})
	config := nativeConfig("openai")
	config.ProviderID = "ark"
	config.Model = "doubao-seed-2-1-pro-260915"
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	args := map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "ask", "instruction": "First turn"}
	first := invoke[Job](t, engine, "start_ai", args)
	done := finishJob(t, engine, first)
	assert(t, done.Status == "done" && done.Text == "Answer text" && !strings.Contains(jsonText(done), opaque), "Private data appeared in public text")
	args["history"] = []Message{{Role: "assistant", Content: done.Text, SourceJobID: first.ID}}
	second := invoke[Job](t, engine, "start_ai", args)
	finishJob(t, engine, second)
	assert(t, preserved.Load(), "The encrypted reasoning block was not restored intact")
}

// TestProviderOptionsInvalidateWikiCache checks new option invalidation and stable legacy or key-rotation fingerprints.
func TestProviderOptionsInvalidateWikiCache(t *testing.T) {
	project := testProject()
	config := defaultProvider()
	config.Model = "mock"
	before, _ := compilationFingerprint(&project, project.Chapters[0].ID, config)
	expected := hashText(strings.Join([]string{project.Chapters[0].ID, hashText(project.Chapters[0].Body), rulesHash(&project), config.Protocol, config.BaseURL, config.Model}, "|"))
	assert(t, before == expected, "Legacy cache identity changed")
	config.ProviderID = "deepseek"
	config.ThinkingMode = "enabled"
	first, _ := compilationFingerprint(&project, project.Chapters[0].ID, config)
	config.ThinkingMode = "disabled"
	second, _ := compilationFingerprint(&project, project.Chapters[0].ID, config)
	assert(t, first != second && first != before, "Thinking options were absent from the cache identity")
	config.APIKey = "do-not-hash-key"
	third, _ := compilationFingerprint(&project, project.Chapters[0].ID, config)
	assert(t, third == second, "Credentials entered the cache identity")
}
