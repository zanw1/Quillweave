package core

// tokenUsageBaseline retains one provider-counted input snapshot for an unchanged request prefix.
type tokenUsageBaseline struct {
	Body        map[string]any
	InputTokens uint64
}

// withTokenUsageBaseline recovers measured usage only for a completed request on the same model and connection.
func (engine *Engine) withTokenUsageBaseline(config ProviderConfig, request AIRequest) ProviderConfig {
	if request.ModelSelection == nil {
		return config
	}
	for i := len(request.History) - 1; i >= 0; i-- {
		id := request.History[i].SourceJobID
		if id == "" {
			continue
		}
		previous, err := engine.loadStoredJob(id)
		if err != nil || previous.ProjectID != request.ProjectID || previous.Status != "done" || previous.providerContext == nil || !providerContextMatches(*previous.providerContext, config, request.ModelSelection.ProfileID) {
			return config
		}
		bodyText, err := expandRequestBody(previous.providerContext.LastRequestBody)
		if err != nil {
			return config
		}
		var body map[string]any
		if decodeJSON([]byte(bodyText), &body) != nil {
			return config
		}
		for j := len(previous.Metrics) - 1; j >= 0; j-- {
			metric := previous.Metrics[j]
			if metric.Purpose != "agent" && metric.Purpose != "generation" {
				continue
			}
			if metric.Succeeded && metric.Model == config.Model && metric.Protocol == config.Protocol && metric.Usage.InputTokens != nil && *metric.Usage.InputTokens > 0 {
				config.tokenBaseline = &tokenUsageBaseline{Body: body, InputTokens: uint64(*metric.Usage.InputTokens)}
			}
			return config
		}
		return config
	}
	return config
}

// calibratedInputTokens reuses provider usage only when every prior input item and static tool field still matches.
func calibratedInputTokens(config ProviderConfig, messages []Message) (uint64, bool) {
	baseline := config.tokenBaseline
	if baseline == nil || baseline.InputTokens == 0 {
		return 0, false
	}
	body, _, err := modelRequest(config, messages)
	if err != nil {
		return 0, false
	}
	for _, key := range []string{"model", "system", "systemInstruction", "system_instruction", "tools", "tool_choice", "parallel_tool_calls"} {
		if jsonText(body[key]) != jsonText(baseline.Body[key]) {
			return 0, false
		}
	}
	// Normalize concrete map slices from modelRequest to the decoded form used by stored snapshots.
	var normalized map[string]any
	if decodeJSON([]byte(jsonText(body)), &normalized) != nil {
		return 0, false
	}
	for _, key := range []string{"messages", "input", "contents"} {
		previous, exists := baseline.Body[key].([]any)
		if !exists {
			continue
		}
		current, ok := normalized[key].([]any)
		if !ok || len(current) < len(previous) {
			return 0, false
		}
		for i := range previous {
			if jsonText(current[i]) != jsonText(previous[i]) {
				return 0, false
			}
		}
		tokens := baseline.InputTokens
		for _, item := range current[len(previous):] {
			tokens += estimateTextTokens(jsonText(item)) + 6
		}
		return tokens, true
	}
	return 0, false
}
