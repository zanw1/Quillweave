export interface WorldLocation {
  id: string;
  name: string;
  kind: "place" | "region";
  region_id: string;
  description: string;
  x: number;
  y: number;
  entity_ids: string[];
  plan_ids: string[];
  chapter_ids: string[];
}

export interface WorldRoute {
  id: string;
  from_id: string;
  to_id: string;
  transport: string;
  travel_hours: number;
  bidirectional: boolean;
  description: string;
}

export interface WritingScene {
  pov: string;
  location: string;
  goal: string;
  obstacle: string;
  choice: string;
  action: string;
  cost: string;
  outcome: string;
  knowledge: string;
  handoff: string;
  characters: { name: string; want: string; knowledge: string; response: string }[];
}

export interface StoryPlan {
  scene_cards?: WritingScene[];
  flexibility?: "" | "fixed" | "flexible";
  open_questions?: string[];
  id: string;
  parent_id: string;
  level: string;
  title: string;
  theme: string;
  goal: string;
  conflict: string;
  choice: string;
  consequence: string;
  pacing: string;
  pov: string;
  characters: string[];
  clues: string[];
  start_chapter: number;
  end_chapter: number;
  target_characters: number;
  status: string;
}
export interface StoryMemory {
  id: string;
  kind: string;
  entity: string;
  title: string;
  content: string;
  state: string;
  status: string;
  chapter: number;
  through_chapter: number;
  due_chapter: number;
  story_time: string;
  known_by: string[];
  source_id: string;
  source_hash: string;
  quote: string;
  session_id: string;
  supersedes: string;
  updated_at: string;
}
export interface StoryDecision {
  id: string;
  title: string;
  content: string;
  status: string;
  session_id: string;
  created_at: string;
}
export type StepStatus = "not_started" | "in_progress" | "ready";

export interface FlowStepReadiness {
  index: number;
  key: "setup" | "world" | "plan" | "write";
  status: StepStatus;
  count: number;
  hint: string;
}

export interface FlowReadiness {
  steps: FlowStepReadiness[];
  recommended_step: number;
}

export type ChangeTargetKind =
  | "project_meta"
  | "studio_goal"
  | "decision"
  | "entity"
  | "memory"
  | "plan"
  | "chapter"
  | "wiki"
  | "import_metadata";
export type MapTargetKind = "location" | "route";

export type ChangeOpType =
  | "upsert"
  | "delete"
  | "append_prose"
  | "replace_prose"
  | "restore";

export type ChangeSetStatus =
  | "staged"
  | "applied"
  | "undone"
  | "rejected"
  | "conflicted";

export interface ChangeOperation {
  proposal_id?: string;
  id: string;
  target_kind: ChangeTargetKind | MapTargetKind;
  target_id: string;
  op_type: ChangeOpType;
  title: string;
  summary: string;
  before_value?: unknown;
  preview_value?: unknown;
  after_value: unknown;
}

export interface ChangeSet {
  provenance?: {
    client: string;
    transport: string;
    writing_session_id?: string;
    wiki_session_id?: string;
    draft_hash?: string;
    evidence_version?: string;
    revisions?: number;
    review?: { summary: string; needs_revision: boolean; issues: { kind: string; scope?: string; quote: string; explanation: string; suggestion: string }[] };
  };
  reverts_changeset_id?: string;
  revert_conflicts?: { kind: string; id: string; current: unknown; expected: unknown; restore: unknown; resolution?: string }[];
  impacts?: { kind: string; id: string; title: string }[];
  id: string;
  project_id: string;
  session_id?: string;
  turn_id?: string;
  job_id?: string;
  proposal_links_version?: number;
  before_fact_references?:
    | {
        fact_id: string;
        entity: string;
        related_entity: string;
      }[]
    | null;
  source: string;
  commit_policy: string;
  status: ChangeSetStatus;
  base_revision: number;
  applied_revision?: number;
  preview_revision?: number;
  review_token?: string;
  skipped_operations?: ChangeOperation[];
  archived?: boolean;
  summary: string;
  operations: ChangeOperation[];
  created_at: string;
  updated_at: string;
}

export interface ChangeEffect {
  kind: string;
  id: string;
  before: unknown;
  after: unknown;
  resolution?: string;
}

export interface WorkspaceContext {
  active_step?: string;
  active_chapter_id?: string;
  active_entity_id?: string;
  active_plan_id?: string;
  active_source_id?: string;
  selected_text?: string;
  omit_selected_text?: boolean;
  omit_chapter?: boolean;
  omit_source?: boolean;
  omit_entity_ids?: string[];
  omit_plan_ids?: string[];
  omit_memory_ids?: string[];
}

export interface StudioState {
  language: string;
  mode: string;
  stage: string;
  target_characters: number;
  target_chapters: number;
  target_volumes: number;
  pacing: string;
  narrative_focus: string;
  audience: string;
  naming_style: string;
  forbidden_names: string[];
  skills: string[];
  preferences: string[];
  plans: StoryPlan[];
  memories: StoryMemory[];
  decisions: StoryDecision[];
  changesets?: ChangeSet[];
  locations?: WorldLocation[];
  routes?: WorldRoute[];
}
export interface ConversationTurn {
  id: string;
  role: "user" | "assistant";
  content: string;
  job_id: string;
  action: string;
  status: string;
  request_id: string;
  created_at: string;
}
export interface Conversation {
  archived?: boolean;
  model_selection?: import("./types").ChatModelSelection;
  id: string;
  project_id: string;
  chapter_id: string;
  title: string;
  parent_id: string;
  fork_turn_id: string;
  draft: string;
  summary: string;
  revision: number;
  created_at: string;
  updated_at: string;
  turns: ConversationTurn[];
}
export interface AgentStep {
  input?: string;
  started_at?: string;
  finished_at?: string;
  id: string;
  tool: string;
  status: string;
  detail: string;
}
export interface StudioProposal {
  mode?: string;
  id: string;
  kind: string;
  title: string;
  content: string;
  status: string;
  plan?: StoryPlan;
  memory?: StoryMemory;
}
export interface SearchProvider {
  id: string;
  kind: string;
  name: string;
  endpoint: string;
  enabled: boolean;
}
export interface IntegrationSettings {
  revision: number;
  search_revision: number;
  providers: SearchProvider[];
  search_enabled: boolean;
  max_searches: number;
}
export interface IntegrationResponse {
  settings: IntegrationSettings;
  key_status: Record<string, boolean>;
}
export interface ResearchResult {
  id: string;
  title: string;
  url: string;
  content: string;
  provider: string;
  retrieved_at: string;
  reading: string;
}
export interface ResearchResponse {
  results: ResearchResult[];
  warnings: string[];
  provider: string;
}
export interface WritingSkill {
  id: string;
  name: string;
  description: string;
  source: string;
  version: string;
  modes: string[];
}
