package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const maxStreamBytes = 32_000_000
const maxStreamFrameBytes = 8_000_000

type modelOutput struct {
	Text      string
	Reasoning string
	Encrypted string
}

type modelStream struct {
	config           ProviderConfig
	event            string
	data             []string
	frameBytes       int
	done             bool
	sseStarted       bool
	anthropicStopped bool
	steps            map[string]string
	text             strings.Builder
	reasoning        strings.Builder
	encrypted        strings.Builder
	onText           func(string)
}

// streamStopError accepts successful text endings and reports truncation, refusal, or tool requirements.
func streamStopError(reason string) error {
	switch strings.ToLower(reason) {
	case "", "stop", "end_turn", "stop_sequence", "completed":
		return nil
	case "length", "max_tokens", "max_output_tokens", "max_completion_tokens":
		return errors.New("The model output was truncated. Received text is retained. Increase the total output budget and try again")
	case "content_filter", "safety", "sensitive", "recitation", "blocklist", "prohibited_content", "refusal":
		return errors.New("The model filtered or refused the output. Review the request before trying again")
	case "tool_calls", "tool_use", "function_call", "requires_action", "requires_continuation", "pause_turn":
		return errors.New("The model requested a tool or further execution instead of completing the text response")
	default:
		return errors.New("The model ended in an unsuccessful state")
	}
}

// jsonObject requires one valid UTF-8 JSON object with no trailing data.
func jsonObject(raw string) (map[string]any, error) {
	var value map[string]any
	if err := decodeJSON([]byte(raw), &value); err != nil || value == nil {
		return nil, errors.New("The model stream contains an invalid JSON object")
	}
	return value, nil
}

// objectField reads optional provider objects as data.
func objectField(value map[string]any, key string) map[string]any {
	result, _ := value[key].(map[string]any)
	return result
}

// textField accepts string fields for prose extraction.
func textField(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}

// firstChoice selects the first result under the single-candidate text contract.
func firstChoice(value map[string]any, key string) map[string]any {
	items, _ := value[key].([]any)
	if len(items) == 0 {
		return nil
	}
	result, _ := items[0].(map[string]any)
	return result
}

// parseLine parses Chat Completions or Ollama lines and rejects unsuccessful stop reasons.
func parseLine(line, protocol string) (string, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, ":") {
		return "", false, nil
	}
	if protocol == "openai" {
		if !strings.HasPrefix(line, "data:") {
			return "", false, nil
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if line == "[DONE]" {
			return "", true, nil
		}
	}
	value, err := jsonObject(line)
	if err != nil {
		return "", false, err
	}
	if value["error"] != nil {
		return "", false, errors.New("The model service returned an error. Check the model, quota, and service status")
	}
	if protocol == "ollama" {
		message := objectField(value, "message")
		done, _ := value["done"].(bool)
		return textField(message, "content"), done, streamStopError(textField(value, "done_reason"))
	}
	choice := firstChoice(value, "choices")
	reason := textField(choice, "finish_reason")
	return textField(objectField(choice, "delta"), "content"), reason == "stop", streamStopError(reason)
}

// appendText checks the aggregate limit before publishing answer-text increments.
func (stream *modelStream) appendText(text string) error {
	if stream.text.Len()+stream.reasoning.Len()+stream.encrypted.Len()+len(text) > maxStreamBytes {
		return errors.New("The model response exceeds the size limit")
	}
	if text != "" {
		stream.text.WriteString(text)
		stream.onText(text)
	}
	return nil
}

// appendContext collects supported private context within the shared response-size budget.
func (stream *modelStream) appendContext(reasoning, encrypted string) error {
	if !preservesProviderContext(stream.config) {
		return nil
	}
	if stream.text.Len()+stream.reasoning.Len()+stream.encrypted.Len()+len(reasoning)+len(encrypted) > maxStreamBytes {
		return errors.New("The model response exceeds the size limit")
	}
	stream.reasoning.WriteString(reasoning)
	stream.encrypted.WriteString(encrypted)
	return nil
}

// payload decodes complete events and uses provider terminal states to determine completion.
func (stream *modelStream) payload(event, data string) error {
	data = strings.TrimSpace(data)
	if data == "" {
		return nil
	}
	if data == "[DONE]" {
		if stream.config.Protocol == "openai" {
			stream.done = true
			return nil
		}
		if stream.done {
			return nil
		}
		return errors.New("The model stream is missing a successful completion event")
	}
	value, err := jsonObject(data)
	if err != nil {
		return err
	}
	if value["error"] != nil {
		return errors.New("The model stream returned an error. Check the model, quota, and service status")
	}
	if code, ok := objectField(value, "base_resp")["status_code"].(json.Number); ok && code.String() != "0" {
		return errors.New("The model service returned an error status")
	}
	kind := textField(value, "type")
	if kind == "" {
		kind = textField(value, "event_type")
	}
	if kind == "" {
		kind = event
	}
	switch stream.config.Protocol {
	case "openai":
		choice := firstChoice(value, "choices")
		delta := objectField(choice, "delta")
		if err := stream.appendContext(textField(delta, "reasoning_content"), textField(delta, "encrypted_content")); err != nil {
			return err
		}
		text, done, parseErr := parseLine("data: "+data, "openai")
		if err := stream.appendText(text); err != nil {
			return err
		}
		if parseErr != nil {
			return parseErr
		}
		stream.done = stream.done || done
	case "ollama":
		text, done, parseErr := parseLine(data, "ollama")
		if err := stream.appendText(text); err != nil {
			return err
		}
		if parseErr != nil {
			return parseErr
		}
		stream.done = stream.done || done
	case "responses":
		switch kind {
		case "response.output_text.delta":
			return stream.appendText(textField(value, "delta"))
		case "response.completed":
			response := objectField(value, "response")
			if textField(response, "status") != "completed" || response["error"] != nil {
				return errors.New("The model did not complete the response successfully")
			}
			stream.done = true
		case "response.incomplete":
			return errors.New("The model response is incomplete. It may have reached the output budget or been filtered")
		case "response.failed", "error":
			return errors.New("The model service returned a streaming error")
		case "response.refusal.delta", "response.refusal.done":
			return errors.New("The model refused to generate this content")
		}
	case "anthropic":
		switch kind {
		case "content_block_start":
			block := objectField(value, "content_block")
			if textField(block, "type") == "text" {
				return stream.appendText(textField(block, "text"))
			}
		case "content_block_delta":
			delta := objectField(value, "delta")
			if textField(delta, "type") == "text_delta" {
				return stream.appendText(textField(delta, "text"))
			}
		case "message_delta":
			delta := objectField(value, "delta")
			if textField(objectField(delta, "stop_details"), "type") == "refusal" {
				return streamStopError("refusal")
			}
			reason := textField(delta, "stop_reason")
			if err := streamStopError(reason); err != nil {
				return err
			}
			stream.anthropicStopped = reason == "end_turn" || reason == "stop_sequence"
		case "message_stop":
			if !stream.anthropicStopped {
				return errors.New("The Claude stream is missing a successful stop reason")
			}
			stream.done = true
		case "error":
			return errors.New("The Claude API returned a streaming error")
		}
	case "gemini":
		return stream.geminiEvent(kind, value)
	case "gemini_generate":
		feedback := objectField(value, "promptFeedback")
		if reason := textField(feedback, "blockReason"); reason != "" {
			return errors.New("Gemini refused this input")
		}
		candidate := firstChoice(value, "candidates")
		parts, _ := objectField(candidate, "content")["parts"].([]any)
		for _, part := range parts {
			item, _ := part.(map[string]any)
			thought, _ := item["thought"].(bool)
			if !thought {
				if err := stream.appendText(textField(item, "text")); err != nil {
					return err
				}
			}
		}
		reason := textField(candidate, "finishReason")
		if err := streamStopError(reason); err != nil {
			return err
		}
		stream.done = stream.done || reason == "STOP"
	}
	return nil
}

// geminiEvent extracts model_output text from Interactions steps.
func (stream *modelStream) geminiEvent(kind string, value map[string]any) error {
	index := fmt.Sprint(value["index"])
	switch kind {
	case "step.start":
		step := objectField(value, "step")
		stream.steps[index] = textField(step, "type")
		if textField(step, "type") == "function_call" {
			return streamStopError("requires_action")
		}
	case "step.delta":
		delta := objectField(value, "delta")
		if textField(delta, "type") == "text" {
			if stream.steps[index] != "model_output" {
				return errors.New("Gemini text belongs to an undeclared output step")
			}
			return stream.appendText(textField(delta, "text"))
		}
	case "interaction.status_update":
		status := textField(value, "status")
		if status != "" && status != "in_progress" && status != "completed" {
			return streamStopError(status)
		}
	case "interaction.completed":
		interaction := objectField(value, "interaction")
		if textField(interaction, "status") != "completed" {
			return errors.New("The Gemini interaction did not complete successfully")
		}
		stream.done = true
	case "interaction.failed", "interaction.incomplete", "error":
		return errors.New("Gemini returned a failed or incomplete result")
	}
	return nil
}

// flush joins multiline data into one SSE event and resets frame state.
func (stream *modelStream) flush() error {
	if len(stream.data) == 0 {
		stream.event = ""
		stream.frameBytes = 0
		return nil
	}
	event, data := stream.event, strings.Join(stream.data, "\n")
	stream.event, stream.data, stream.frameBytes = "", nil, 0
	return stream.payload(event, data)
}

// line handles one SSE or NDJSON line and strips only the initial SSE BOM.
func (stream *modelStream) line(line string) error {
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if stream.config.Protocol == "ollama" {
		if strings.TrimSpace(line) == "" {
			return nil
		}
		return stream.payload("", line)
	}
	if !stream.sseStarted {
		stream.sseStarted = true
		line = strings.TrimPrefix(line, "\ufeff")
	}
	if line == "" {
		return stream.flush()
	}
	if strings.HasPrefix(line, ":") {
		return nil
	}
	field, value, found := strings.Cut(line, ":")
	if !found {
		value = ""
	}
	value = strings.TrimPrefix(value, " ")
	switch field {
	case "event":
		stream.event = value
	case "data":
		stream.frameBytes += len(value) + 1
		if stream.frameBytes > maxStreamFrameBytes {
			return errors.New("A model stream event exceeds the size limit")
		}
		stream.data = append(stream.data, value)
	}
	return nil
}

// streamModel returns the established text result while private context uses the dedicated output path.
func streamModel(ctx context.Context, config ProviderConfig, messages []Message, onText func(string)) (string, error) {
	output, err := streamModelOutput(ctx, config, messages, onText)
	return output.Text, err
}

// streamModelOutput sends cancellable requests with redirect rejection and validates complete output.
func streamModelOutput(ctx context.Context, config ProviderConfig, messages []Message, onText func(string)) (modelOutput, error) {
	endpoint, err := validateConfig(config)
	if err != nil {
		return modelOutput{}, err
	}
	if ctx.Err() != nil {
		return modelOutput{}, errors.New("Task cancelled")
	}
	body, headers, err := modelRequest(config, messages)
	if err != nil {
		return modelOutput{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(jsonText(body)))
	if err != nil {
		return modelOutput{}, errors.New("Unable to create the model request. Check the API URL")
	}
	request.Header = headers
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Duration(config.TimeoutSeconds) * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return modelOutput{}, errors.New("Task cancelled")
		}
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			return modelOutput{}, errors.New("Model connection timed out")
		}
		return modelOutput{}, errors.New("Unable to connect to the model service. Check the API URL and network")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return modelOutput{}, modelHTTPError(response.StatusCode)
	}
	stream := modelStream{config: config, onText: onText, steps: map[string]string{}}
	pending := []byte{}
	buffer := make([]byte, 32768)
	sse := config.Protocol != "ollama"
	afterCR := false
	for {
		if ctx.Err() != nil {
			return modelOutput{}, errors.New("Task cancelled")
		}
		count, readErr := response.Body.Read(buffer)
		pending = append(pending, buffer[:count]...)
		if len(pending) > maxStreamFrameBytes {
			return modelOutput{}, errors.New("The model response exceeds the size limit")
		}
		for {
			// A CR ended the previous line. Skip its following LF even across read boundaries.
			if afterCR && len(pending) > 0 {
				if pending[0] == '\n' {
					pending = pending[1:]
				}
				afterCR = false
			}
			var end int
			if sse {
				end = bytes.IndexAny(pending, "\r\n")
			} else {
				end = bytes.IndexByte(pending, '\n')
			}
			if end < 0 {
				break
			}
			line := pending[:end]
			if !utf8.Valid(line) {
				return modelOutput{}, errors.New("The model response is not UTF-8")
			}
			if err := stream.line(string(line)); err != nil {
				return modelOutput{}, err
			}
			afterCR = sse && pending[end] == '\r'
			pending = pending[end+1:]
		}
		if stream.done {
			break
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return modelOutput{}, errors.New("Task cancelled")
			}
			if readErr != io.EOF {
				return modelOutput{}, errors.New("The model stream was interrupted or timed out")
			}
			break
		}
	}
	if len(pending) > 0 && !stream.done {
		if !utf8.Valid(pending) {
			return modelOutput{}, errors.New("The model response is not UTF-8")
		}
		if err := stream.line(string(pending)); err != nil {
			return modelOutput{}, err
		}
	}
	if !stream.done {
		if err := stream.flush(); err != nil {
			return modelOutput{}, err
		}
	}
	if !stream.done {
		return modelOutput{}, errors.New("The model connection ended before a completion marker arrived")
	}
	if strings.TrimSpace(stream.text.String()) == "" {
		return modelOutput{}, errors.New("The model returned empty text")
	}
	return modelOutput{Text: stream.text.String(), Reasoning: stream.reasoning.String(), Encrypted: stream.encrypted.String()}, nil
}

// modelHTTPError returns actionable HTTP errors while excluding raw provider response bodies.
func modelHTTPError(status int) error {
	switch status {
	case 400, 422:
		return fmt.Errorf("Model service returned HTTP %d. Check the provider preset, model, API version, and generation parameters", status)
	case 401, 403:
		return fmt.Errorf("Model service returned HTTP %d. Check the API key, region, and model access", status)
	case 404:
		return fmt.Errorf("Model service returned HTTP %d. Check the endpoint and model name. Ollama requires an installed model", status)
	case 429:
		return fmt.Errorf("Model service returned HTTP %d. Quota or rate limit reached. Try again later", status)
	default:
		return fmt.Errorf("Model service returned HTTP %d. Check service status and the API URL", status)
	}
}
