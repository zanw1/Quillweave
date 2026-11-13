package core

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
)

// ChatModelSelection is the credential-free model intent accepted with one author submission.
type ChatModelSelection struct {
	ProfileID       string `json:"profile_id"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	OutputMode      string `json:"output_mode"`
	MaxTokens       uint32 `json:"max_tokens,omitempty"`
}

// TokenBudget describes the same estimated input and output decisions used by generation.
type TokenBudget struct {
	ContextWindowTokens uint64 `json:"context_window_tokens"`
	InputTokensEstimate uint64 `json:"input_tokens_estimate"`
	OutputTokens        uint32 `json:"output_tokens"`
	MaxOutputTokens     uint32 `json:"max_output_tokens"`
	CanSend             bool   `json:"can_send"`
	NeedsCompaction     bool   `json:"needs_compaction"`
	Estimated           bool   `json:"estimated"`
	Warning             string `json:"warning,omitempty"`
	Source              string `json:"source"`
	MaxInputTokens      uint64 `json:"max_input_tokens,omitempty"`
	UsageCalibrated     bool   `json:"usage_calibrated,omitempty"`
}

// CarriedRecord identifies one record actually included in the prepared author context.
type CarriedRecord struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// copyChatModelSelection isolates accepted settings from subsequent composer and session edits.
func copyChatModelSelection(selection *ChatModelSelection) *ChatModelSelection {
	if selection == nil {
		return nil
	}
	copy := *selection
	return &copy
}

// selectChatModel resolves explicit submission intent before session defaults and legacy task routing.
func (models ModelSettings) selectChatModel(project *Project, request AIRequest, task string) (SelectedModel, *ChatModelSelection, error) {
	selection := copyChatModelSelection(request.ModelSelection)
	if selection == nil && request.conversation != nil {
		selection = copyChatModelSelection(request.conversation.ModelSelection)
	}
	if selection == nil {
		selected, err := models.Select(project, task)
		return selected, nil, err
	}
	profile, err := models.profile(selection.ProfileID)
	if err != nil {
		return SelectedModel{}, nil, err
	}
	config := profile.Config
	if config.ModelLimits != nil {
		limits := *config.ModelLimits
		config.ModelLimits = &limits
	}
	if selection.OutputMode == "" {
		selection.OutputMode = "auto"
	}
	if selection.OutputMode != "auto" && selection.OutputMode != "custom" {
		return SelectedModel{}, nil, errors.New("Choose automatic or custom output tokens")
	}
	if selection.ReasoningEffort != "" {
		config.ReasoningEffort = selection.ReasoningEffort
		rules, _ := providerRules(config)
		if selection.ReasoningEffort == "none" && contains(rules.ThinkingModes, "disabled") {
			config.ThinkingMode = "disabled"
			if !contains(rules.ReasoningEfforts, "none") {
				config.ReasoningEffort = ""
			}
		} else if selection.ReasoningEffort != "none" && contains(rules.ThinkingModes, "enabled") {
			config.ThinkingMode = "enabled"
		}
		if !sendsTemperature(config, rules) {
			config.Temperature = nil
		}
	}
	limits := effectiveModelLimits(config)
	config.chatOutputAuto = selection.OutputMode == "auto"
	if config.chatOutputAuto {
		if limits.MaxOutputTokens > 0 {
			config.MaxTokens = min(limits.MaxOutputTokens, maxOutputBudget)
		}
		selection.MaxTokens = 0
	} else {
		if selection.MaxTokens == 0 {
			return SelectedModel{}, nil, errors.New("Enter a positive custom output token limit")
		}
		config.MaxTokens = selection.MaxTokens
	}
	if limits.ContextWindowTokens > 0 {
		if config.chatOutputAuto {
			config.MaxTokens = min(config.MaxTokens, uint32(min(limits.ContextWindowTokens, math.MaxUint32)))
		}
		// Initial assembly accepts text-heavy English; the completed prompt derives its exact character threshold below.
		config.ContextChars = min(uint64(maxContextCharacters), min(limits.ContextWindowTokens, uint64(maxContextCharacters))*4)
	} else if limits.MaxInputTokens > 0 {
		config.ContextChars = min(uint64(maxContextCharacters), min(limits.MaxInputTokens, uint64(maxContextCharacters))*4)
	}
	if _, err := validateConfig(config); err != nil {
		return SelectedModel{}, nil, err
	}
	return SelectedModel{ProfileID: profile.ID, ProfileName: profile.Name, Config: config}, selection, nil
}

// validateChatSelection uses generation's resolver when persisting a session default, allowing explicit inheritance.
func validateChatSelection(models ModelSettings, selection *ChatModelSelection) error {
	if selection == nil {
		return nil
	}
	_, _, err := models.selectChatModel(&Project{}, AIRequest{ModelSelection: selection}, "ask")
	return err
}

// estimateTextTokens conservatively counts mixed Unicode prose while identifying the result as an estimate.
func estimateTextTokens(text string) uint64 {
	var ascii, tokens uint64
	for _, character := range text {
		if character <= unicode.MaxASCII {
			ascii++
			continue
		}
		if ascii > 0 {
			tokens += (ascii + 3) / 4
			ascii = 0
		}
		if unicode.IsSpace(character) {
			continue
		}
		tokens += 2
	}
	return tokens + (ascii+3)/4
}

// estimateMessageTokens includes private replay packets and function parameters as well as visible prose.
func estimateMessageTokens(messages []Message) uint64 {
	var tokens uint64
	for _, message := range messages {
		if len(message.providerBlocks) > 0 {
			tokens += estimateTextTokens(jsonText(message.providerBlocks)) + 6
			continue
		}
		tokens += estimateTextTokens(message.Content+message.reasoningContent+message.encryptedContent) + 6
		for _, call := range message.ToolCalls {
			tokens += estimateTextTokens(call.Name + call.Arguments)
		}
	}
	return tokens
}

// requestInputTokens counts the same complete tool declarations that are serialized to the provider.
func requestInputTokens(config ProviderConfig, messages []Message) uint64 {
	if calibrated, ok := calibratedInputTokens(config, messages); ok {
		return calibrated
	}
	tokens := estimateMessageTokens(messages)
	if config.nativeTools {
		tokens += estimateTextTokens(jsonText(nativeToolDefinitions(config)))
	}
	return tokens
}

// tokenInputLimit reserves model output space without assigning advertised capacity to unknown deployments.
func tokenInputLimit(config ProviderConfig) uint64 {
	limits := effectiveModelLimits(config)
	input := limits.MaxInputTokens
	if limits.ContextWindowTokens > 0 {
		reserve := uint64(config.MaxTokens)
		if config.chatOutputAuto {
			reserve = min(reserve, min(uint64(21000), limits.ContextWindowTokens/4))
		}
		margin := min(uint64(1000), limits.ContextWindowTokens/20)
		windowInput := uint64(0)
		if limits.ContextWindowTokens > reserve+margin {
			windowInput = limits.ContextWindowTokens - reserve - margin
		}
		if input == 0 || windowInput < input {
			input = windowInput
		}
	}
	return input
}

// applyRequestTokenBudget validates assembled input and clamps only automatic output to remaining capacity.
func applyRequestTokenBudget(config ProviderConfig, messages []Message) (ProviderConfig, error) {
	limits := effectiveModelLimits(config)
	input := requestInputTokens(config, messages)
	if limits.MaxInputTokens > 0 && input > limits.MaxInputTokens {
		return config, fmt.Errorf("Estimated input uses %d tokens, exceeding the model's %d-token input limit", input, limits.MaxInputTokens)
	}
	if limits.ContextWindowTokens == 0 {
		return config, nil
	}
	margin := min(uint64(1000), limits.ContextWindowTokens/20)
	if input+margin >= limits.ContextWindowTokens {
		return config, errors.New("The assembled input exceeds the model context window. Compact older conversation or remove context")
	}
	available := limits.ContextWindowTokens - input - margin
	if config.chatOutputAuto {
		config.MaxTokens = min(config.MaxTokens, uint32(min(available, math.MaxUint32)))
	} else if uint64(config.MaxTokens) > available {
		return config, fmt.Errorf("The input leaves %d output tokens. Reduce the custom output limit or compact the conversation", available)
	}
	return config, nil
}

// prepareTokenBudget shares model capacity and compaction feasibility with both preview and submission.
func prepareTokenBudget(prompt *Prompt, config *ProviderConfig, selection *ChatModelSelection) {
	limits := effectiveModelLimits(*config)
	input := requestInputTokens(*config, prompt.Messages)
	source := "unknown"
	if _, known := lookupModel(config.ProviderID, config.Model); known {
		source = "catalog"
	}
	if config.ModelLimits != nil {
		source = "deployment"
	}
	budget := &TokenBudget{ContextWindowTokens: limits.ContextWindowTokens, MaxInputTokens: limits.MaxInputTokens, InputTokensEstimate: input, OutputTokens: config.MaxTokens, MaxOutputTokens: limits.MaxOutputTokens, CanSend: true, Estimated: true, Source: source}
	_, budget.UsageCalibrated = calibratedInputTokens(*config, prompt.Messages)
	prompt.ModelSelection = copyChatModelSelection(selection)
	inputLimit := tokenInputLimit(*config)
	if limits.ContextWindowTokens == 0 && limits.MaxInputTokens == 0 {
		budget.Warning = "Model capacity is unknown. The saved local context and output budgets apply."
		budget.NeedsCompaction = len(prompt.HistoryGroups) > 0 && uint64(messageChars(prompt.Messages)+prompt.ToolChars) > config.ContextChars*4/5
	}
	if inputLimit > 0 {
		chars := messageChars(prompt.Messages) + prompt.ToolChars
		if input > 0 {
			derived := min(uint64(maxContextCharacters), max(uint64(1), uint64(chars)*inputLimit/input))
			if selection == nil {
				derived = min(derived, config.ContextChars)
			}
			config.ContextChars = derived
		}
		budget.NeedsCompaction = input > inputLimit*4/5 && len(prompt.HistoryGroups) > 0
		if input > inputLimit {
			fixed := []Message{}
			if len(prompt.Messages) > 0 {
				fixed = append(fixed, prompt.Messages[0])
			}
			if len(prompt.Messages) > 1 {
				start := len(prompt.Messages) - 1
				if len(prompt.HistoryGroups) > 0 {
					start = prompt.HistoryGroups[len(prompt.HistoryGroups)-1].End
					if start < 1 || start >= len(prompt.Messages) {
						budget.CanSend = false
						budget.Warning = "Conversation context boundaries changed. Reload the conversation before sending."
						prompt.Budget = budget
						return
					}
				}
				fixed = append(fixed, prompt.Messages[start:]...)
			}
			budget.CanSend = len(prompt.HistoryGroups) > 0 && requestInputTokens(*config, fixed)+800 < inputLimit
		}
	}
	if adjusted, err := applyRequestTokenBudget(*config, prompt.Messages); err == nil {
		budget.OutputTokens = adjusted.MaxTokens
	} else if !budget.NeedsCompaction {
		budget.CanSend = false
		budget.Warning = err.Error()
	}
	if !budget.CanSend && budget.Warning == "" {
		budget.Warning = "The current request and required context do not fit the selected model. Remove context or select a larger model."
	}
	prompt.Budget = budget
}

// contextHasOmissions identifies selections whose current removal must also replace a cached story baseline.
func contextHasOmissions(context *WorkspaceContext) bool {
	return context != nil && (context.OmitChapter || context.OmitSource || context.OmitSelectedText || len(context.OmitEntityIDs)+len(context.OmitMemoryIDs)+len(context.OmitPlanIDs) > 0)
}

// carriedContextRecords derives UI chips from record markers present in the actual assembled prompt.
func carriedContextRecords(project *Project, request AIRequest, prompt Prompt) []CarriedRecord {
	text := ""
	if len(prompt.Messages) > 0 {
		text = prompt.Messages[0].Content + "\n" + prompt.Messages[len(prompt.Messages)-1].Content
	}
	records := []CarriedRecord{}
	appendRecord := func(id, label, marker string) {
		if strings.Contains(text, marker) {
			records = append(records, CarriedRecord{ID: id, Label: label})
		}
	}
	if context := request.WorkspaceContext; context != nil && strings.TrimSpace(context.SelectedText) != "" && !context.OmitSelectedText {
		appendRecord("selection", truncateSummary(context.SelectedText, 80), "Selected text excerpt:")
	}
	for _, chapter := range project.Chapters {
		appendRecord("chapter:"+chapter.ID, fmt.Sprintf("%d %s", chapter.Number, chapter.Title), "[C:"+chapter.ID+"]")
	}
	for _, source := range project.Sources {
		appendRecord("source:"+source.ID, source.Title, "[S:"+source.ID+"]")
	}
	for _, entity := range project.Entities {
		appendRecord("entity:"+entity.ID, entity.Name, "["+entity.ID+"]")
	}
	if context := request.WorkspaceContext; context != nil && !omittedIDs(context.OmitEntityIDs)[context.ActiveEntityID] {
		for _, place := range project.Studio.Locations {
			if place.ID == context.ActiveEntityID {
				appendRecord("entity:"+place.ID, place.Name, "Focused location: "+jsonText(place))
				break
			}
		}
	}
	for _, memory := range project.Studio.Memories {
		appendRecord("memory:"+memory.ID, memory.Title, "[N:"+memory.ID+"]")
	}
	for _, plan := range project.Studio.Plans {
		appendRecord("plan:"+plan.ID, plan.Title, "[P:"+plan.ID+"]")
	}
	return records
}
