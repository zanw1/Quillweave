package core

import (
	"errors"
	"fmt"
)

// wikiMessagesBudget validates a complete stage prompt, including instructions and the reserved model output.
func wikiMessagesBudget(config ProviderConfig, messages []Message) error {
	limits := effectiveModelLimits(config)
	if limits.ContextWindowTokens == 0 && limits.MaxInputTokens == 0 {
		if uint64(messageChars(messages)) > config.ContextChars {
			return errors.New("Wiki instructions and source exceed the saved local context budget")
		}
	} else if limit := tokenInputLimit(config); requestInputTokens(config, messages) > limit {
		return fmt.Errorf("Wiki input exceeds the %d-token input allowance after reserving output", limit)
	}
	_, err := applyRequestTokenBudget(config, messages)
	return err
}

// prepareWikiChunks shares token-aware extraction windows between preview and execution without dropping source text.
func prepareWikiChunks(project *Project, source string, config ProviderConfig) ([]string, error) {
	if err := wikiMessagesBudget(config, extractionPrompt("", project)); err != nil {
		return nil, fmt.Errorf("Wiki extraction instructions and tracking focus do not fit this model: %w", err)
	}
	characters := []rune(source)
	parts := []string{}
	for start := 0; start < len(characters); {
		low, high, size := 1, min(6000, len(characters)-start), 0
		for low <= high {
			candidate := low + (high-low)/2
			prompt := extractionPrompt(string(characters[start:start+candidate]), project)
			if wikiMessagesBudget(config, prompt) == nil {
				size = candidate
				low = candidate + 1
			} else {
				high = candidate - 1
			}
		}
		if size == 0 {
			return nil, errors.New("Wiki extraction cannot fit source text after its instructions and output allowance")
		}
		end := start + size
		parts = append(parts, string(characters[start:end]))
		if end == len(characters) {
			break
		}
		start = end - min(150, size/4)
	}
	return parts, nil
}
