package core

import (
	"errors"
	"fmt"
	"strings"
)

// publishReasoning emits provider-returned thought text or summaries while keeping signatures and encrypted replay opaque.
func (stream *modelStream) publishReasoning(kind string, value map[string]any) error {
	text := ""
	switch stream.config.Protocol {
	case "openai":
		delta := objectField(firstChoice(value, "choices"), "delta")
		text = textField(delta, "reasoning_content")
		if text == "" {
			text = textField(delta, "reasoning")
		}
	case "responses":
		if kind == "response.reasoning_summary_text.delta" || kind == "response.reasoning_summary_text.done" {
			if stream.publicSummaries == nil {
				stream.publicSummaries = map[string]string{}
			}
			key := fmt.Sprint(value["output_index"], "/", value["summary_index"])
			text = textField(value, "delta")
			if kind == "response.reasoning_summary_text.done" {
				full := textField(value, "text")
				text = ""
				if strings.HasPrefix(full, stream.publicSummaries[key]) {
					text = full[len(stream.publicSummaries[key]):]
				}
			}
			stream.publicSummaries[key] += text
		}
	case "anthropic":
		if kind == "content_block_start" {
			block := objectField(value, "content_block")
			if textField(block, "type") == "thinking" {
				text = textField(block, "thinking")
			}
		}
		if kind == "content_block_delta" && textField(objectField(value, "delta"), "type") == "thinking_delta" {
			text = textField(objectField(value, "delta"), "thinking")
		}
	case "ollama":
		text = textField(objectField(value, "message"), "thinking")
	case "gemini_generate":
		parts, _ := objectField(firstChoice(value, "candidates"), "content")["parts"].([]any)
		for _, part := range parts {
			item, _ := part.(map[string]any)
			if thought, _ := item["thought"].(bool); thought {
				text += textField(item, "text")
			}
		}
	case "gemini":
		if kind == "step.delta" {
			delta := objectField(value, "delta")
			if textField(delta, "type") == "thought_summary" {
				text = textField(delta, "text")
				if text == "" {
					text = textField(objectField(delta, "content"), "text")
				}
			}
		}
	}
	stream.publicReasoningBytes += len(text)
	if stream.text.Len()+stream.publicReasoningBytes+stream.encrypted.Len() > maxStreamBytes {
		return errors.New("The model response exceeds the size limit")
	}
	if text != "" && stream.config.onReasoning != nil {
		stream.config.onReasoning(text)
	}
	return nil
}
