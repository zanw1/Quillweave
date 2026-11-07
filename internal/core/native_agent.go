package core

import "errors"

const agentModelCallLimit = 8
const agentToolCallLimit = 16

// nativeCapabilityKey scopes a compatibility fallback to the exact model, profile and protocol options.
func nativeCapabilityKey(selected SelectedModel) string {
	return providerIdentity(selected.Config, selected.ProfileID) + providerOptionFingerprint(selected.Config)
}

// providerContextMatches preserves legacy identity checks and binds new signed packets to reasoning options.
func providerContextMatches(private providerContext, config ProviderConfig, profileID string) bool {
	return private.Fingerprint == providerIdentity(config, profileID) && (private.OptionFingerprint == "" || private.OptionFingerprint == "native/"+providerOptionFingerprint(config))
}

// assistantModelMessage retains the original provider packet separately from the visible assistant text.
func assistantModelMessage(output modelOutput) Message {
	return Message{Role: "assistant", Content: output.Text, ToolCalls: output.ToolCalls, reasoningContent: output.Reasoning, encryptedContent: output.Encrypted, providerFormat: output.ProviderFormat, providerBlocks: output.ProviderBlocks}
}

// nativeResultMessage correlates one validated result with the exact native function call.
func nativeResultMessage(call NativeToolCall, content string) Message {
	return Message{Role: "tool", Content: content, ToolCallID: call.ID, ToolName: call.Name}
}

// requestAgentModel retries an explicitly unsupported native function interface once within the shared call budget.
func (engine *Engine) requestAgentModel(job *Job, selected *SelectedModel, messages []Message, calls *int) (modelOutput, []Message, error) {
	*calls = *calls + 1
	output, err := engine.streamJobModel(job.ID, "agent", job.ctx, selected.Config, messages, func(string) {})
	if !errors.Is(err, errNativeToolsUnsupported) || !selected.Config.nativeTools || *calls >= agentModelCallLimit {
		return output, messages, err
	}
	engine.mu.Lock()
	if engine.nativeToolFallback == nil {
		engine.nativeToolFallback = map[string]bool{}
	}
	engine.nativeToolFallback[nativeCapabilityKey(*selected)] = true
	engine.mu.Unlock()
	selected.Config.nativeTools = false
	messages = compatibleToolHistory(messages)
	job.Prompt.Messages = compatibleToolHistory(job.Prompt.Messages)
	job.Prompt.ToolChars, job.Prompt.Chars = 0, messageChars(job.Prompt.Messages)
	engine.jobsMu.Lock()
	if current := engine.jobs[job.ID]; current != nil {
		current.Prompt = job.Prompt
	}
	engine.jobsMu.Unlock()
	engine.addStep(job.ID, "native_tools", "done", "The model rejected native functions. Continuing with the compatible tool protocol.")
	*calls = *calls + 1
	output, err = engine.streamJobModel(job.ID, "agent", job.ctx, selected.Config, messages, func(string) {})
	return output, messages, err
}
