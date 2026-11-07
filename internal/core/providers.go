package core

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
)

const ProviderDocsDate = "2026-11-06"
const AnthropicVersion = "2026-10-21"

type ProviderEndpoint struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type ProviderPreset struct {
	ID                     string             `json:"id"`
	Name                   string             `json:"name"`
	Description            string             `json:"description"`
	DocsURL                string             `json:"docs_url"`
	Models                 []string           `json:"models"`
	Endpoints              []ProviderEndpoint `json:"endpoints"`
	ThinkingModes          []string           `json:"thinking_modes"`
	ReasoningEfforts       []string           `json:"reasoning_efforts"`
	TemperaturePolicy      string             `json:"temperature_policy"`
	TemperatureFixedModels []string           `json:"temperature_fixed_models,omitempty"`
	Config                 ProviderConfig     `json:"config"`
}

// providerPreset creates credential-free defaults under shared budget and timeout constraints.
func providerPreset(id, name, provider, protocol, base, model, docs, description string) ProviderPreset {
	config := defaultProvider()
	config.ProviderID, config.Protocol, config.BaseURL, config.Model = provider, protocol, base, model
	config.Temperature = nil
	config.MaxTokens, config.ContextChars, config.TimeoutSeconds = 8192, 64000, 180
	return ProviderPreset{ID: id, Name: name, Description: description, DocsURL: docs, Models: []string{model}, Endpoints: []ProviderEndpoint{{Name: "Default endpoint", URL: base}}, ThinkingModes: []string{}, ReasoningEfforts: []string{}, TemperaturePolicy: "optional", Config: config}
}

// providerPresets provides documented text presets whose example model IDs remain editable.
func providerPresets() []ProviderPreset {
	items := []ProviderPreset{
		providerPreset("openai", "OpenAI", "openai", "responses", "https://api.openai.com/v1", "gpt-6-astra", "https://developers.openai.com/api/docs/guides/streaming-responses", "Responses API. The output budget includes reasoning tokens. Temperature is omitted by default."),
		providerPreset("anthropic", "Anthropic / Claude", "anthropic", "anthropic", "https://api.anthropic.com/v1", "claude-sonnet-5-5", "https://platform.claude.com/docs/en/api/migration-2026-10-21", "Native Messages API. The current API uses model-default temperature. Legacy API compatibility is available."),
		providerPreset("gemini", "Google Gemini · AI Studio", "google", "gemini", "https://generativelanguage.googleapis.com/v1beta", "gemini-3.8-flash", "https://aistudio.google.com/api-reference/interactions-api/create-interaction", "Native Interactions API with API key authentication and step-based text streaming."),
		providerPreset("deepseek", "DeepSeek", "deepseek", "openai", "https://api.deepseek.com", "deepseek-flash", "https://api-docs.deepseek.com/api/create-chat-completion/", "Separate reasoning and answer text. Thinking can be disabled. Temperature is available with thinking off."),
		providerPreset("bailian", "Alibaba Cloud Bailian / Qwen", "bailian", "openai", "https://dashscope.aliyuncs.com/compatible-mode/v1", "qwen3.8-max", "https://help.aliyun.com/zh/model-studio/qwen-api-via-openai-chat-completions", "Standard API with regional and workspace endpoints. Use a key from the selected region."),
		providerPreset("ark", "Volcengine Ark / Doubao", "ark", "openai", "https://ark.cn-beijing.volces.com/api/v3", "doubao-seed-2-1-pro-260915", "https://docs.volcengine.com/docs/ark/chat-api?lang=zh", "Use a model ID or enabled endpoint ID. The output budget includes reasoning."),
		providerPreset("zhipu", "Zhipu / GLM", "zhipu", "openai", "https://open.bigmodel.cn/api/paas/v4", "glm-5.3", "https://docs.bigmodel.cn/api-reference/%E6%A8%A1%E5%9E%8B-api/%E5%AF%B9%E8%AF%9D%E8%A1%A5%E5%85%A8", "Standard pay-as-you-go API. Reasoning depth depends on the model. Temperature range is 0–1."),
		providerPreset("moonshot", "Moonshot / Kimi", "moonshot", "openai", "https://api.moonshot.cn/v1", "kimi-k3.1-pro", "https://platform.kimi.com/docs/api/chat", "K3 always uses thinking. Compatible reasoning history is stored and restored by the backend."),
		providerPreset("minimax", "MiniMax", "minimax", "openai", "https://api.minimax.cn/v1", "MiniMax-M3.1-Flash", "https://platform.minimax.cn/docs/api-reference/text-openai-api", "Reasoning and answer text are received separately. Flash uses thinking and model-default temperature."),
		providerPreset("xai", "xAI / Grok", "xai", "responses", "https://api.x.ai/v1", "grok-4.7", "https://docs.x.ai/developers/model-capabilities/text/generate-text", "Official Responses API with store=false."),
		providerPreset("ollama", "Ollama · Local", "ollama", "ollama", "http://localhost:11434", "gemma4", "https://docs.ollama.com/api/chat", "Start Ollama and install the selected model. Local access can use an empty API key."),
		providerPreset("openrouter", "OpenRouter", "openrouter", "openai", "https://openrouter.ai/api/v1", "openai/gpt-6.1-sol", "https://openrouter.ai/docs/quickstart", "Unified Chat Completions API. Keep the full provider/model ID, or fetch models available through OpenRouter."),
	}
	for i := range items {
		item := &items[i]
		switch item.Config.ProviderID {
		case "openai":
			item.ReasoningEfforts = []string{"low", "medium", "high", "xhigh"}
		case "anthropic":
			item.Config.APIVersion = AnthropicVersion
			item.ReasoningEfforts = []string{"low", "medium", "high", "max"}
			item.Models = []string{"claude-sonnet-5-5", "claude-opus-5-5"}
			item.TemperatureFixedModels = append([]string{}, item.Models...)
			item.TemperaturePolicy = "anthropic_version"
		case "google":
			item.ReasoningEfforts = []string{"minimal", "low", "medium", "high"}
		case "deepseek":
			item.Models = []string{"deepseek-flash", "deepseek-v4-pro"}
			item.ThinkingModes = []string{"enabled", "disabled"}
			item.ReasoningEfforts = []string{"low", "high", "max"}
			item.TemperaturePolicy = "thinking_off"
		case "bailian":
			item.ThinkingModes = []string{"enabled", "disabled"}
			item.ReasoningEfforts = []string{"low", "medium", "xhigh"}
			item.Endpoints = []ProviderEndpoint{
				{Name: "Beijing · Shared endpoint", URL: item.Config.BaseURL},
				{Name: "Singapore · Shared endpoint", URL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"},
				{Name: "Beijing · Workspace endpoint", URL: "https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"},
				{Name: "Singapore · Workspace endpoint", URL: "https://{WorkspaceId}.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"},
				{Name: "Virginia · Workspace endpoint", URL: "https://{WorkspaceId}.us-east-1.maas.aliyuncs.com/compatible-mode/v1"},
				{Name: "Frankfurt · Workspace endpoint", URL: "https://{WorkspaceId}.eu-central-1.maas.aliyuncs.com/compatible-mode/v1"},
				{Name: "Tokyo · Workspace endpoint", URL: "https://{WorkspaceId}.ap-northeast-1.maas.aliyuncs.com/compatible-mode/v1"},
				{Name: "Hong Kong · Workspace endpoint", URL: "https://{WorkspaceId}.cn-hongkong.maas.aliyuncs.com/compatible-mode/v1"},
			}
		case "ark":
			item.Config.TimeoutSeconds = 1800
			item.ThinkingModes = []string{"enabled", "disabled"}
			item.ReasoningEfforts = []string{"low", "medium", "high", "xhigh", "max"}
		case "zhipu":
			item.ThinkingModes = []string{"enabled", "disabled"}
			item.ReasoningEfforts = []string{"low", "high", "max"}
			item.TemperaturePolicy = "range01"
		case "moonshot":
			item.ReasoningEfforts = []string{"low", "high", "max"}
			item.TemperaturePolicy = "none"
		case "minimax":
			item.ReasoningEfforts = []string{"low", "medium", "high", "xhigh", "max"}
			item.TemperaturePolicy = "none"
			item.Endpoints = []ProviderEndpoint{{Name: "China", URL: "https://api.minimax.cn/v1"}, {Name: "International", URL: "https://api.minimax.io/v1"}}
		case "ollama":
			item.ThinkingModes = []string{"enabled", "disabled"}
			item.Config.MaxTokens, item.Config.ContextChars = 4096, 24000
		case "xai":
			item.Config.TimeoutSeconds = 3600
		case "openrouter":
			item.Models = []string{"openai/gpt-6.1-sol", "openai/gpt-5.4", "anthropic/claude-sonnet-4.6", "openrouter/auto"}
			item.ReasoningEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
		}
	}
	chat := items[0]
	chat.ID, chat.Name, chat.Config.Protocol = "openai-chat", "OpenAI · Chat compatibility", "openai"
	chat.Description = "Chat Completions integration using max_completion_tokens."
	items = append(items, chat)
	generate := items[2]
	generate.ID, generate.Name, generate.Config.Protocol = "gemini-generate", "Gemini · Generate Content compatibility", "gemini_generate"
	generate.DocsURL = "https://ai.google.dev/gemini-api/docs/generate-content/text-generation"
	generate.Description = "Native Generate Content streaming for compatible gateways."
	items = append(items, generate)
	kimi := items[7]
	kimi.ID, kimi.Name, kimi.Config.Model = "moonshot-k2", "Kimi · K2.6", "kimi-k2.6"
	kimi.Models, kimi.ThinkingModes, kimi.ReasoningEfforts = []string{"kimi-k2.6"}, []string{"enabled", "disabled"}, []string{}
	kimi.Description = "K2.6 supports thinking on or off and uses its own parameter set."
	items = append(items, kimi)
	mini := items[8]
	mini.ID, mini.Name, mini.Config.Model = "minimax-m3", "MiniMax · M3", "MiniMax-M3"
	mini.Models, mini.ThinkingModes, mini.ReasoningEfforts = []string{"MiniMax-M3"}, []string{"enabled", "disabled"}, []string{}
	mini.Description = "M3 supports thinking on or off. Reasoning remains separate from the answer."
	items = append(items, mini)
	for i := range items {
		if items[i].Config.ProviderID != "openrouter" && contains(items[i].ReasoningEfforts, "low") {
			items[i].Config.ReasoningEffort = "low"
		} else {
			items[i].Config.ReasoningEffort = ""
		}
	}
	return items
}

// providerRules selects known model constraints or explicit custom-protocol options.
func providerRules(config ProviderConfig) (ProviderPreset, bool) {
	rules, ok := baseProviderRules(config)
	if metadata, found := lookupModel(config.ProviderID, config.Model); found {
		if metadata.TemperatureSupported != nil && !*metadata.TemperatureSupported {
			rules.TemperaturePolicy = "none"
		}
		if len(metadata.ReasoningEfforts) > 0 && (config.ProviderID == "" || config.ProviderID == "openai") {
			// Catalog metadata cannot introduce a dialect the adapter cannot send.
			allowed := customReasoningEfforts(config.Protocol)
			for _, effort := range metadata.ReasoningEfforts {
				if contains(allowed, effort) && !contains(rules.ReasoningEfforts, effort) {
					rules.ReasoningEfforts = append(rules.ReasoningEfforts, effort)
				}
			}
		}
	}
	return rules, ok
}

// customReasoningEfforts limits metadata to reasoning fields implemented by each protocol.
func customReasoningEfforts(protocol string) []string {
	switch protocol {
	case "openai", "responses":
		return []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
	case "anthropic":
		return []string{"low", "medium", "high", "max"}
	case "gemini", "gemini_generate":
		return []string{"minimal", "low", "medium", "high"}
	default:
		return nil
	}
}

// baseProviderRules retains provider-specific wire behavior independently of model metadata.
func baseProviderRules(config ProviderConfig) (ProviderPreset, bool) {
	if config.ProviderID == "" {
		rules := ProviderPreset{ThinkingModes: []string{}, ReasoningEfforts: []string{}, TemperaturePolicy: "optional"}
		switch config.Protocol {
		case "responses":
			rules.ReasoningEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
		case "anthropic":
			rules.ReasoningEfforts = []string{"low", "medium", "high", "max"}
			rules.TemperaturePolicy = "anthropic_version"
			rules.TemperatureFixedModels = []string{"claude-sonnet-5-5", "claude-opus-5-5"}
		case "gemini", "gemini_generate":
			rules.ReasoningEfforts = []string{"minimal", "low", "medium", "high"}
		case "ollama":
			rules.ThinkingModes = []string{"enabled", "disabled"}
		}
		return rules, true
	}
	var fallback ProviderPreset
	found := false
	for _, item := range providerPresets() {
		if item.Config.ProviderID == config.ProviderID && item.Config.Protocol == config.Protocol {
			if contains(item.Models, config.Model) {
				return item, true
			}
			if !found {
				fallback, found = item, true
			}
		}
	}
	return fallback, found
}

// optionalMode normalizes empty and model-default options for stable cache identity.
func optionalMode(value string) string {
	if value == "auto" {
		return ""
	}
	return value
}

// anthropicVersion selects the current version by default and permits explicit legacy compatibility.
func anthropicVersion(config ProviderConfig) string {
	if config.APIVersion != "" {
		return config.APIVersion
	}
	return AnthropicVersion
}

// sendsTemperature checks whether an API and model accept an explicit temperature.
func sendsTemperature(config ProviderConfig, rules ProviderPreset) bool {
	if contains(rules.TemperatureFixedModels, config.Model) {
		return false
	}
	switch rules.TemperaturePolicy {
	case "none":
		return false
	case "thinking_off":
		return optionalMode(config.ThinkingMode) == "disabled"
	case "anthropic_version":
		return anthropicVersion(config) == "2023-06-01"
	default:
		return true
	}
}

// validateProviderOptions validates provider-specific options before a request is sent.
func validateProviderOptions(config ProviderConfig) error {
	rules, ok := providerRules(config)
	if !ok {
		return errors.New("The provider and API protocol do not match. Apply a preset or choose a custom profile")
	}
	thinking, effort := optionalMode(config.ThinkingMode), optionalMode(config.ReasoningEffort)
	if thinking != "" && !contains(rules.ThinkingModes, thinking) {
		return errors.New("This provider or model does not support that thinking mode. Select the model default")
	}
	if effort != "" && !contains(rules.ReasoningEfforts, effort) {
		return errors.New("This provider or model does not support that reasoning effort")
	}
	if thinking == "disabled" && effort != "" {
		return errors.New("Set reasoning effort to the model default when thinking is disabled")
	}
	if config.Protocol == "anthropic" {
		if !contains([]string{AnthropicVersion, "2023-06-01"}, anthropicVersion(config)) {
			return errors.New("Unsupported Anthropic API version")
		}
	} else if config.APIVersion != "" {
		return errors.New("API version is available for Anthropic Messages")
	}
	if config.Temperature != nil {
		if math.IsNaN(*config.Temperature) || math.IsInf(*config.Temperature, 0) || *config.Temperature < 0 || *config.Temperature > 2 {
			return errors.New("Temperature must be between 0 and 2, or left empty")
		}
		if !sendsTemperature(config, rules) {
			return errors.New("This API or thinking mode requires model-default temperature. Leave the field empty")
		}
		if (rules.TemperaturePolicy == "range01" || config.Protocol == "anthropic") && *config.Temperature > 1 {
			return errors.New("Temperature range for this API is 0–1")
		}
	}
	return nil
}

// providerEndpoint retains gateway paths and completes the endpoint using the configured protocol and model.
func providerEndpoint(config ProviderConfig) (string, error) {
	base, err := validatedAPIBase(config.BaseURL)
	if err != nil {
		return "", err
	}
	switch config.Protocol {
	case "openai":
		if !strings.HasSuffix(base, "/chat/completions") {
			base += "/chat/completions"
		}
	case "responses":
		if !strings.HasSuffix(base, "/responses") {
			base += "/responses"
		}
	case "anthropic":
		if !strings.HasSuffix(base, "/messages") {
			if !strings.HasSuffix(base, "/v1") {
				base += "/v1"
			}
			base += "/messages"
		}
	case "gemini":
		if !strings.HasSuffix(base, "/interactions") {
			base += "/interactions"
		}
	case "gemini_generate":
		if strings.HasSuffix(base, ":streamGenerateContent") || strings.HasSuffix(base, ":generateContent") {
			at := strings.LastIndex(base, "/models/")
			if at < 0 {
				return "", errors.New("The Gemini endpoint is missing its models path")
			}
			base = base[:at]
		}
		model := strings.TrimPrefix(config.Model, "models/")
		base += "/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
	case "ollama":
		if !strings.HasSuffix(base, "/api/chat") {
			if strings.HasSuffix(base, "/api") {
				base += "/chat"
			} else {
				base += "/api/chat"
			}
		}
	default:
		return "", errors.New("Select a supported API protocol")
	}
	return base, nil
}

// validatedAPIBase shares URL validation between model discovery and generation.
func validatedAPIBase(address string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(address), "/")
	if strings.ContainsAny(base, "{}<>") {
		return "", errors.New("Enter a workspace ID or complete API URL to replace the placeholder")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "", errors.New("Invalid API URL")
	}
	local := contains([]string{"localhost", "127.0.0.1", "::1"}, strings.ToLower(parsed.Hostname()))
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && local) {
		return "", errors.New("Use HTTPS for cloud APIs. Local services can use HTTP")
	}
	if parsed.User != nil && (parsed.User.Username() != "" || strings.Contains(parsed.User.String(), ":")) || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(base, "#") {
		return "", errors.New("Use an API URL without credentials, query parameters, or a fragment")
	}
	return base, nil
}

// modelWireMessages builds provider fields while keeping internal task IDs local.
func modelWireMessages(messages []Message, config ProviderConfig) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		role := message.Role
		if config.ProviderID == "openai" && config.Protocol == "openai" && role == "system" {
			role = "developer"
		}
		item := map[string]any{"role": role, "content": message.Content}
		if message.CacheControl != nil && (config.Protocol == "anthropic" || config.ProviderID == "openrouter") {
			item["content"] = []map[string]any{{"type": "text", "text": message.Content, "cache_control": message.CacheControl}}
		}
		if config.Protocol == "openai" && preservesProviderContext(config) {
			if message.reasoningContent != "" {
				item["reasoning_content"] = message.reasoningContent
			}
			if message.encryptedContent != "" {
				item["encrypted_content"] = message.encryptedContent
			}
		}
		if config.Protocol == "openai" || config.Protocol == "ollama" {
			item = nativeChatMessage(message, config, item)
		}
		result = append(result, item)
	}
	return result
}

// splitSystemMessages converts unified system messages to the native API system field.
func splitSystemMessages(messages []Message) (string, []Message) {
	system := []string{}
	turns := []Message{}
	for _, message := range messages {
		if message.Role == "system" || message.Role == "developer" {
			system = append(system, message.Content)
		} else {
			turns = append(turns, message)
		}
	}
	return strings.Join(system, "\n\n"), turns
}

// modelRequest constructs native request bodies and authentication headers for the selected provider.
func modelRequest(config ProviderConfig, messages []Message) (map[string]any, http.Header, error) {
	if _, err := validateConfig(config); err != nil {
		return nil, nil, err
	}
	body := map[string]any{"model": config.Model, "stream": true}
	headers := http.Header{"Content-Type": []string{"application/json"}, "Accept": []string{"text/event-stream"}}
	rules, _ := providerRules(config)
	thinking, effort := optionalMode(config.ThinkingMode), optionalMode(config.ReasoningEffort)
	if config.APIKey != "" {
		headers.Set("Authorization", "Bearer "+config.APIKey)
	}
	switch config.Protocol {
	case "openai":
		body["messages"] = modelWireMessages(messages, config)
		body["stream_options"] = map[string]bool{"include_usage": true}
		if config.ProviderID == "openai" || config.ProviderID == "ark" || config.ProviderID == "bailian" || config.ProviderID == "moonshot" && strings.HasPrefix(config.Model, "kimi-k3") {
			body["max_completion_tokens"] = config.MaxTokens
		} else {
			body["max_tokens"] = config.MaxTokens
		}
		if config.Temperature != nil {
			body["temperature"] = *config.Temperature
		}
		if effort != "" {
			if config.ProviderID == "openrouter" {
				body["reasoning"] = map[string]string{"effort": effort}
			} else {
				body["reasoning_effort"] = effort
			}
		}
		if thinking != "" {
			switch config.ProviderID {
			case "bailian":
				body["enable_thinking"] = thinking == "enabled"
			case "minimax":
				if thinking == "enabled" {
					thinking = "adaptive"
				}
				body["thinking"] = map[string]string{"type": thinking}
			default:
				body["thinking"] = map[string]string{"type": thinking}
			}
		}
		if config.ProviderID == "minimax" {
			body["reasoning_split"] = true
		}
	case "responses":
		body["input"] = nativeResponsesInput(messages, config)
		body["max_output_tokens"], body["store"] = config.MaxTokens, false
		if config.Temperature != nil {
			body["temperature"] = *config.Temperature
		}
		if effort != "" {
			body["reasoning"] = map[string]string{"effort": effort}
		}
	case "anthropic":
		_, turns := splitSystemMessages(messages)
		body["messages"], body["max_tokens"] = nativeAnthropicMessages(turns, config), config.MaxTokens
		system := []map[string]any{}
		for _, message := range messages {
			if message.Role != "system" && message.Role != "developer" {
				continue
			}
			block := map[string]any{"type": "text", "text": message.Content}
			if message.CacheControl != nil {
				block["cache_control"] = message.CacheControl
			}
			system = append(system, block)
		}
		if len(system) > 0 {
			body["system"] = system
		}
		headers.Set("anthropic-version", anthropicVersion(config))
		if anthropicVersion(config) == "2023-06-01" {
			headers.Del("Authorization")
			if config.APIKey != "" {
				headers.Set("x-api-key", config.APIKey)
			}
		}
		if config.Temperature != nil && sendsTemperature(config, rules) {
			body["temperature"] = *config.Temperature
		}
		if effort != "" {
			body["output_config"] = map[string]string{"effort": effort}
			body["thinking"] = map[string]string{"type": "adaptive"}
		}
	case "gemini":
		headers.Del("Authorization")
		if config.APIKey != "" {
			headers.Set("x-goog-api-key", config.APIKey)
		}
		system, turns := splitSystemMessages(messages)
		body["input"], body["store"] = nativeGeminiInput(turns), false
		if system != "" {
			body["system_instruction"] = system
		}
		generation := map[string]any{"max_output_tokens": config.MaxTokens, "thinking_summaries": "none"}
		if effort != "" {
			generation["thinking_level"] = effort
		}
		if config.Temperature != nil {
			generation["temperature"] = *config.Temperature
		}
		body["generation_config"] = generation
	case "gemini_generate":
		headers.Del("Authorization")
		if config.APIKey != "" {
			headers.Set("x-goog-api-key", config.APIKey)
		}
		delete(body, "model")
		delete(body, "stream")
		system, turns := splitSystemMessages(messages)
		body["contents"] = nativeGeminiContents(turns)
		if system != "" {
			body["systemInstruction"] = map[string]any{"parts": []map[string]string{{"text": system}}}
		}
		generation := map[string]any{"maxOutputTokens": config.MaxTokens}
		if effort != "" {
			generation["thinkingConfig"] = map[string]any{"thinkingLevel": effort, "includeThoughts": false}
		}
		if config.Temperature != nil {
			generation["temperature"] = *config.Temperature
		}
		body["generationConfig"] = generation
	case "ollama":
		headers.Set("Accept", "application/x-ndjson")
		body["messages"] = modelWireMessages(messages, config)
		options := map[string]any{"num_predict": config.MaxTokens}
		if config.Temperature != nil {
			options["temperature"] = *config.Temperature
		}
		body["options"] = options
		if thinking != "" {
			body["think"] = thinking == "enabled"
		}
	default:
		return nil, nil, fmt.Errorf("Unsupported API protocol: %s", config.Protocol)
	}
	applyNativeTools(body, config)
	return body, headers, nil
}

// preservesProviderContext restores reasoning only for supported compatible protocols.
func preservesProviderContext(config ProviderConfig) bool {
	if config.nativeTools {
		return true
	}
	if config.Protocol != "openai" || optionalMode(config.ThinkingMode) == "disabled" {
		return false
	}
	switch config.ProviderID {
	case "moonshot":
		return strings.HasPrefix(config.Model, "kimi-k3") || strings.HasPrefix(config.Model, "kimi-k2.7")
	case "bailian":
		return strings.HasPrefix(config.Model, "qwen3.8")
	case "ark":
		return true
	default:
		return false
	}
}

// providerIdentity binds private context to profile, provider, protocol, endpoint, model, and API version.
func providerIdentity(config ProviderConfig, profileID string) string {
	return hashText(strings.Join([]string{profileID, config.ProviderID, config.Protocol, strings.TrimRight(config.BaseURL, "/"), config.Model, config.APIVersion}, "|"))
}

// providerOptionFingerprint includes new API options in Wiki cache identity while retaining legacy fingerprints.
func providerOptionFingerprint(config ProviderConfig) string {
	version := config.APIVersion
	if config.Protocol == "anthropic" {
		version = anthropicVersion(config)
	}
	values := []string{config.ProviderID, optionalMode(config.ThinkingMode), optionalMode(config.ReasoningEffort), version}
	if strings.Join(values, "") == "" {
		return ""
	}
	return hashText(jsonText(values))
}
