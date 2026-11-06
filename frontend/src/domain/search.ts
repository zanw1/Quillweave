export type SearchFamily =
  | "deepseek"
  | "kimi"
  | "openrouter"
  | "tavily"
  | "bocha"
  | "exa"
  | "serper"
  | "searxng"
  | "wikipedia";
export interface SearchSelection {
  mode: "inherit" | "off" | "profile";
  profile_id?: string;
}
export interface SearchLimits {
  max_searches: number;
  max_results: number;
  max_fetches: number;
  timeout_seconds: number;
  max_content_chars: number;
  strict_budget: boolean;
}
export interface SearchFilters {
  language?: string;
  allowed_domains?: string[];
  excluded_domains?: string[];
  start_date?: string;
  end_date?: string;
  include_content: boolean;
}
export interface SearchProfile {
  id: string;
  legacy_id?: string;
  name: string;
  family: SearchFamily;
  mode: string;
  region: string;
  endpoint: string;
  enabled: boolean;
  credential_source: "own" | "model_ref";
  model_profile_id?: string;
  engine?: string;
  search_mode?: string;
  limits: SearchLimits;
  filters: SearchFilters;
}
export interface SearchSettings {
  schema_version: number;
  enabled: boolean;
  profiles: SearchProfile[];
  default_profile_id: string;
  fallback_policy: "none" | "ordered" | "legacy";
  fallback_profile_ids: string[];
  bindings: Record<string, SearchSelection>;
}
export interface SearchModel {
  id: string;
  name: string;
  family: string;
  protocol: string;
  base_url: string;
  model: string;
  enabled: boolean;
  max_tokens: number;
}
export interface SearchSettingsView {
  revision: number;
  model_revision: number;
  settings: SearchSettings;
  key_status: Record<string, boolean>;
  models: SearchModel[];
  capability_version: string;
}
export interface SearchCapability {
  family: SearchFamily;
  name: string;
  modes: string[];
  regions: string[];
  endpoint: string;
  requires_key: boolean;
  supports_fetch: boolean;
  max_results: number;
  max_query_chars: number;
  docs: string;
}
export interface SearchCatalog {
  version: string;
  services: SearchCapability[];
  presets: SearchProfile[];
}
export interface SearchFault {
  code: string;
  message: string;
  details?: Record<string, unknown>;
}
export interface SearchSource {
  id: string;
  title: string;
  url: string;
  content: string;
  snippet?: string;
  passages?: string[];
  provider: string;
  engine?: string;
  retrieved_at: string;
  published_at?: string;
  reading: string;
  truncated?: boolean;
  redacted?: boolean;
}
export interface SearchAnswer {
  text: string;
  citations: { url: string; title: string; quote?: string }[];
}
export interface SearchUsage {
  attempts_reserved: number;
  search_calls: number | null;
  fetch_calls: number | null;
  model_input_tokens: number | null;
  model_output_tokens: number | null;
  reported_cost: number | null;
  currency?: string;
  cost_status: string;
}
export interface SearchResponse {
  results: SearchSource[];
  answer?: SearchAnswer;
  usage: SearchUsage;
  warnings: string[];
  engine?: string;
  search_state: "executed" | "not_triggered" | "unknown";
  test_verdict: string;
}
export interface SearchRun {
  id: string;
  revision: number;
  project_id?: string;
  request_id: string;
  purpose: string;
  operation: string;
  status: string;
  query: string;
  url?: string;
  language: string;
  effective_profile: {
    profile: SearchProfile;
    fallbacks: SearchProfile[];
    source: string;
    settings_revision: number;
    model_revision: number;
    task_model_profile_id?: string;
    model: SearchModel;
  };
  response: SearchResponse;
  attempts: {
    profile_id: string;
    provider: string;
    status: string;
    error?: SearchFault;
  }[];
  reserved_searches: number;
  reserved_fetches: number;
  native_model_calls: number;
  error?: SearchFault;
  created_at: string;
  finished_at?: string;
}
export interface SearchProfileInfo {
  profile: SearchProfile;
  revision: number;
  key_configured: boolean;
  test_valid: boolean;
  last_test?: SearchRun;
}
export interface SearchProfilePage {
  rows: SearchProfile[];
  total: number;
  pageNo: number;
  pageSize: number;
  revision: number;
  key_status: Record<string, boolean>;
}

/** Keep explicit off distinguishable from an empty inherited selection in all selectors. */
export function selectionValue(selection?: SearchSelection): string {
  return selection?.mode === "profile"
    ? selection.profile_id || "inherit"
    : selection?.mode || "inherit";
}

/** Construct a typed selection instead of encoding off as an absent profile. */
export function searchSelection(value: string): SearchSelection {
  return value === "inherit" || value === "off"
    ? { mode: value }
    : { mode: "profile", profile_id: value };
}

/** Expose only known terminal states so an unexpected status cannot silently end a pending run. */
export function searchTerminal(status: string): boolean {
  return ["succeeded", "empty", "failed", "cancelled", "interrupted"].includes(
    status,
  );
}
