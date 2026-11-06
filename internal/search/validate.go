package search

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// ValidURL accepts explicit local gateways but never URL-embedded credentials or executable schemes.
func ValidURL(value string, endpoint bool) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.User == nil && (!endpoint || parsed.Fragment == "")
}

// hostOf normalizes only a parsed hostname, not a deceptive substring in another host's URL.
func hostOf(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// Identity excludes credentials while binding their reuse to the exact destination and account region.
func (profile Profile) Identity() string {
	return Digest(Canonical([]string{profile.ID, profile.Family, profile.Region, strings.TrimRight(profile.Endpoint, "/"), profile.CredentialSource, profile.ModelProfileID}))
}

// Fingerprint invalidates tests when public parameters change, without hashing a secret into a public identifier.
func (profile Profile) Fingerprint() string { return Digest(Canonical(profile)) }

// Digest gives durable provenance and idempotency keys for nonsensitive structured values.
func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Validate rejects unsupported combinations before any network or mutation side effect.
func (profile Profile) Validate() error {
	capability, err := CapabilityFor(profile.Family)
	if err != nil {
		return err
	}
	if profile.ID == "" || contains([]string{"inherit", "off"}, profile.ID) || len(profile.ID) > 100 || strings.TrimSpace(profile.Name) == "" || utf8.RuneCountInString(profile.Name) > 80 {
		return Fail("SEARCH_INVALID_ARGUMENT", "Enter a unique search profile name of 1–80 characters", 400)
	}
	if !contains(capability.Modes, profile.Mode) || !contains(capability.Regions, profile.Region) {
		return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "This search mode or account region is not supported", 422)
	}
	if !ValidURL(profile.Endpoint, true) && !(profile.Endpoint == "" && !profile.Enabled && profile.Family == "searxng") {
		return Fail("SEARCH_INVALID_ARGUMENT", "Enter an HTTP(S) search endpoint without embedded credentials or fragments", 400)
	}
	if profile.Family == "kimi" && ((hostOf(profile.Endpoint) == "api.moonshot.cn" && profile.Region != "cn") || (hostOf(profile.Endpoint) == "api.moonshot.ai" && profile.Region != "global")) {
		return Fail("SEARCH_INVALID_ARGUMENT", "Kimi account region must match its endpoint", 400)
	}
	if contains([]string{"deepseek", "kimi", "openrouter"}, profile.Family) {
		parsed, _ := url.Parse(profile.Endpoint)
		if parsed != nil && parsed.RawQuery != "" {
			return Fail("SEARCH_INVALID_ARGUMENT", "Search API bases must not contain query parameters or credentials", 400)
		}
	}
	if !contains([]string{"own", "model_ref"}, profile.CredentialSource) {
		return Fail("SEARCH_INVALID_ARGUMENT", "Invalid search credential source", 400)
	}
	if profile.Enabled && (profile.Native() || profile.CredentialSource == "model_ref") && profile.ModelProfileID == "" {
		return Fail("SEARCH_INVALID_ARGUMENT", "Select a model connection for this search mode", 400)
	}
	limits := profile.Limits
	maxResults := capability.MaxResults
	if profile.Family == "openrouter" && profile.Engine == "perplexity" {
		maxResults = 20
	}
	if limits.MaxSearches < 1 || limits.MaxSearches > 5 || limits.MaxResults < 1 || limits.MaxResults > maxResults || limits.MaxFetches < 0 || limits.MaxFetches > 5 || limits.TimeoutSeconds < 1 || limits.TimeoutSeconds > 60 || limits.MaxContentChars < 500 || limits.MaxContentChars > 100000 {
		return Fail("SEARCH_INVALID_ARGUMENT", "Search limits exceed the selected service's supported range", 400)
	}
	if profile.Family == "openrouter" && !contains([]string{"auto", "native", "exa", "parallel", "perplexity", "firecrawl"}, profile.Engine) {
		return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "Unsupported OpenRouter search engine", 422)
	}
	if profile.Family != "openrouter" && profile.Engine != "" {
		return Fail("SEARCH_INVALID_ARGUMENT", "This service does not accept a separate search engine", 400)
	}
	if profile.SearchMode != "" && !(profile.Family == "openrouter" && ((profile.Engine == "exa" && contains([]string{"instant", "fast", "auto", "deep-lite", "deep", "deep-reasoning"}, profile.SearchMode)) || (profile.Engine == "parallel" && contains([]string{"turbo", "fast", "basic", "advanced"}, profile.SearchMode)))) {
		return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "This engine does not support the requested search depth", 422)
	}
	return validateFilters(profile)
}

// validateFilters exposes differences instead of silently dropping a user's source constraints.
func validateFilters(profile Profile) error {
	filters := profile.Filters
	if filters.Language != "" && !contains([]string{"deepseek", "tavily", "searxng"}, profile.Family) {
		return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "This adapter does not support an explicit language filter", 422)
	}
	for _, domains := range [][]string{filters.AllowedDomains, filters.ExcludedDomains} {
		if len(domains) > 20 {
			return Fail("SEARCH_INVALID_ARGUMENT", "Too many search domain filters", 400)
		}
		seen := map[string]bool{}
		for _, domain := range domains {
			if domain == "" || strings.TrimSpace(domain) != domain || hostOf("https://"+domain) != strings.ToLower(domain) || strings.ContainsAny(domain, "/:?#@ *\t\n") || seen[domain] {
				return Fail("SEARCH_INVALID_ARGUMENT", "Enter unique hostnames without paths in domain filters", 400)
			}
			seen[domain] = true
		}
	}
	if profile.Family == "kimi" && profile.Mode != "pro" && (len(filters.AllowedDomains)+len(filters.ExcludedDomains) > 0 || filters.StartDate != "" || filters.EndDate != "") {
		return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "Kimi site and date filters require Pro mode", 422)
	}
	if profile.Family == "kimi" && (len(filters.AllowedDomains) > 5 || len(filters.ExcludedDomains) > 0) {
		return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "Kimi Pro supports at most five allowed sites and no excluded-site list", 422)
	}
	if profile.Family == "openrouter" && len(filters.AllowedDomains) > 0 && len(filters.ExcludedDomains) > 0 && profile.Engine != "exa" {
		return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "This engine accepts either allowed or excluded domains, not both", 422)
	}
	if profile.Mode == "native" && (len(filters.AllowedDomains)+len(filters.ExcludedDomains) > 0 || filters.StartDate != "" || filters.EndDate != "" || filters.IncludeContent || filters.Language != "") {
		return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "This native search mode cannot guarantee those filters; use standalone search", 422)
	}
	if profile.Family == "openrouter" && (filters.StartDate != "" || filters.EndDate != "" || filters.IncludeContent || filters.Language != "") {
		return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "These filters are not supported by this OpenRouter adapter", 422)
	}
	if contains([]string{"bocha", "serper", "searxng", "wikipedia"}, profile.Family) && (len(filters.AllowedDomains)+len(filters.ExcludedDomains) > 0 || filters.StartDate != "" || filters.EndDate != "" || filters.IncludeContent) {
		return Fail("SEARCH_CAPABILITY_UNSUPPORTED", "This adapter does not support those advanced filters", 422)
	}
	start, end := time.Time{}, time.Time{}
	for index, value := range []string{filters.StartDate, filters.EndDate} {
		if value == "" {
			continue
		}
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return Fail("SEARCH_INVALID_ARGUMENT", "Use valid YYYY-MM-DD search dates", 400)
		}
		if index == 0 {
			start = parsed
		} else {
			end = parsed
		}
	}
	if !start.IsZero() && !end.IsZero() && start.After(end) {
		return Fail("SEARCH_INVALID_ARGUMENT", "Search start date must not follow its end date", 400)
	}
	return nil
}

// ValidateSelection keeps an explicit off state distinct from inheritance and missing references.
func ValidateSelection(selection Selection) error {
	if !contains([]string{"inherit", "off", "profile"}, selection.Mode) || (selection.Mode == "profile") != (selection.ProfileID != "") {
		return Fail("SEARCH_INVALID_ARGUMENT", "Invalid search selection", 400)
	}
	return nil
}

// Validate ensures reference integrity and bounded collections before a settings transaction commits.
func (settings Settings) Validate() error {
	if settings.SchemaVersion != 1 || len(settings.Profiles) > 200 || len(settings.FallbackProfileIDs) > 4 || !contains([]string{"none", "ordered", "legacy"}, settings.FallbackPolicy) {
		return Fail("SEARCH_INVALID_ARGUMENT", "Invalid search settings version or size", 400)
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, profile := range settings.Profiles {
		if err := profile.Validate(); err != nil {
			return err
		}
		name := strings.ToLower(strings.TrimSpace(profile.Name))
		if ids[profile.ID] || names[name] {
			return Fail("SEARCH_INVALID_ARGUMENT", "Search profile IDs and names must be unique", 400)
		}
		ids[profile.ID] = true
		names[name] = true
	}
	if settings.DefaultProfileID != "" && !ids[settings.DefaultProfileID] {
		return Fail("SEARCH_PROFILE_UNAVAILABLE", "The default search profile does not exist", 422)
	}
	used := map[string]bool{settings.DefaultProfileID: true}
	for _, id := range settings.FallbackProfileIDs {
		if !ids[id] || used[id] {
			return Fail("SEARCH_INVALID_ARGUMENT", "Fallback profiles must exist, be unique and differ from the primary", 400)
		}
		used[id] = true
	}
	for modelID, selection := range settings.Bindings {
		if modelID == "" {
			return Fail("SEARCH_INVALID_ARGUMENT", "A model binding is missing its model", 400)
		}
		if err := ValidateSelection(selection); err != nil {
			return err
		}
		if selection.Mode == "profile" && !ids[selection.ProfileID] {
			return Fail("SEARCH_PROFILE_UNAVAILABLE", "A model binding references a missing search profile", 422)
		}
	}
	return nil
}
