package search

import (
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var tags = regexp.MustCompile(`<[^>]*>`)

// sortedResultKeys gives citations deterministic ordering when supplier blocks repeat a URL.
func sortedResultKeys(results map[string]Result) []string {
	keys := make([]string, 0, len(results))
	for key := range results {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// newResult gives source cards stable IDs and never claims a snippet was read as a complete page.
func newResult(profile Profile, address, title, content, reading string) Result {
	if title == "" {
		title = hostOf(address)
	}
	return Result{ID: Digest(address)[:24], Title: title, URL: address, Content: content, Provider: profile.Name, Engine: profile.Family, RetrievedAt: time.Now().UTC().Format(time.RFC3339Nano), Reading: reading}
}

// parseStandalone checks supplier arrays before normalizing them, so a wrong schema cannot pass as no results.
func parseStandalone(profile Profile, value map[string]any, operation string) ([]Result, error) {
	var raw any
	switch profile.Family {
	case "deepseek", "tavily", "exa", "searxng":
		raw = value["results"]
	case "kimi":
		raw = value["search_results"]
	case "serper":
		raw = value["organic"]
	case "bocha":
		raw = Object(Object(value, "data"), "webPages")["value"]
	case "wikipedia":
		raw = Object(value, "query")["search"]
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, Fail("SEARCH_INVALID_RESPONSE", "Search service returned an unexpected result schema", 502)
	}
	results := []Result{}
	for _, item := range entries {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, Fail("SEARCH_INVALID_RESPONSE", "Search service returned an invalid source record", 502)
		}
		title, address := firstText(entry, "title", "name"), firstText(entry, "url", "link")
		if profile.Family == "wikipedia" {
			base, _ := url.Parse(profile.Endpoint)
			base.Path = "/wiki/" + strings.ReplaceAll(title, " ", "_")
			base.RawQuery = ""
			address = base.String()
		}
		if !ValidURL(address, false) {
			continue
		}
		content := firstText(entry, "content", "text", "summary", "snippet", "description")
		reading := "search_excerpt"
		if profile.Family == "deepseek" {
			if Text(entry, "content") != "" {
				content = Text(entry, "content")
				reading = "fetched_page"
			} else if Text(entry, "summary") != "" {
				content = Text(entry, "summary")
				reading = "model_summary"
			}
		}
		if profile.Family == "kimi" && Text(entry, "text") != "" {
			reading = "fetched_page"
		}
		if profile.Family == "tavily" && Text(entry, "raw_content") != "" {
			content = Text(entry, "raw_content")
			reading = "fetched_page"
		}
		passages := []string{}
		if chunks, ok := entry["chunks"].([]any); ok {
			for _, chunk := range chunks {
				data, _ := chunk.(map[string]any)
				if text := Text(data, "text"); text != "" {
					passages = append(passages, text)
				}
			}
		}
		if len(passages) > 0 {
			content = strings.Join(passages, "\n\n")
			reading = "provider_passage"
		}
		if operation == "fetch" {
			reading = "fetched_page"
		}
		if profile.Family != "deepseek" && profile.Family != "kimi" {
			content = html.UnescapeString(tags.ReplaceAllString(content, ""))
		}
		result := newResult(profile, address, title, content, reading)
		result.Snippet = Text(entry, "snippet")
		result.Passages = passages
		result.PublishedAt = firstText(entry, "date", "publishedDate")
		results = append(results, result)
		if len(results) >= profile.Limits.MaxResults {
			break
		}
	}
	return results, nil
}

// firstText selects a documented string field without converting untrusted JSON objects to prose.
func firstText(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := Text(value, key); text != "" {
			return text
		}
	}
	return ""
}

// sanitizeResponse bounds retained source text and removes exact secret echoes before storage or display.
func sanitizeResponse(response Response, request ExecuteRequest) Response {
	remaining := request.Profile.Limits.MaxContentChars
	secrets := []string{request.Key, request.Model.APIKey}
	clean := func(text string) string {
		for _, secret := range secrets {
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "[redacted]")
			}
		}
		return text
	}
	results := []Result{}
	for _, result := range response.Results {
		if len(results) >= request.Profile.Limits.MaxResults {
			break
		}
		if len(result.URL) > 8192 {
			continue
		}
		if clean(result.URL) != result.URL {
			continue
		}
		original := result.Content
		result.Title = clean(result.Title)
		if len([]rune(result.Title)) > 300 {
			result.Title = string([]rune(result.Title)[:300])
		}
		result.Content = clean(result.Content)
		result.Snippet = clean(result.Snippet)
		result.Redacted = result.Content != original
		for index, passage := range result.Passages {
			result.Passages[index] = clean(passage)
		}
		text := []rune(result.Content)
		if len(text) > remaining {
			result.Content = string(text[:max(0, remaining)])
			result.Truncated = true
		}
		remaining = max(0, remaining-len([]rune(result.Content)))
		if len(result.Passages) > 0 {
			result.Passages = []string{result.Content}
		}
		if len([]rune(result.Snippet)) > 1000 {
			result.Snippet = string([]rune(result.Snippet)[:1000])
		}
		results = append(results, result)
	}
	response.Results = results
	if response.Answer != nil {
		text := []rune(clean(response.Answer.Text))
		if len(text) > request.Profile.Limits.MaxContentChars {
			text = text[:request.Profile.Limits.MaxContentChars]
		}
		response.Answer.Text = string(text)
		citations := []Citation{}
		for _, citation := range response.Answer.Citations {
			if len(citations) >= request.Profile.Limits.MaxResults {
				break
			}
			if !ValidURL(citation.URL, false) || len(citation.URL) > 8192 || clean(citation.URL) != citation.URL {
				continue
			}
			citation.Title = clean(citation.Title)
			citation.Quote = clean(citation.Quote)
			if len([]rune(citation.Title)) > 300 {
				citation.Title = string([]rune(citation.Title)[:300])
			}
			if len([]rune(citation.Quote)) > 1000 {
				citation.Quote = string([]rune(citation.Quote)[:1000])
			}
			citations = append(citations, citation)
		}
		response.Answer.Citations = citations
	}
	return response
}
