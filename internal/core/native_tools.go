package core

import (
	"errors"
	"strings"
)

const nativeAgentFunction = "quillweave_step"

// NativeToolCall keeps public tool identity separate from provider-private replay blocks.
type NativeToolCall struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Arguments      string `json:"arguments"`
	ProviderCallID string `json:"provider_call_id,omitempty"`
}

var errNativeToolsUnsupported = errors.New("The model does not support native tools")

// nativeToolSchema exposes one bounded story-operation function with the same server-side validators.
func nativeToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool":  map[string]any{"type": "string", "enum": []string{"recall", "get_entity_detail", "get_chapter_window", "search_story_facts", "get_plan_node", "get_world_map", "get_source_detail", "get_decision_detail", "get_conversation_excerpt", "get_task_step", "search", "plan", "propose", "propose_changes", "researcher", "reviewer", "writer"}},
			"query": map[string]string{"type": "string"},
			"text":  map[string]string{"type": "string"},
		},
		"required":             []string{"tool"},
		"additionalProperties": true,
	}
}

// nativeToolDefinitions converts the shared operation schema into each protocol's native function declaration.
func nativeToolDefinitions(config ProviderConfig) []any {
	if !config.nativeTools {
		return nil
	}
	description := "Execute one Quillweave operation using the tool name and fields described in the system instructions. Reads and proposed changes are validated by the application. Formal changes require author preview and confirmation."
	function := map[string]any{"name": nativeAgentFunction, "description": description, "parameters": nativeToolSchema()}
	switch config.Protocol {
	case "openai", "ollama":
		if config.Protocol == "openai" {
			function["strict"] = false
		}
		return []any{map[string]any{"type": "function", "function": function}}
	case "responses":
		function["type"], function["strict"] = "function", false
		return []any{function}
	case "anthropic":
		return []any{map[string]any{"name": nativeAgentFunction, "description": description, "input_schema": nativeToolSchema()}}
	case "gemini":
		function["type"] = "function"
		return []any{function}
	case "gemini_generate":
		delete(function, "parameters")
		function["parametersJsonSchema"] = nativeToolSchema()
		return []any{map[string]any{"functionDeclarations": []any{function}}}
	}
	return nil
}

// nativeToolChars reserves the complete function declaration in the local input-character budget.
func nativeToolChars(config ProviderConfig) int {
	definitions := nativeToolDefinitions(config)
	if len(definitions) == 0 {
		return 0
	}
	return charCount(jsonText(definitions))
}

// applyNativeTools adds native declarations only to agent requests while preserving text-only callers.
func applyNativeTools(body map[string]any, config ProviderConfig) {
	if !config.nativeTools {
		return
	}
	body["tools"] = nativeToolDefinitions(config)
	switch config.Protocol {
	case "openai", "responses":
		body["parallel_tool_calls"] = false
	case "anthropic":
		body["tool_choice"] = map[string]any{"type": "auto", "disable_parallel_tool_use": true}
	}
}

// nativeArguments returns a validated JSON object for protocols that encode function parameters as objects.
func nativeArguments(call NativeToolCall) map[string]any {
	var value map[string]any
	_ = decodeJSON([]byte(call.Arguments), &value)
	return value
}

// storedProviderBlocks decodes opaque provider-native assistant items only for their matching protocol.
func storedProviderBlocks(message Message, protocol string) []map[string]any {
	if message.providerFormat != protocol {
		return nil
	}
	result := []map[string]any{}
	for _, raw := range message.providerBlocks {
		var item map[string]any
		if decodeJSON(raw, &item) == nil {
			result = append(result, item)
		}
	}
	return result
}

// nativeChatMessage preserves complete assistant packets and creates Chat Completions or Ollama tool results.
func nativeChatMessage(message Message, config ProviderConfig, fallback map[string]any) map[string]any {
	if blocks := storedProviderBlocks(message, config.Protocol); len(blocks) > 0 {
		return blocks[0]
	}
	if len(message.ToolCalls) > 0 {
		calls := []map[string]any{}
		for _, call := range message.ToolCalls {
			arguments := any(call.Arguments)
			if config.Protocol == "ollama" {
				arguments = nativeArguments(call)
			}
			item := map[string]any{"type": "function", "function": map[string]any{"name": call.Name, "arguments": arguments}}
			if config.Protocol != "ollama" {
				item["id"] = call.ID
			}
			calls = append(calls, item)
		}
		fallback["tool_calls"] = calls
	}
	if message.Role == "tool" {
		if config.Protocol == "ollama" {
			fallback["tool_name"] = message.ToolName
		} else {
			fallback["tool_call_id"] = message.ToolCallID
		}
	}
	return fallback
}

// nativeResponsesInput preserves reasoning, phase and function items under store=false.
func nativeResponsesInput(messages []Message, config ProviderConfig) []map[string]any {
	result := []map[string]any{}
	for _, message := range messages {
		if blocks := storedProviderBlocks(message, "responses"); len(blocks) > 0 {
			result = append(result, blocks...)
			continue
		}
		if message.Role == "tool" {
			result = append(result, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": message.Content})
			continue
		}
		if message.Content != "" || len(message.ToolCalls) == 0 {
			result = append(result, modelWireMessages([]Message{message}, config)...)
		}
		for _, call := range message.ToolCalls {
			result = append(result, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": call.Arguments})
		}
	}
	return result
}

// nativeAnthropicMessages keeps signed assistant blocks and adjacent tool results in the required user turn.
func nativeAnthropicMessages(messages []Message, config ProviderConfig) []map[string]any {
	result := []map[string]any{}
	previousTool := false
	for _, message := range messages {
		if message.Role == "tool" {
			block := map[string]any{"type": "tool_result", "tool_use_id": message.ToolCallID, "content": message.Content}
			if previousTool {
				last := result[len(result)-1]
				last["content"] = append(last["content"].([]map[string]any), block)
			} else {
				result = append(result, map[string]any{"role": "user", "content": []map[string]any{block}})
			}
			previousTool = true
			continue
		}
		previousTool = false
		blocks := storedProviderBlocks(message, "anthropic")
		if len(blocks) == 0 && len(message.ToolCalls) > 0 {
			if message.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": message.Content})
			}
			for _, call := range message.ToolCalls {
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": nativeArguments(call)})
			}
		}
		if len(blocks) > 0 {
			result = append(result, map[string]any{"role": message.Role, "content": blocks})
		} else {
			result = append(result, modelWireMessages([]Message{message}, config)...)
		}
	}
	return result
}

// nativeGeminiInput replays all Interactions steps locally, including thought signatures and function results.
func nativeGeminiInput(messages []Message) []map[string]any {
	result := []map[string]any{}
	for _, message := range messages {
		if blocks := storedProviderBlocks(message, "gemini"); len(blocks) > 0 {
			result = append(result, blocks...)
			continue
		}
		if message.Role == "tool" {
			result = append(result, map[string]any{"type": "function_result", "name": message.ToolName, "call_id": message.ToolCallID, "result": []map[string]string{{"type": "text", "text": message.Content}}})
			continue
		}
		kind := "user_input"
		if message.Role == "assistant" {
			kind = "model_output"
		}
		if message.Content != "" || len(message.ToolCalls) == 0 {
			result = append(result, map[string]any{"type": kind, "content": []map[string]string{{"type": "text", "text": message.Content}}})
		}
		for _, call := range message.ToolCalls {
			result = append(result, map[string]any{"type": "function_call", "id": call.ID, "name": call.Name, "arguments": nativeArguments(call)})
		}
	}
	return result
}

// nativeGeminiContents retains original function-call signatures and correlates function responses where IDs exist.
func nativeGeminiContents(messages []Message) []map[string]any {
	result := []map[string]any{}
	providerIDs := map[string]string{}
	for _, message := range messages {
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		parts := storedProviderBlocks(message, "gemini_generate")
		if len(parts) == 0 && message.Role != "tool" {
			if message.Content != "" || len(message.ToolCalls) == 0 {
				parts = append(parts, map[string]any{"text": message.Content})
			}
			for _, call := range message.ToolCalls {
				parts = append(parts, map[string]any{"functionCall": map[string]any{"name": call.Name, "args": nativeArguments(call)}})
			}
		}
		for _, call := range message.ToolCalls {
			providerIDs[call.ID] = call.ProviderCallID
		}
		if message.Role == "tool" {
			response := map[string]any{"name": message.ToolName, "response": map[string]string{"result": message.Content}}
			if id := providerIDs[message.ToolCallID]; id != "" {
				response["id"] = id
			}
			parts = []map[string]any{{"functionResponse": response}}
		}
		result = append(result, map[string]any{"role": role, "parts": parts})
	}
	return result
}

// compatibleToolHistory converts protocol-specific exchanges to attributable text when the model identity changes.
func compatibleToolHistory(messages []Message) []Message {
	result := append([]Message{}, messages...)
	for i := range result {
		message := &result[i]
		if len(message.ToolCalls) > 0 {
			parts := []string{message.Content}
			for _, call := range message.ToolCalls {
				parts = append(parts, call.Arguments)
			}
			message.Content = strings.TrimSpace(strings.Join(parts, "\n"))
			message.ToolCalls = nil
			message.reasoningContent, message.encryptedContent = "", ""
		}
		if message.Role == "tool" {
			message.Role = "user"
			message.ToolCallID, message.ToolName = "", ""
		}
		message.providerBlocks, message.providerFormat = nil, ""
	}
	return result
}

// validHistoryRole permits structured tool turns only in server-owned persisted conversations.
func validHistoryRole(message Message, owned bool) bool {
	if !owned && (len(message.ToolCalls) > 0 || message.ToolCallID != "") {
		return false
	}
	return message.Role == "user" || message.Role == "assistant" || owned && message.Role == "tool" && message.ToolCallID != ""
}
