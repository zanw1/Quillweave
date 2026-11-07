package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// nativeStreamState assembles complete assistant packets without publishing reasoning or signatures.
type nativeStreamState struct {
	calls     map[string]*NativeToolCall
	order     []string
	blocks    map[int]map[string]any
	fragments map[string]bool
	extra     map[string]any
	size      int
	nextPart  int
}

// initialize creates bounded per-response maps on the first native event.
func (state *nativeStreamState) initialize() {
	if state.calls != nil {
		return
	}
	state.calls, state.blocks = map[string]*NativeToolCall{}, map[int]map[string]any{}
	state.fragments, state.extra = map[string]bool{}, map[string]any{}
}

// call returns one stable accumulator for a provider's output index or item ID.
func (state *nativeStreamState) call(key string) (*NativeToolCall, error) {
	if call := state.calls[key]; call != nil {
		return call, nil
	}
	if len(state.calls) >= 16 {
		return nil, errors.New("The model returned too many tool calls in one response")
	}
	call := &NativeToolCall{}
	state.calls[key] = call
	state.order = append(state.order, key)
	return call, nil
}

// nativeIndex validates provider-owned block indexes before using them in a replay packet.
func nativeIndex(value map[string]any, key string) (int, error) {
	index, ok := value[key].(json.Number)
	if !ok {
		if _, exists := value[key]; exists {
			return 0, errors.New("The model returned an invalid output index")
		}
		return 0, nil
	}
	number, err := index.Int64()
	if err != nil || number < 0 || number > 127 {
		return 0, errors.New("The model returned an invalid output index")
	}
	return int(number), nil
}

// appendBlockText merges text deltas into one native content item while retaining any other fields.
func appendBlockText(block map[string]any, field, text string) {
	items, _ := block[field].([]any)
	if len(items) == 0 {
		items = []any{map[string]any{"type": "text", "text": ""}}
	}
	last, _ := items[len(items)-1].(map[string]any)
	if textField(last, "type") != "text" {
		last = map[string]any{"type": "text", "text": ""}
		items = append(items, last)
	}
	last["text"] = textField(last, "text") + text
	block[field] = items
}

// collect reads native function, reasoning and assistant events under the aggregate response-size guard.
func (state *nativeStreamState) collect(protocol, kind, data string, value map[string]any) error {
	state.initialize()
	state.size += len(data)
	if state.size > maxStreamBytes {
		return errors.New("The model's native response exceeds the size limit")
	}
	switch protocol {
	case "openai", "ollama":
		return state.chat(protocol, value)
	case "responses":
		return state.responses(kind, value)
	case "anthropic":
		return state.anthropic(kind, value)
	case "gemini":
		return state.gemini(kind, value)
	case "gemini_generate":
		return state.geminiGenerate(value)
	}
	return nil
}

// chat accumulates streamed Chat Completions arguments and complete Ollama function objects.
func (state *nativeStreamState) chat(protocol string, value map[string]any) error {
	delta := objectField(firstChoice(value, "choices"), "delta")
	if protocol == "ollama" {
		delta = objectField(value, "message")
	}
	for _, key := range []string{"reasoning", "thinking"} {
		if text := textField(delta, key); text != "" {
			state.extra[key] = textField(state.extra, key) + text
		}
	}
	if details, ok := delta["reasoning_details"].([]any); ok {
		previous, _ := state.extra["reasoning_details"].([]any)
		state.extra["reasoning_details"] = append(previous, details...)
	}
	items, _ := delta["tool_calls"].([]any)
	for _, entry := range items {
		item, _ := entry.(map[string]any)
		index, err := nativeIndex(item, "index")
		if err != nil {
			return err
		}
		key := fmt.Sprint(index)
		if protocol == "ollama" {
			key = fmt.Sprint(len(state.order))
		}
		call, err := state.call(key)
		if err != nil {
			return err
		}
		if id := textField(item, "id"); id != "" {
			call.ID, call.ProviderCallID = id, id
		}
		function := objectField(item, "function")
		call.Name += textField(function, "name")
		if protocol == "ollama" {
			call.Arguments = jsonText(function["arguments"])
		} else {
			call.Arguments += textField(function, "arguments")
		}
	}
	return nil
}

// responseItem records an authoritative Responses output item and its correlated function call.
func (state *nativeStreamState) responseItem(index int, item map[string]any) error {
	if index < 0 || index >= 128 {
		return errors.New("The model returned too many native output items")
	}
	if item == nil {
		return nil
	}
	state.blocks[index] = item
	if textField(item, "type") != "function_call" {
		return nil
	}
	call, err := state.call(fmt.Sprint(index))
	if err != nil {
		return err
	}
	call.ID, call.ProviderCallID = textField(item, "call_id"), textField(item, "call_id")
	call.Name, call.Arguments = textField(item, "name"), textField(item, "arguments")
	return nil
}

// responses preserves complete reasoning and message items, including encrypted content and assistant phase.
func (state *nativeStreamState) responses(kind string, value map[string]any) error {
	index, err := nativeIndex(value, "output_index")
	if err != nil {
		return err
	}
	switch kind {
	case "response.output_item.added", "response.output_item.done":
		return state.responseItem(index, objectField(value, "item"))
	case "response.function_call_arguments.delta":
		call, err := state.call(fmt.Sprint(index))
		if err != nil {
			return err
		}
		call.Arguments += textField(value, "delta")
	case "response.function_call_arguments.done":
		call, err := state.call(fmt.Sprint(index))
		if err != nil {
			return err
		}
		call.Arguments = textField(value, "arguments")
	case "response.completed":
		items, _ := objectField(value, "response")["output"].([]any)
		for i, raw := range items {
			item, _ := raw.(map[string]any)
			if err := state.responseItem(i, item); err != nil {
				return err
			}
		}
	}
	return nil
}

// anthropic retains signed thinking blocks and reconstructs tool input from partial_json fragments.
func (state *nativeStreamState) anthropic(kind string, value map[string]any) error {
	index, err := nativeIndex(value, "index")
	if err != nil {
		return err
	}
	key := fmt.Sprint(index)
	switch kind {
	case "content_block_start":
		block := objectField(value, "content_block")
		state.blocks[index] = block
		if textField(block, "type") == "tool_use" {
			call, err := state.call(key)
			if err != nil {
				return err
			}
			call.ID, call.ProviderCallID = textField(block, "id"), textField(block, "id")
			call.Name, call.Arguments = textField(block, "name"), jsonText(block["input"])
		}
	case "content_block_delta":
		block := state.blocks[index]
		if block == nil {
			return nil
		}
		delta := objectField(value, "delta")
		switch textField(delta, "type") {
		case "text_delta":
			block["text"] = textField(block, "text") + textField(delta, "text")
		case "thinking_delta":
			block["thinking"] = textField(block, "thinking") + textField(delta, "thinking")
		case "signature_delta":
			block["signature"] = textField(block, "signature") + textField(delta, "signature")
		case "input_json_delta":
			call, err := state.call(key)
			if err != nil {
				return err
			}
			if !state.fragments[key] {
				call.Arguments = ""
				state.fragments[key] = true
			}
			call.Arguments += textField(delta, "partial_json")
		}
	}
	return nil
}

// gemini assembles Interactions steps so stateless follow-ups keep the original thought signatures.
func (state *nativeStreamState) gemini(kind string, value map[string]any) error {
	index, err := nativeIndex(value, "index")
	if err != nil {
		return err
	}
	key := fmt.Sprint(index)
	switch kind {
	case "step.start":
		step := objectField(value, "step")
		state.blocks[index] = step
		if textField(step, "type") == "function_call" {
			call, err := state.call(key)
			if err != nil {
				return err
			}
			call.ID, call.ProviderCallID = textField(step, "id"), textField(step, "id")
			call.Name, call.Arguments = textField(step, "name"), jsonText(step["arguments"])
		}
	case "step.delta":
		step := state.blocks[index]
		if step == nil {
			return nil
		}
		delta := objectField(value, "delta")
		switch textField(delta, "type") {
		case "text":
			appendBlockText(step, "content", textField(delta, "text"))
		case "thought_signature":
			step["signature"] = textField(step, "signature") + textField(delta, "signature")
		case "thought_summary":
			appendBlockText(step, "summary", textField(objectField(delta, "content"), "text"))
		case "arguments_delta":
			call, err := state.call(key)
			if err != nil {
				return err
			}
			if !state.fragments[key] {
				call.Arguments = ""
				state.fragments[key] = true
			}
			call.Arguments += textField(delta, "arguments")
		}
	case "interaction.completed":
		items, _ := objectField(value, "interaction")["steps"].([]any)
		for i, raw := range items {
			if i >= 128 {
				return errors.New("The model returned too many native output steps")
			}
			step, _ := raw.(map[string]any)
			state.blocks[i] = step
			if textField(step, "type") == "function_call" {
				call, err := state.call(fmt.Sprint(i))
				if err != nil {
					return err
				}
				call.ID, call.ProviderCallID = textField(step, "id"), textField(step, "id")
				call.Name, call.Arguments = textField(step, "name"), jsonText(step["arguments"])
			}
		}
	}
	return nil
}

// geminiGenerate combines text chunks and retains complete signed function-call parts.
func (state *nativeStreamState) geminiGenerate(value map[string]any) error {
	parts, _ := objectField(firstChoice(value, "candidates"), "content")["parts"].([]any)
	for _, raw := range parts {
		part, _ := raw.(map[string]any)
		function := objectField(part, "functionCall")
		if function == nil {
			previous := state.blocks[state.nextPart-1]
			previousThought, _ := previous["thought"].(bool)
			currentThought, _ := part["thought"].(bool)
			if text, exists := part["text"].(string); exists && previous != nil && previous["text"] != nil && previousThought == currentThought && previous["thoughtSignature"] == nil {
				previous["text"] = textField(previous, "text") + text
				if signature, exists := part["thoughtSignature"]; exists {
					previous["thoughtSignature"] = signature
				}
				continue
			}
		}
		if state.nextPart >= 128 {
			return errors.New("The model returned too many native content parts")
		}
		index := state.nextPart
		state.nextPart++
		state.blocks[index] = part
		if function != nil {
			call, err := state.call(fmt.Sprint(index))
			if err != nil {
				return err
			}
			call.ID, call.ProviderCallID = textField(function, "id"), textField(function, "id")
			call.Name, call.Arguments = textField(function, "name"), jsonText(function["args"])
		}
	}
	return nil
}

// complete validates native call objects and freezes full provider packets for local replay.
func (state *nativeStreamState) complete(protocol, text, reasoning, encrypted string) ([]NativeToolCall, []json.RawMessage, error) {
	if state.calls == nil {
		return nil, nil, nil
	}
	calls := []NativeToolCall{}
	seen := map[string]bool{}
	for _, key := range state.order {
		call := state.calls[key]
		if call.ID == "" && (protocol == "ollama" || protocol == "gemini_generate") {
			call.ID = "call-" + newID()
		}
		var arguments map[string]any
		if call.ID == "" || seen[call.ID] || call.Name == "" || decodeJSON([]byte(call.Arguments), &arguments) != nil || arguments == nil {
			return nil, nil, errors.New("The model returned an incomplete or invalid native tool call")
		}
		seen[call.ID] = true
		calls = append(calls, *call)
		if protocol == "anthropic" || protocol == "gemini" {
			for index, block := range state.blocks {
				if textField(block, "id") == call.ID {
					field := "input"
					if protocol == "gemini" {
						field = "arguments"
					}
					state.blocks[index][field] = arguments
				}
			}
		}
		if protocol == "responses" {
			for _, block := range state.blocks {
				if textField(block, "call_id") == call.ID {
					block["arguments"] = call.Arguments
				}
			}
		}
	}
	if protocol == "openai" || protocol == "ollama" {
		packet := map[string]any{"role": "assistant", "content": text}
		for key, value := range state.extra {
			packet[key] = value
		}
		if reasoning != "" {
			packet["reasoning_content"] = reasoning
		}
		if encrypted != "" {
			packet["encrypted_content"] = encrypted
		}
		message := Message{Role: "assistant", Content: text, ToolCalls: calls}
		packet = nativeChatMessage(message, ProviderConfig{Protocol: protocol}, packet)
		return calls, []json.RawMessage{rawMessage(packet)}, nil
	}
	indexes := []int{}
	for index := range state.blocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	blocks := []json.RawMessage{}
	for _, index := range indexes {
		if state.blocks[index] != nil {
			blocks = append(blocks, rawMessage(state.blocks[index]))
		}
	}
	return calls, blocks, nil
}

// nativeToolsRejected recognizes explicit feature rejection without exposing provider response bodies.
func nativeToolsRejected(data string) bool {
	message := data
	if value, err := jsonObject(data); err == nil {
		failure := objectField(value, "error")
		message = textField(failure, "message")
		if message == "" {
			message = textField(value, "message")
		}
	}
	message = strings.ToLower(message)
	tool := strings.Contains(message, "tool") || strings.Contains(message, "function calling")
	unsupported := strings.Contains(message, "not support") || strings.Contains(message, "unsupported") || strings.Contains(message, "not enabled") || strings.Contains(message, "unknown parameter") || strings.Contains(message, "unrecognized request argument")
	return tool && unsupported
}
