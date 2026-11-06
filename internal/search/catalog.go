package search

import "strings"

const CapabilityVersion = "search-2026-11-06-v1"

type Capability struct {
	Family        string   `json:"family"`
	Name          string   `json:"name"`
	Modes         []string `json:"modes"`
	Regions       []string `json:"regions"`
	Endpoint      string   `json:"endpoint"`
	RequiresKey   bool     `json:"requires_key"`
	SupportsFetch bool     `json:"supports_fetch"`
	MaxResults    int      `json:"max_results"`
	MaxQueryChars int      `json:"max_query_chars"`
	Docs          string   `json:"docs"`
}

// Catalog advertises documented protocols, never successful account access merely from a brand name.
func Catalog() []Capability {
	return []Capability{
		{"deepseek", "DeepSeek", []string{"search", "native"}, []string{"global"}, "https://api.deepseek.com", true, true, 50, 128, "https://api-docs.deepseek.com/guides/search/"},
		{"kimi", "Kimi", []string{"basic", "pro", "native"}, []string{"cn", "global"}, "https://api.moonshot.cn/v1", true, true, 20, 500, "https://platform.kimi.com/docs/guide/best-practices-for-web-search"},
		{"openrouter", "OpenRouter", []string{"server_tool"}, []string{"global"}, "https://openrouter.ai/api/v1", true, true, 25, 500, "https://openrouter.ai/docs/guides/features/server-tools/web-search"},
		{"tavily", "Tavily", []string{"search"}, []string{"global"}, "https://api.tavily.com/search", true, false, 20, 500, "https://docs.tavily.com/documentation/api-reference/endpoint/search"},
		{"bocha", "Bocha", []string{"search"}, []string{"cn"}, "https://api.bocha.cn/v1/web-search", true, false, 50, 500, "https://open.bocha.cn/"},
		{"exa", "Exa", []string{"search"}, []string{"global"}, "https://api.exa.ai/search", true, true, 25, 500, "https://exa.ai/docs/reference/search"},
		{"serper", "Serper", []string{"search"}, []string{"global"}, "https://google.serper.dev/search", true, false, 20, 500, "https://serper.dev/"},
		{"searxng", "SearXNG", []string{"search"}, []string{"global"}, "", false, false, 25, 500, "https://docs.searxng.org/dev/search_api.html"},
		{"wikipedia", "Wikipedia", []string{"search"}, []string{"global"}, "https://zh.wikipedia.org/w/api.php", false, false, 50, 500, "https://www.mediawiki.org/wiki/API:Search"},
	}
}

// CapabilityFor resolves a declared family and rejects unsupported pseudo-providers.
func CapabilityFor(family string) (Capability, error) {
	for _, capability := range Catalog() {
		if capability.Family == family {
			return capability, nil
		}
	}
	return Capability{}, Fail("SEARCH_CAPABILITY_UNSUPPORTED", "Unsupported search service", 422)
}

// DefaultProfile gives new paid connections safe explicit defaults without enabling them.
func DefaultProfile(family string) Profile {
	capability, _ := CapabilityFor(family)
	mode, region := "search", "global"
	if len(capability.Modes) > 0 {
		mode = capability.Modes[0]
	}
	if len(capability.Regions) > 0 {
		region = capability.Regions[0]
	}
	return Profile{Family: family, Name: capability.Name + " Search", Mode: mode, Region: region, Endpoint: capability.Endpoint, CredentialSource: "own", Engine: engineDefault(family), Limits: Limits{MaxSearches: 2, MaxResults: 5, MaxFetches: 1, TimeoutSeconds: 20, MaxContentChars: 20000, StrictBudget: true}}
}

// engineDefault pins a bounded OpenRouter engine instead of selecting an unbounded native route.
func engineDefault(family string) string {
	if family == "openrouter" {
		return "exa"
	}
	return ""
}

// Native reports an additional model request, whose provider-side tool calls need separate limits.
func (profile Profile) Native() bool {
	return profile.Mode == "native" || profile.Family == "openrouter"
}

// HardLimitKnown is conservative for auto/native routes because their upstream may ignore max_uses.
func (profile Profile) HardLimitKnown() bool {
	return !profile.Native() || (profile.Family == "openrouter" && contains([]string{"exa", "parallel", "perplexity", "firecrawl"}, profile.Engine))
}

// ModelFamily recognizes explicit official identities without classifying arbitrary gateways by model name.
func ModelFamily(provider, address string) string {
	name := strings.ToLower(provider)
	if contains([]string{"deepseek", "moonshot", "kimi", "openrouter"}, name) {
		if name == "moonshot" {
			return "kimi"
		}
		return name
	}
	endpoint := strings.ToLower(address)
	for _, pair := range [][2]string{{"api.deepseek.com", "deepseek"}, {"api.moonshot.cn", "kimi"}, {"api.moonshot.ai", "kimi"}, {"openrouter.ai", "openrouter"}} {
		if hostOf(endpoint) == pair[0] {
			return pair[1]
		}
	}
	return ""
}

// contains compares bounded enum values without accepting an accidental prefix match.
func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
