package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ModelUsage keeps unknown usage absent while retaining explicit provider zero values.
type ModelUsage struct {
	InputTokens      *int64 `json:"input_tokens,omitempty"`
	OutputTokens     *int64 `json:"output_tokens,omitempty"`
	ReasoningTokens  *int64 `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  *int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64 `json:"cache_write_tokens,omitempty"`
}

type ModelCallMetric struct {
	Purpose             string     `json:"purpose"`
	Model               string     `json:"model"`
	ProfileID           string     `json:"profile_id,omitempty"`
	ProviderID          string     `json:"provider_id,omitempty"`
	ReasoningEffort     string     `json:"reasoning_effort,omitempty"`
	MaxOutputTokens     uint32     `json:"max_output_tokens,omitempty"`
	Protocol            string     `json:"protocol"`
	Usage               ModelUsage `json:"usage"`
	DurationMS          int64      `json:"duration_ms"`
	FirstTextMS         *int64     `json:"first_text_ms,omitempty"`
	InputChars          int        `json:"input_chars"`
	PrefixHash          string     `json:"prefix_hash"`
	RequestHash         string     `json:"request_hash"`
	Succeeded           bool       `json:"succeeded"`
	ConfigHash          string     `json:"config_hash,omitempty"`
	SharedPrefixBytes   *int       `json:"shared_prefix_bytes,omitempty"`
	FirstChangedMessage *int       `json:"first_changed_message,omitempty"`
	ChangeReason        string     `json:"change_reason,omitempty"`
	RequestChangePath   string     `json:"request_change_path,omitempty"`
}

// compressRequestBody keeps an exact request snapshot inside the private continuation record with bounded disk overhead.
func compressRequestBody(body string) string {
	if body == "" {
		return ""
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(body)); err != nil {
		return ""
	}
	if err := writer.Close(); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(compressed.Bytes())
}

// expandRequestBody bounds both encoded and expanded snapshots before using them in diagnostics.
func expandRequestBody(encoded string) (string, error) {
	if len(encoded) > maxStreamBytes*2 {
		return "", errors.New("Stored request snapshot exceeds its size limit")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, maxStreamBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxStreamBytes {
		return "", errors.New("Stored request snapshot exceeds its size limit")
	}
	return string(body), nil
}

// seedRequestComparison recovers the previous main request from the completed exchange's saved messages.
func (engine *Engine) seedRequestComparison(job *Job, request AIRequest) {
	for i := len(request.History) - 1; i >= 0; i-- {
		id := request.History[i].SourceJobID
		if id == "" {
			continue
		}
		previous, err := engine.loadStoredJob(id)
		if err != nil || previous.ProjectID != job.ProjectID || previous.Status != "done" {
			return
		}
		for j := len(previous.Metrics) - 1; j >= 0; j-- {
			metric := previous.Metrics[j]
			if metric.Purpose == "agent" || metric.Purpose == "generation" {
				job.lastRequestConfig = metric.ConfigHash
				break
			}
		}
		if previous.providerContext != nil && previous.providerContext.LastRequestBody != "" {
			job.lastRequestBody, _ = expandRequestBody(previous.providerContext.LastRequestBody)
		}
		return
	}
}

// compareModelRequest records the exact serialized-body prefix and first differing message for comparable configurations.
func compareModelRequest(metric *ModelCallMetric, before, after, previousConfig string) {
	if metric.Purpose == "compaction" {
		metric.ChangeReason = "Conversation summary"
		return
	}
	if metric.Purpose != "agent" && metric.Purpose != "generation" {
		metric.ChangeReason = "Scoped task request"
		return
	}
	if before == "" || after == "" || previousConfig == "" {
		metric.ChangeReason = "First comparable request"
		return
	}
	if previousConfig != metric.ConfigHash {
		metric.ChangeReason = "Request configuration changed"
		metric.RequestChangePath = "configuration"
		return
	}
	beforeBody, beforeErr := jsonObject(before)
	afterBody, afterErr := jsonObject(after)
	if beforeErr != nil || afterErr != nil {
		return
	}
	shared := 0
	for shared < len(before) && shared < len(after) && before[shared] == after[shared] {
		shared++
	}
	metric.SharedPrefixBytes = &shared
	metric.ChangeReason = "Identical request"
	for _, key := range []string{"system", "systemInstruction", "system_instruction", "messages", "input", "contents"} {
		if jsonText(beforeBody[key]) == jsonText(afterBody[key]) {
			continue
		}
		metric.RequestChangePath, metric.ChangeReason = key, "Message content changed"
		oldItems, oldOK := beforeBody[key].([]any)
		newItems, newOK := afterBody[key].([]any)
		if !oldOK || !newOK {
			return
		}
		for i := 0; i < min(len(oldItems), len(newItems)); i++ {
			if jsonText(oldItems[i]) != jsonText(newItems[i]) {
				position := i + 1
				metric.FirstChangedMessage = &position
				metric.RequestChangePath = fmt.Sprintf("%s[%d]", key, i)
				return
			}
		}
		metric.RequestChangePath = fmt.Sprintf("%s[%d]", key, min(len(oldItems), len(newItems)))
		metric.ChangeReason = "History appended"
		if len(newItems) < len(oldItems) {
			metric.ChangeReason = "History shortened"
		}
		return
	}
}

// usageCount accepts nonnegative integral counters without conflating missing values with zero.
func usageCount(value map[string]any, key string) *int64 {
	number, ok := value[key].(json.Number)
	if !ok {
		return nil
	}
	count, err := number.Int64()
	if err != nil || count < 0 {
		return nil
	}
	return &count
}

// setUsageCount merges cumulative streaming usage without erasing an earlier known counter.
func setUsageCount(target **int64, value map[string]any, key string) {
	if count := usageCount(value, key); count != nil {
		*target = count
	}
}

// readUsage normalizes provider counters, including Claude's separate uncached and cached input.
func (stream *modelStream) readUsage(value map[string]any) {
	usage := objectField(value, "usage")
	switch stream.config.Protocol {
	case "responses":
		usage = objectField(objectField(value, "response"), "usage")
		setUsageCount(&stream.usage.InputTokens, usage, "input_tokens")
		setUsageCount(&stream.usage.OutputTokens, usage, "output_tokens")
		setUsageCount(&stream.usage.ReasoningTokens, objectField(usage, "output_tokens_details"), "reasoning_tokens")
		details := objectField(usage, "input_tokens_details")
		setUsageCount(&stream.usage.CacheReadTokens, details, "cached_tokens")
		setUsageCount(&stream.usage.CacheWriteTokens, details, "cache_write_tokens")
	case "openai":
		setUsageCount(&stream.usage.InputTokens, usage, "prompt_tokens")
		setUsageCount(&stream.usage.OutputTokens, usage, "completion_tokens")
		setUsageCount(&stream.usage.ReasoningTokens, objectField(usage, "completion_tokens_details"), "reasoning_tokens")
		details := objectField(usage, "prompt_tokens_details")
		setUsageCount(&stream.usage.CacheReadTokens, details, "cached_tokens")
		setUsageCount(&stream.usage.CacheWriteTokens, details, "cache_write_tokens")
		setUsageCount(&stream.usage.CacheReadTokens, usage, "prompt_cache_hit_tokens")
	case "anthropic":
		if start := objectField(objectField(value, "message"), "usage"); start != nil {
			usage = start
		}
		setUsageCount(&stream.uncachedInput, usage, "input_tokens")
		setUsageCount(&stream.usage.OutputTokens, usage, "output_tokens")
		setUsageCount(&stream.usage.CacheReadTokens, usage, "cache_read_input_tokens")
		setUsageCount(&stream.usage.CacheWriteTokens, usage, "cache_creation_input_tokens")
		if stream.uncachedInput != nil {
			total := *stream.uncachedInput
			for _, count := range []*int64{stream.usage.CacheReadTokens, stream.usage.CacheWriteTokens} {
				if count != nil {
					total += *count
				}
			}
			stream.usage.InputTokens = &total
		}
	case "gemini_generate":
		usage = objectField(value, "usageMetadata")
		setUsageCount(&stream.usage.InputTokens, usage, "promptTokenCount")
		stream.readSeparateReasoningUsage(usage, "candidatesTokenCount", "thoughtsTokenCount")
		setUsageCount(&stream.usage.CacheReadTokens, usage, "cachedContentTokenCount")
	case "gemini":
		if final := objectField(objectField(value, "interaction"), "usage"); final != nil {
			usage = final
		}
		setUsageCount(&stream.usage.InputTokens, usage, "total_input_tokens")
		stream.readSeparateReasoningUsage(usage, "total_output_tokens", "total_thought_tokens")
		setUsageCount(&stream.usage.CacheReadTokens, usage, "total_cached_tokens")
	case "ollama":
		setUsageCount(&stream.usage.InputTokens, value, "prompt_eval_count")
		setUsageCount(&stream.usage.OutputTokens, value, "eval_count")
	}
}

// readSeparateReasoningUsage counts thought tokens once when a provider reports them outside visible output.
func (stream *modelStream) readSeparateReasoningUsage(usage map[string]any, outputKey, reasoningKey string) {
	setUsageCount(&stream.outputWithoutReasoning, usage, outputKey)
	setUsageCount(&stream.usage.ReasoningTokens, usage, reasoningKey)
	if stream.outputWithoutReasoning != nil {
		total := *stream.outputWithoutReasoning
		if stream.usage.ReasoningTokens != nil {
			total += *stream.usage.ReasoningTokens
		}
		stream.usage.OutputTokens = &total
	}
}

// streamJobModel records each real request's usage, latency and byte-stable prefix fingerprint.
func (engine *Engine) streamJobModel(jobID, purpose string, ctx context.Context, config ProviderConfig, messages []Message, onText func(string), profileIDs ...string) (modelOutput, error) {
	config, budgetErr := applyRequestTokenBudget(config, messages)
	if budgetErr != nil {
		return modelOutput{}, budgetErr
	}
	metric := ModelCallMetric{Purpose: purpose, Model: config.Model, Protocol: config.Protocol, ProviderID: config.ProviderID, ReasoningEffort: config.ReasoningEffort, MaxOutputTokens: config.MaxTokens, InputChars: messageChars(messages) + nativeToolChars(config)}
	if len(profileIDs) > 0 {
		metric.ProfileID = profileIDs[0]
	}
	configuration, _, _ := modelRequest(config, nil)
	metric.ConfigHash = hashText(jsonText([]any{config.ProviderID, config.Protocol, config.BaseURL, configuration}))
	bodyText := ""
	if body, _, err := modelRequest(config, messages); err == nil {
		bodyText = jsonText(body)
		metric.RequestHash = hashText(bodyText)
	}
	engine.jobsMu.Lock()
	if job := engine.jobs[jobID]; job != nil {
		compareModelRequest(&metric, job.lastRequestBody, bodyText, job.lastRequestConfig)
		if purpose == "agent" || purpose == "generation" {
			job.lastRequestBody = bodyText
			job.lastRequestConfig = metric.ConfigHash
		}
	}
	engine.jobsMu.Unlock()
	if len(messages) > 0 {
		metric.PrefixHash = hashText(jsonText(modelWireMessages(messages[:1], config)))
	}
	engine.noteModelCall(jobID)
	started := time.Now()
	reasoningID, textID := newID(), newID()
	var reasoning, raw strings.Builder
	visible := ""
	config.onReasoning = func(delta string) {
		reasoning.WriteString(delta)
		engine.updateChatActivity(jobID, ChatActivity{ID: reasoningID, Kind: "reasoning", Text: reasoning.String(), Purpose: purpose, Status: "running"})
	}
	output, err := streamModelOutput(ctx, config, messages, func(text string) {
		if text != "" && metric.FirstTextMS == nil {
			elapsed := time.Since(started).Milliseconds()
			metric.FirstTextMS = &elapsed
		}
		onText(text)
		if contains([]string{"agent", "generation", "writing_draft", "writing_composition", "writing_review"}, purpose) {
			raw.WriteString(text)
			visible = agentProsePrefix(raw.String())
			engine.updateChatActivity(jobID, ChatActivity{ID: textID, Kind: "text", Text: visible, Purpose: purpose, Status: "running"})
		}
	})
	status := "done"
	if err != nil {
		status = "interrupted"
	} else if raw.Len() > 0 {
		visible = visibleAgentOutput(output.Text)
	}
	engine.updateChatActivity(jobID, ChatActivity{ID: reasoningID, Kind: "reasoning", Text: reasoning.String(), Purpose: purpose, Status: status})
	engine.updateChatActivity(jobID, ChatActivity{ID: textID, Kind: "text", Text: visible, Purpose: purpose, Status: status})
	metric.DurationMS, metric.Usage, metric.Succeeded = time.Since(started).Milliseconds(), output.Usage, err == nil
	engine.jobsMu.Lock()
	if job := engine.jobs[jobID]; job != nil {
		job.Metrics = append(job.Metrics, metric)
	}
	engine.jobsMu.Unlock()
	return output, err
}
