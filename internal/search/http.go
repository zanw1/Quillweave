package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 2_000_000

// RequestJSON bounds reads and time, refuses credential redirects, and never exposes upstream secret echoes.
func RequestJSON(ctx context.Context, method, address string, body any, headers map[string]string, timeout int) (map[string]any, error) {
	if !ValidURL(address, true) {
		return nil, Fail("SEARCH_INVALID_ARGUMENT", "Invalid search service endpoint", 400)
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, Fail("SEARCH_INVALID_ARGUMENT", "Invalid search request", 400)
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return nil, Fail("SEARCH_INVALID_ARGUMENT", "Invalid search request", 400)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Quillweave/3 web research")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	client := http.Client{Timeout: time.Duration(timeout) * time.Second, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) > 3 || next.URL.Scheme != via[0].URL.Scheme || next.URL.Host != via[0].URL.Host {
			return Fail("SEARCH_REDIRECT_REFUSED", "Search service redirect changed its credential destination", 502)
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, Fail("SEARCH_CANCELLED", "Search was cancelled", 409)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return nil, Fail("SEARCH_TIMEOUT", "Search service timed out", 504)
		}
		return nil, Fail("SEARCH_NETWORK_ERROR", "Search service could not be reached", 502)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code, message := "SEARCH_PROVIDER_ERROR", "Search service returned an error"
		switch response.StatusCode {
		case 401:
			code, message = "SEARCH_AUTH_REQUIRED", "Search service rejected the API key"
		case 402:
			code, message = "SEARCH_QUOTA_EXHAUSTED", "Search service balance is insufficient"
		case 403:
			code, message = "SEARCH_ACCESS_DENIED", "Search service denied this account, region or request"
		case 429:
			code, message = "SEARCH_RATE_LIMITED", "Search service rate limit was reached"
		}
		fault := Fail(code, message, 502)
		fault.Details = map[string]any{"upstream_status": response.StatusCode}
		for _, header := range []string{"X-Msh-Track-Id", "X-Request-Id", "Request-Id"} {
			if id := response.Header.Get(header); id != "" && len(id) <= 160 && !strings.ContainsAny(id, "\r\n") {
				safe := true
				for _, value := range headers {
					secret := strings.TrimPrefix(value, "Bearer ")
					if secret != "" && strings.Contains(id, secret) {
						safe = false
					}
				}
				if safe {
					fault.Details["request_id"] = id
				}
				break
			}
		}
		return nil, fault
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, Fail("SEARCH_RESPONSE_ERROR", "Search response could not be read", 502)
	}
	if len(raw) > maxResponseBytes {
		return nil, Fail("SEARCH_RESPONSE_TOO_LARGE", "Search response exceeds the supported size", 502)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil || value == nil {
		return nil, Fail("SEARCH_INVALID_RESPONSE", "Search service returned invalid JSON", 502)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, Fail("SEARCH_INVALID_RESPONSE", "Search service returned trailing JSON data", 502)
	}
	if value["error"] != nil {
		return nil, Fail("SEARCH_PROVIDER_ERROR", "Search service returned an error response", 502)
	}
	return value, nil
}

// ToolEndpoint appends documented tool paths without doubling an existing v1 or accepting a query-bearing base.
func ToolEndpoint(base, operation string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || parsed.RawQuery != "" || !ValidURL(base, true) {
		return "", Fail("SEARCH_INVALID_ARGUMENT", "Configure a search API base URL without query parameters", 400)
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(path, "/tools/search") || strings.HasSuffix(path, "/tools/search_pro") || strings.HasSuffix(path, "/tools/fetch") {
		path = path[:strings.LastIndex(path, "/tools/")]
	}
	if !strings.HasSuffix(path, "/v1") {
		path += "/v1"
	}
	parsed.Path = path + "/tools/" + operation
	return parsed.String(), nil
}

// ModelEndpoint uses the configured API prefix and replaces only a known completion suffix.
func ModelEndpoint(base, suffix string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || parsed.RawQuery != "" || !ValidURL(base, true) {
		return "", Fail("SEARCH_INVALID_ARGUMENT", "Configure a model API base URL without query parameters", 400)
	}
	path := strings.TrimRight(parsed.Path, "/")
	for _, ending := range []string{"/chat/completions", "/responses", "/messages"} {
		path = strings.TrimSuffix(path, ending)
	}
	if !strings.HasSuffix(path, "/v1") {
		path += "/v1"
	}
	parsed.Path = path + "/" + suffix
	return parsed.String(), nil
}

// Object reads only structured supplier fields, never URLs invented in free-form model prose.
func Object(value map[string]any, key string) map[string]any {
	nested, _ := value[key].(map[string]any)
	return nested
}

// Text accepts string fields without converting arbitrary JSON into apparent source text.
func Text(value map[string]any, key string) string { text, _ := value[key].(string); return text }

// Number preserves reported zero while keeping unavailable usage values nullable.
func Number(value any) *int {
	var result int
	switch number := value.(type) {
	case json.Number:
		v, err := number.Int64()
		if err != nil {
			return nil
		}
		result = int(v)
	case float64:
		result = int(number)
	case int:
		result = number
	default:
		return nil
	}
	if result < 0 {
		return nil
	}
	return &result
}

// Zero returns a stable pointer for a documented no-call result.
func Zero() *int { value := 0; return &value }
