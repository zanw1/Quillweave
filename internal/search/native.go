package search

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// executeNative requests provider-side browsing and accepts only structured execution/citation evidence.
func executeNative(ctx context.Context, request ExecuteRequest) (Response, error) {
	response := EmptyResponse()
	profile := request.Profile
	if request.Model.NameID == "" || !request.Model.Enabled || request.Model.Family != profile.Family {
		return response, Fail("SEARCH_PROFILE_UNAVAILABLE", "Select an enabled research model from the same service", 422)
	}
	if profile.Limits.StrictBudget && !profile.HardLimitKnown() {
		return response, Fail("SEARCH_CAPABILITY_UNSUPPORTED", "This native route cannot guarantee the configured hard search limit; use standalone search or a pinned engine", 422)
	}
	prompt := request.Query
	if request.Operation == "fetch" {
		prompt = "Read this exact public page and cite it. Do not substitute another page: " + request.URL
	}
	instructions := "Research only the provided question. Use the provided web tool when needed. Clearly distinguish retrieved source material from your own explanation; return source citations. Do not claim you searched if no tool ran."
	if request.Language == "zh-CN" || request.Language == "zh" {
		instructions = "请只研究当前问题，必要时使用提供的联网工具，保留来源引用；区分网页材料和自己的解释，未调用工具时不得声称已搜索。请使用简体中文回答。"
	}
	tokens := request.Model.MaxTokens
	if tokens <= 0 || tokens > 4096 {
		tokens = 4096
	}
	headers := map[string]string{"Authorization": "Bearer " + request.Key}
	var body map[string]any
	var address string
	var err error
	if profile.Family == "openrouter" {
		address, err = ModelEndpoint(profile.Endpoint, "chat/completions")
		if err != nil {
			return response, err
		}
		parameters := map[string]any{"engine": profile.Engine, "max_uses": max(1, request.Remaining), "max_results": profile.Limits.MaxResults, "max_total_results": profile.Limits.MaxResults, "max_characters": max(500, profile.Limits.MaxContentChars/profile.Limits.MaxResults)}
		toolType := "openrouter:web_search"
		if profile.SearchMode != "" {
			parameters["mode"] = profile.SearchMode
		}
		if len(profile.Filters.AllowedDomains) > 0 {
			parameters["allowed_domains"] = profile.Filters.AllowedDomains
		}
		if len(profile.Filters.ExcludedDomains) > 0 {
			parameters["excluded_domains"] = profile.Filters.ExcludedDomains
		}
		if request.Operation == "fetch" {
			toolType = "openrouter:web_fetch"
			parameters = map[string]any{"engine": "openrouter", "max_uses": 1, "max_content_tokens": max(500, profile.Limits.MaxContentChars/3), "allowed_domains": []string{hostOf(request.URL)}}
		}
		body = map[string]any{"model": request.Model.NameID, "messages": []any{map[string]any{"role": "system", "content": instructions}, map[string]any{"role": "user", "content": prompt}}, "tools": []any{map[string]any{"type": toolType, "parameters": parameters}}, "max_tokens": tokens, "stream": false, "max_tool_calls": max(1, request.Remaining), "plugins": []any{map[string]any{"id": "web", "enabled": false}}}
	} else {
		if profile.Family == "deepseek" && request.Model.NameID != "deepseek-v4-pro" {
			return response, Fail("SEARCH_CAPABILITY_UNSUPPORTED", "DeepSeek native web search currently requires deepseek-v4-pro", 422)
		}
		if profile.Family == "kimi" && !strings.HasPrefix(request.Model.NameID, "kimi-k3") {
			return response, Fail("SEARCH_CAPABILITY_UNSUPPORTED", "Use a documented Kimi K3 Responses model or standalone Kimi search", 422)
		}
		address, err = ModelEndpoint(profile.Endpoint, "responses")
		if err != nil {
			return response, err
		}
		body = map[string]any{"model": request.Model.NameID, "instructions": instructions, "input": prompt, "tools": []any{map[string]any{"type": "web_search"}}, "max_output_tokens": tokens, "stream": false}
		if profile.Family == "kimi" {
			body["include"] = []string{"web_search_call.action.sources"}
		}
	}
	if strings.Contains(request.Model.NameID, ":online") {
		return response, Fail("SEARCH_CAPABILITY_UNSUPPORTED", "Remove the deprecated :online model variant before using the search server tool", 422)
	}
	value, err := RequestJSON(ctx, "POST", address, body, headers, profile.Limits.TimeoutSeconds)
	if err != nil {
		return response, err
	}
	response = parseNative(profile, value, request.Operation)
	if status := Text(value, "status"); status == "failed" || status == "incomplete" {
		response.TestVerdict = "failed"
		return response, Fail("SEARCH_INCOMPLETE_RESPONSE", "The research model did not finish its response", 502)
	}
	if profile.Family == "openrouter" {
		choices, ok := value["choices"].([]any)
		if !ok || len(choices) == 0 {
			return response, Fail("SEARCH_INVALID_RESPONSE", "The research model returned an unexpected response schema", 502)
		}
		for _, item := range choices {
			choice, _ := item.(map[string]any)
			if Text(choice, "finish_reason") == "length" {
				response.TestVerdict = "failed"
				return response, Fail("SEARCH_INCOMPLETE_RESPONSE", "The research model did not finish its response", 502)
			}
		}
	} else if _, ok := value["output"].([]any); !ok && Text(value, "output_text") == "" {
		return response, Fail("SEARCH_INVALID_RESPONSE", "The research model returned an unexpected response schema", 502)
	}
	if profile.Family == "openrouter" && contains([]string{"exa", "parallel", "perplexity", "firecrawl"}, profile.Engine) && response.Engine == "" {
		response.Engine = profile.Engine
	}
	for index := range response.Results {
		response.Results[index].Engine = response.Engine
	}
	if response.SearchState == "not_triggered" {
		response.Warnings = append(response.Warnings, "The model returned an answer without verified web-tool execution")
	}
	return response, nil
}

// parseNative keeps model prose separate from URL citations and provider tool-result records.
func parseNative(profile Profile, value map[string]any, operation string) Response {
	response := EmptyResponse()
	response.SearchState = "not_triggered"
	citations := map[string]Citation{}
	results := map[string]Result{}
	texts := []string{}
	executed := false
	addSource := func(source map[string]any) {
		address := firstText(source, "url", "uri")
		if !ValidURL(address, false) {
			return
		}
		title := Text(source, "title")
		content := firstText(source, "content", "text", "snippet")
		result := newResult(profile, address, title, content, "provider_passage")
		if content == "" {
			result.Reading = "search_excerpt"
		}
		if previous, exists := results[address]; exists && previous.Content != "" && result.Content == "" {
			result = previous
		}
		results[address] = result
		citations[address] = Citation{URL: address, Title: result.Title}
	}
	var walkAnnotations func(any)
	walkAnnotations = func(raw any) {
		annotations, _ := raw.([]any)
		for _, item := range annotations {
			annotation, ok := item.(map[string]any)
			if !ok {
				continue
			}
			kind := Text(annotation, "type")
			if kind != "url_citation" && kind != "web_search_result_location" {
				continue
			}
			source := Object(annotation, "url_citation")
			if source == nil {
				source = annotation
			}
			addSource(source)
			if len(results) > 0 {
				executed = true
			}
		}
	}
	if choices, ok := value["choices"].([]any); ok {
		for _, item := range choices {
			choice, _ := item.(map[string]any)
			message := Object(choice, "message")
			if text := Text(message, "content"); text != "" {
				texts = append(texts, text)
			}
			walkAnnotations(message["annotations"])
			if parts, ok := message["content"].([]any); ok {
				for _, part := range parts {
					block, _ := part.(map[string]any)
					if text := Text(block, "text"); text != "" {
						texts = append(texts, text)
					}
					walkAnnotations(block["annotations"])
				}
			}
		}
	}
	if output, ok := value["output"].([]any); ok {
		for _, item := range output {
			entry, _ := item.(map[string]any)
			kind := Text(entry, "type")
			if kind == "web_search_call" || kind == "web_fetch_call" {
				if Text(entry, "status") == "completed" {
					executed = true
				}
				for _, container := range []map[string]any{entry, Object(entry, "action"), Object(entry, "result")} {
					for _, field := range []string{"sources", "results"} {
						if sources, ok := container[field].([]any); ok {
							for _, source := range sources {
								object, _ := source.(map[string]any)
								addSource(object)
							}
						}
					}
				}
				if engine := firstText(entry, "engine"); contains([]string{"native", "exa", "parallel", "perplexity", "firecrawl", "openrouter"}, engine) {
					response.Engine = engine
				}
			}
			if kind == "message" {
				if blocks, ok := entry["content"].([]any); ok {
					for _, part := range blocks {
						block, _ := part.(map[string]any)
						if Text(block, "type") == "output_text" || Text(block, "type") == "text" {
							if text := Text(block, "text"); text != "" {
								texts = append(texts, text)
							}
							walkAnnotations(block["annotations"])
						}
					}
				}
			}
		}
	}
	if len(texts) == 0 {
		if text := Text(value, "output_text"); text != "" {
			texts = append(texts, text)
		}
	}
	usage := Object(value, "usage")
	response.Usage.ModelInputTokens = Number(usage["input_tokens"])
	if response.Usage.ModelInputTokens == nil {
		response.Usage.ModelInputTokens = Number(usage["prompt_tokens"])
	}
	response.Usage.ModelOutputTokens = Number(usage["output_tokens"])
	if response.Usage.ModelOutputTokens == nil {
		response.Usage.ModelOutputTokens = Number(usage["completion_tokens"])
	}
	for _, key := range []string{"server_tool_use_details", "server_tool_use"} {
		tools := Object(usage, key)
		if number := Number(tools["web_search_requests"]); number != nil {
			response.Usage.SearchCalls = number
		}
		if number := Number(tools["tool_search"]); number != nil {
			response.Usage.SearchCalls = number
		}
		if number := Number(tools["web_fetch_requests"]); number != nil {
			response.Usage.FetchCalls = number
		}
		if number := Number(tools["tool_fetch"]); number != nil {
			response.Usage.FetchCalls = number
		}
	}
	if cost, ok := usage["cost"]; ok {
		var parsed float64
		if _, err := fmt.Sscan(fmt.Sprint(cost), &parsed); err == nil && parsed >= 0 && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
			response.Usage.ReportedCost = &parsed
			response.Usage.Currency = "USD"
			response.Usage.CostStatus = "reported"
		}
	}
	if engine := Text(value, "search_engine"); contains([]string{"native", "exa", "parallel", "perplexity", "firecrawl", "openrouter"}, engine) {
		response.Engine = engine
	}
	keys := sortedResultKeys(results)
	answer := &Answer{Text: strings.Join(texts, "\n\n"), Citations: []Citation{}}
	for _, address := range keys {
		response.Results = append(response.Results, results[address])
		answer.Citations = append(answer.Citations, citations[address])
	}
	if answer.Text != "" {
		response.Answer = answer
	}
	if executed {
		response.SearchState = "executed"
		response.TestVerdict = "passed"
		if len(response.Results) == 0 {
			response.TestVerdict = "connected_empty"
		}
	} else {
		response.TestVerdict = "inconclusive"
	}
	return response
}
