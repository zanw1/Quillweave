package search

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

type ExecuteRequest struct {
	Profile   Profile
	Key       string
	Model     Model
	Operation string
	Query     string
	URL       string
	Language  string
	Remaining int
}

// Execute uses only the declared adapter and normalizes its structured response with explicit provenance.
func Execute(ctx context.Context, request ExecuteRequest) (Response, error) {
	response := EmptyResponse()
	if err := ValidateExecution(request); err != nil {
		return response, err
	}
	if request.Profile.Native() && !(request.Operation == "fetch" && request.Profile.Family != "openrouter") {
		response, err := executeNative(ctx, request)
		return sanitizeResponse(response, request), err
	}
	response, err := executeStandalone(ctx, request)
	return sanitizeResponse(response, request), err
}

// ValidateExecution applies pure request checks before a durable budget reservation or supplier call.
func ValidateExecution(request ExecuteRequest) error {
	if err := request.Profile.Validate(); err != nil {
		return err
	}
	if request.Operation != "search" && request.Operation != "fetch" {
		return Fail("SEARCH_INVALID_ARGUMENT", "Invalid search operation", 400)
	}
	if request.Operation == "search" && (strings.TrimSpace(request.Query) == "" || utf8.RuneCountInString(request.Query) > 500) {
		return Fail("SEARCH_INVALID_ARGUMENT", "Search query must contain 1–500 characters", 400)
	}
	if request.Operation == "fetch" && (!ValidURL(request.URL, false) || len(request.URL) > 8192) {
		return Fail("SEARCH_INVALID_ARGUMENT", "Enter a public HTTP(S) page URL without embedded credentials", 400)
	}
	capability, _ := CapabilityFor(request.Profile.Family)
	if request.Operation == "fetch" && (!capability.SupportsFetch || request.Profile.Limits.MaxFetches == 0) {
		return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "This search configuration does not allow page fetching", 422)
	}
	if request.Operation == "search" && request.Profile.Family == "deepseek" && request.Profile.Mode == "search" && utf8.RuneCountInString(filteredQuery(request.Query, request.Profile.Filters)) > 128 {
		return Fail("SEARCH_INVALID_ARGUMENT", "DeepSeek search query including filters must not exceed 128 characters", 400)
	}
	if request.Profile.Native() && !(request.Operation == "fetch" && request.Profile.Family != "openrouter") {
		if request.Model.NameID == "" || !request.Model.Enabled || request.Model.Family != request.Profile.Family {
			return Fail("SEARCH_PROFILE_UNAVAILABLE", "Select an enabled research model from the same service", 422)
		}
		if request.Profile.Limits.StrictBudget && !request.Profile.HardLimitKnown() {
			return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "This native route cannot guarantee the hard search limit", 422)
		}
		if request.Profile.Family == "deepseek" && request.Model.NameID != "deepseek-v4-pro" {
			return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "DeepSeek native web search currently requires deepseek-v4-pro", 422)
		}
		if request.Profile.Family == "kimi" && !strings.HasPrefix(request.Model.NameID, "kimi-k3") {
			return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "Use a documented Kimi K3 Responses model or standalone Kimi search", 422)
		}
	}
	return nil
}

// executeStandalone maps each documented request schema without sharing incompatible field names.
func executeStandalone(ctx context.Context, request ExecuteRequest) (Response, error) {
	profile := request.Profile
	response := EmptyResponse()
	response.Engine = profile.Family
	method, address, headers, body := "POST", profile.Endpoint, map[string]string{}, map[string]any{}
	if request.Key != "" {
		headers["Authorization"] = "Bearer " + request.Key
	}
	limit := profile.Limits.MaxResults
	switch profile.Family {
	case "deepseek":
		operation := "search"
		if request.Operation == "fetch" {
			operation = "fetch"
		}
		var err error
		address, err = ToolEndpoint(address, operation)
		if err != nil {
			return response, err
		}
		if operation == "fetch" {
			body = map[string]any{"url": request.URL}
		} else {
			query := filteredQuery(request.Query, profile.Filters)
			if utf8.RuneCountInString(query) > 128 {
				return response, Fail("SEARCH_INVALID_ARGUMENT", "DeepSeek search query including filters must not exceed 128 characters", 400)
			}
			body = map[string]any{"query": query, "count": limit, "need_content": profile.Filters.IncludeContent}
		}
	case "kimi":
		operation := "search"
		if profile.Mode == "pro" {
			operation = "search_pro"
		}
		if request.Operation == "fetch" {
			operation = "fetch"
		}
		var err error
		address, err = ToolEndpoint(address, operation)
		if err != nil {
			return response, err
		}
		if operation == "fetch" {
			body = map[string]any{"url": request.URL}
		} else {
			body = map[string]any{"text_query": request.Query, "limit": limit, "timeout_seconds": profile.Limits.TimeoutSeconds}
			if operation == "search" {
				body["include_content"] = profile.Filters.IncludeContent
			} else {
				if len(profile.Filters.AllowedDomains) > 0 {
					body["sites"] = profile.Filters.AllowedDomains
				}
				if profile.Filters.StartDate != "" || profile.Filters.EndDate != "" {
					window := map[string]string{}
					if profile.Filters.StartDate != "" {
						window["start"] = profile.Filters.StartDate
					}
					if profile.Filters.EndDate != "" {
						window["end"] = profile.Filters.EndDate
					}
					body["time_window"] = window
				}
			}
		}
	case "tavily":
		body = map[string]any{"query": request.Query, "max_results": limit, "search_depth": "basic", "include_raw_content": profile.Filters.IncludeContent}
		if len(profile.Filters.AllowedDomains) > 0 {
			body["include_domains"] = profile.Filters.AllowedDomains
		}
		if len(profile.Filters.ExcludedDomains) > 0 {
			body["exclude_domains"] = profile.Filters.ExcludedDomains
		}
		if profile.Filters.StartDate != "" {
			body["start_date"] = profile.Filters.StartDate
		}
		if profile.Filters.EndDate != "" {
			body["end_date"] = profile.Filters.EndDate
		}
		if profile.Filters.Language != "" {
			body["language"] = profile.Filters.Language
		}
	case "bocha":
		body = map[string]any{"query": request.Query, "count": limit, "summary": true}
	case "exa":
		delete(headers, "Authorization")
		headers["x-api-key"] = request.Key
		if request.Operation == "fetch" {
			parsed, _ := url.Parse(address)
			parsed.Path = strings.TrimSuffix(parsed.Path, "/search") + "/contents"
			address = parsed.String()
			body = map[string]any{"ids": []string{request.URL}, "text": map[string]any{"maxCharacters": profile.Limits.MaxContentChars}}
		} else {
			body = map[string]any{"query": request.Query, "numResults": limit, "contents": map[string]any{"text": map[string]any{"maxCharacters": min(6000, profile.Limits.MaxContentChars)}}}
			if len(profile.Filters.AllowedDomains) > 0 {
				body["includeDomains"] = profile.Filters.AllowedDomains
			}
			if len(profile.Filters.ExcludedDomains) > 0 {
				body["excludeDomains"] = profile.Filters.ExcludedDomains
			}
			if profile.Filters.StartDate != "" {
				body["startPublishedDate"] = profile.Filters.StartDate + "T00:00:00Z"
			}
			if profile.Filters.EndDate != "" {
				body["endPublishedDate"] = profile.Filters.EndDate + "T23:59:59Z"
			}
		}
	case "serper":
		delete(headers, "Authorization")
		headers["X-API-KEY"] = request.Key
		body = map[string]any{"q": request.Query, "num": limit}
	case "searxng", "wikipedia":
		method = "GET"
		parsed, err := url.Parse(address)
		if err != nil {
			return response, Fail("SEARCH_INVALID_ARGUMENT", "Invalid search endpoint", 400)
		}
		values := parsed.Query()
		values.Set("format", "json")
		if profile.Family == "searxng" {
			values.Set("q", request.Query)
			language := profile.Filters.Language
			if language == "" {
				language = request.Language
			}
			values.Set("language", language)
		} else {
			values.Set("action", "query")
			values.Set("list", "search")
			values.Set("srsearch", request.Query)
			values.Set("srlimit", fmt.Sprint(limit))
			values.Set("utf8", "1")
		}
		parsed.RawQuery = values.Encode()
		address = parsed.String()
	default:
		return response, Fail("SEARCH_CAPABILITY_UNSUPPORTED", "Unsupported standalone search adapter", 422)
	}
	var payload any = body
	if method == "GET" {
		payload = nil
	}
	value, err := RequestJSON(ctx, method, address, payload, headers, profile.Limits.TimeoutSeconds)
	if err != nil {
		return response, err
	}
	if request.Operation == "fetch" && (profile.Family == "deepseek" || profile.Family == "kimi") {
		key := "content"
		if profile.Family == "kimi" {
			key = "markdown"
		}
		content, ok := value[key].(string)
		if !ok {
			return response, Fail("SEARCH_INVALID_RESPONSE", "Fetch service returned no readable content field", 502)
		}
		if strings.TrimSpace(content) != "" {
			response.Results = []Result{newResult(profile, request.URL, Text(value, "title"), content, "fetched_page")}
		}
		response.Usage.FetchCalls = integer(1)
		response.Usage.SearchCalls = Zero()
	} else {
		results, err := parseStandalone(profile, value, request.Operation)
		if err != nil {
			return response, err
		}
		response.Results = results
		if request.Operation == "fetch" {
			response.Usage.FetchCalls = integer(1)
			response.Usage.SearchCalls = Zero()
		} else {
			response.Usage.SearchCalls = integer(1)
			response.Usage.FetchCalls = Zero()
		}
	}
	response.SearchState = "executed"
	response.TestVerdict = "passed"
	if len(response.Results) == 0 {
		response.TestVerdict = "connected_empty"
	}
	return response, nil
}

// filteredQuery uses documented DeepSeek operators and keeps query truncation an explicit error.
func filteredQuery(query string, filters Filters) string {
	parts := []string{query}
	if len(filters.AllowedDomains) > 0 {
		sites := []string{}
		for _, domain := range filters.AllowedDomains {
			sites = append(sites, "site:"+domain)
		}
		parts = append(parts, strings.Join(sites, " OR "))
	}
	for _, domain := range filters.ExcludedDomains {
		parts = append(parts, "-site:"+domain)
	}
	if filters.Language != "" {
		parts = append(parts, "lang:"+filters.Language)
	}
	if filters.StartDate != "" {
		parts = append(parts, "after:"+filters.StartDate)
	}
	if filters.EndDate != "" {
		parts = append(parts, "before:"+filters.EndDate)
	}
	return strings.Join(parts, " ")
}

// integer records a known call count without confusing unavailable usage with zero.
func integer(value int) *int { return &value }
