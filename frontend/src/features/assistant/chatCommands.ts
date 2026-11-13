import { t } from "../../lib/i18n";

export interface ChatCommand {
  name: string;
  label: string;
  description: string;
  group: string;
  intent?: string;
  prompt?: string;
  aliases?: string[];
}

export const chatCommands: ChatCommand[] = [
  {
    name: "model",
    label: "Choose model",
    description: "Choose the model for the next submission",
    group: "Conversation",
  },
  {
    name: "context",
    label: "Inspect context",
    description: "Inspect the actual request and token budget",
    group: "Conversation",
  },
  {
    name: "compact",
    label: "Compact conversation",
    description: "Save a checkpoint of earlier complete exchanges",
    group: "Conversation",
  },
  {
    name: "new",
    label: "New conversation",
    description: "Start a conversation with its own draft",
    group: "Conversation",
  },
  {
    name: "resume",
    label: "Conversations",
    description: "Search and resume a saved conversation",
    group: "Conversation",
  },
  {
    name: "brainstorm",
    label: "Brainstorm directions",
    description: "Compare a few story directions",
    group: "Writing",
    intent: "brainstorm",
    prompt:
      "Compare 2–3 distinct story directions from the current idea, covering character desires, conflicts, and trade-offs.",
  },
  {
    name: "name",
    label: "Titles & character names",
    description: "Explore titles and character names",
    group: "Writing",
    intent: "naming",
    prompt:
      "Suggest at least five distinctive book titles and five protagonist names, explaining their meaning and tone fit.",
  },
  {
    name: "outline",
    aliases: ["大纲", "章纲", "规划"],
    label: "Volume & chapter outline",
    description: "Break the discussion into a reviewable outline",
    group: "Writing",
    intent: "outline_breakdown",
    prompt:
      "Break down our discussed plot into volume and chapter outlines with POV, goal, obstacle, choice, and consequence.",
  },
  {
    name: "write",
    aliases: ["写作", "写章", "续写"],
    label: "Write this chapter",
    description:
      "Draft and review this chapter using its planning and saved context",
    group: "Writing",
    prompt:
      "Write the next complete scene using this chapter's planning and saved context, then review its continuity and reading experience.",
  },
  {
    name: "review",
    aliases: ["审阅", "检查"],
    label: "Review this chapter",
    description:
      "Review continuity and reading experience with source passages",
    group: "Writing",
    prompt:
      "Review the current chapter's continuity and reading experience. Locate each issue in the prose and suggest a focused edit.",
  },
  {
    name: "reference",
    label: "Analyze writing references",
    description: "Trace a reference scene's causes, choices and consequences",
    group: "Writing",
    prompt:
      "Read the selected writing reference. Analyze its starting situation, character goals, choices, feasible actions, reactions and lasting consequences. Cite the actual source passages and explain its applicability to this story. Keep the reference's characters and events separate from this work.",
  },
  {
    name: "polish",
    aliases: ["润色", "改写"],
    label: "Humanize chapter",
    description: "Polish the saved chapter for author review",
    group: "Writing",
  },
  {
    name: "extract",
    aliases: ["设定", "提取", "保存"],
    label: "Extract & stage settings",
    description: "Prepare structured changes for confirmation",
    group: "Writing",
    intent: "commit_extract",
    prompt: "Prepare structured changes from this discussion for review.",
  },
  {
    name: "wiki",
    label: "Track this chapter's characters and clues",
    description: "Compile source evidence for review",
    group: "Writing",
  },
  {
    name: "search",
    aliases: ["检索", "搜索", "联网"],
    label: "Web search",
    description: "Choose an available service and request research",
    group: "Tools",
  },
  {
    name: "skill",
    aliases: ["技能", "技法"],
    label: "Skills",
    description: "Attach a skill by name or choose from the catalog",
    group: "Tools",
  },
];

/** Parse only a command at the beginning of the submitted input. */
export function parseChatCommand(text: string) {
  const match = /^\/([^\s/]+)(?:\s+([\s\S]*))?$/u.exec(text.trim());
  if (!match) return null;
  const token = match[1].toLocaleLowerCase();
  const definition = chatCommands.find(
    (command) => command.name === token || command.aliases?.includes(token),
  );
  return { name: definition?.name || token, args: (match[2] || "").trim() };
}

/** Match command names and translated descriptions using the same registry as dispatch. */
export function commandSuggestions(text: string) {
  if (!/^\/[^\s]*$/.test(text)) return [];
  const query = text.slice(1).toLocaleLowerCase();
  return chatCommands.filter((command) =>
    `${command.name} ${(command.aliases || []).join(" ")} ${t(command.label)} ${t(command.description)}`
      .toLocaleLowerCase()
      .includes(query),
  );
}

/** Select a command without discarding the author's unfinished request or existing arguments. */
export function completeChatCommand(text: string, name: string) {
  const parsed = parseChatCommand(text);
  const body = parsed ? parsed.args : text.startsWith("/") ? "" : text;
  return `/${name} ${body}`;
}

/** Default explicit creation commands to saving while ordinary discussion keeps reviewable candidates. */
export function taskCommitPolicy(action: string, intent: string) {
  if (["writing_recompose", "writing_trial", "writing_revision"].includes(intent))
    return "review";
  return ["continue", "rewrite", "outline", "architecture", "memory_update"].includes(action) ||
    ["outline_breakdown", "commit_extract", "write_from_outline"].includes(intent)
    ? "auto"
    : "review";
}
