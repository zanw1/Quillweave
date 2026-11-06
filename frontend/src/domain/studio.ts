export interface StoryPlan {
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
  hindsight_url: string;
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
export interface PolicyExperiment {
  id: string;
  title: string;
  created_at: string;
  cost_penalty: number;
  min_gain: number;
  policies: {
    id: string;
    order: string;
    max_calls: number;
    max_depth: number;
  }[];
  cases: {
    id: string;
    split: string;
    nodes: {
      id: string;
      parent_id: string;
      score: number;
      cost: number;
      text: string;
      reviewed: boolean;
    }[];
  }[];
}
export interface PolicyEvaluation {
  scores: {
    policy_id: string;
    split: string;
    cases: number;
    quality: number;
    cost: number;
    calls: number;
    reward: number;
  }[];
  candidate: string;
  promotable: boolean;
  reason: string;
}
