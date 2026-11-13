package core

import "errors"

// The coordinator loop has no fixed model-call or tool-step cap: it ends when the
// model stops calling tools, when the repeated-failure circuit breaker closes tool
// use, when the context budget is exhausted, or when the author cancels. The
// writing pipeline keeps its own independent budget below.
const writingStageCallBudget = 12

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
	stream := engine.newAgentTextStream(job.ID)
	output, err := engine.streamJobModel(job.ID, "agent", job.ctx, selected.Config, messages, stream.append, selected.ProfileID)
	if err == nil {
		stream.finish(output.Text)
	}
	if !errors.Is(err, errNativeToolsUnsupported) || !selected.Config.nativeTools {
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
	stream = engine.newAgentTextStream(job.ID)
	output, err = engine.streamJobModel(job.ID, "agent", job.ctx, selected.Config, messages, stream.append, selected.ProfileID)
	if err == nil {
		stream.finish(output.Text)
	}
	return output, messages, err
}
