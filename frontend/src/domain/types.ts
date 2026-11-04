export interface Version {
  id: string;
  created_at: string;
  title: string;
  body: string;
  synopsis: string;
  status: string;
  target_words: number | null;
}
export interface Chapter {
  id: string;
  number: number;
  title: string;
  body: string;
  synopsis: string;
  status: string;
  versions: Version[];
  target_words: number | null;
}
export interface Entity {
  id: string;
  name: string;
  kind: string;
  aliases: string[];
  description: string;
}
export interface Source {
  id: string;
  title: string;
  category: string;
  url: string;
  content: string;
  created_at: string;
}
export interface Fact {
  id: string;
  entity: string;
  kind: string;
  claim: string;
  source_id: string;
  source_hash: string;
  quote: string;
  chapter: number;
  story_time: string;
  location: string;
  related_entity: string;
  epistemic: string;
  status: string;
  model: string;
  created_at: string;
}
export interface Project {
  id: string;
  title: string;
  genre: string;
  premise: string;
  style: string;
  outline: string;
  revision: number;
  created_at: string;
  updated_at: string;
  chapters: Chapter[];
  entities: Entity[];
  facts: Fact[];
  sources: Source[];
  wiki_purpose: string;
  compilations: WikiCompilation[];
  writing_style: string;
  default_chapter_words: number;
  narrative_pov: string;
  model_overrides: Record<string, string>;
}
export interface MemorySection {
  summary: string;
  evidence_ids: string[];
  questions: string[];
}
export interface WikiCompilation {
  id: string;
  source_id: string;
  source_hash: string;
  rules_hash: string;
  fingerprint: string;
  compiler_version: string;
  model: string;
  chunks: number;
  sections: MemorySection[];
  status: string;
  created_at: string;
}
export interface WikiHealth {
  compiler_version: string;
  compilations: {
    record: WikiCompilation;
    current: boolean;
    source_title: string;
  }[];
  findings: {
    kind: string;
    severity: string;
    title: string;
    detail: string;
    entity: string;
    source_id: string;
  }[];
}
export interface ProjectCard {
  id: string;
  title: string;
  genre: string;
  premise: string;
  updated_at: string;
  chapter_count: number;
  word_count: number;
}
export interface ProviderConfig {
  protocol: string;
  base_url: string;
  model: string;
  temperature: number | null;
  max_tokens: number;
  timeout_seconds: number;
  context_chars: number;
  api_key: string;
  provider_id?: string;
  thinking_mode?: string;
  reasoning_effort?: string;
  api_version?: string;
}
export interface ProviderPreset {
  id: string;
  name: string;
  description: string;
  docs_url: string;
  models: string[];
  endpoints: { name: string; url: string }[];
  thinking_modes: string[];
  reasoning_efforts: string[];
  temperature_policy: string;
  temperature_fixed_models?: string[];
  config: ProviderConfig;
}
export interface ProviderCatalog {
  checked_at: string;
  presets: ProviderPreset[];
}
export interface ModelProfile {
  id: string;
  name: string;
  enabled: boolean;
  config: ProviderConfig;
}
export interface ModelSettings {
  schema_version: number;
  revision: number;
  profiles: ModelProfile[];
  default_profile: string;
  routes: Record<string, string>;
}
export interface ModelSettingsResponse {
  settings: ModelSettings;
  key_status: Record<string, boolean>;
}
export type SecretUpdate =
  | { action: "preserve" }
  | { action: "clear" }
  | { action: "replace"; value: string };
export interface StylePreset {
  id: string;
  name: string;
  description: string;
  guidance: string;
}
export interface GraphNode {
  id: string;
  label: string;
  kind: string;
  entity_id: string | null;
  source_id: string | null;
  description: string;
  evidence_ids: string[];
}
export interface GraphEdge {
  id: string;
  source: string;
  target: string;
  kind: string;
  status: string;
  label: string;
  evidence_ids: string[];
}
export interface KnowledgeGraph {
  nodes: GraphNode[];
  edges: GraphEdge[];
  omitted_stale: number;
  unresolved_relations: number;
  through_chapter: number;
}
export interface ContextItem {
  id: string;
  title: string;
  kind: string;
  text: string;
  score: number;
  reason?: string;
}
export interface Message {
  role: "user" | "assistant";
  content: string;
  source_job_id?: string;
}
export interface Prompt {
  messages: Message[];
  context: ContextItem[];
  source_hash: string;
  source_id: string;
  chars: number;
}
export interface Job {
  id: string;
  project_id: string;
  chapter_id: string;
  action: string;
  instruction: string;
  status: string;
  text: string;
  error: string;
  progress: string;
  created_at: string;
  model: string;
  prompt: Prompt;
}
export type Page =
  | "home"
  | "write"
  | "plan"
  | "wiki"
  | "knowledge"
  | "methods"
  | "settings";

export const entityKinds: Record<string, string> = {
  character: "Characters",
  faction: "Factions",
  location: "Locations",
  world: "Worldbuilding",
  item: "Items",
  concept: "Concepts",
};
export const factKinds: Record<string, string> = {
  event: "Actions and choices",
  emotion: "Emotional changes",
  relationship: "Relationships",
  foreshadow: "Foreshadowing",
  world: "World rules",
  knowledge: "Reference knowledge",
  knowledge_state: "Character knowledge",
};
export const actions: Record<string, string> = {
  ask: "Discuss",
  continue: "Continue chapter",
  rewrite: "Rewrite chapter",
  review: "Review consistency",
  architecture: "Develop story setup",
  outline: "Create outline",
  wiki: "Compile Wiki",
};
