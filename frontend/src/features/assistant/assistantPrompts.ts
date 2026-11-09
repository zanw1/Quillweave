export const conversationActions = [
  "ask",
  "architecture",
  "outline",
  "agent",
  "naming",
  "research",
];

export const outlineInstruction =
  "Create a complete chapter-by-chapter outline from our discussion and the current story bible. Include the premise, character arcs, act progression, and each chapter's viewpoint, goal, obstacle, choice, consequence, and clues. Identify assumptions and open questions.";

export const humanizeInstruction =
  "Polish the current chapter to reduce formulaic AI phrasing. Preserve its original language, plot, names, relationships, viewpoint, chronology, clues, and approximate length; keep the author's style and each character's voice. Trim redundant explanations and stock phrasing, and vary mechanical sentence patterns. Keep intentional imagery, repetition, and punctuation. Make no new plot developments. Return only the complete polished chapter. For this editing task, preserve the source length instead of expanding to the project's chapter-length target.";

export const explorationSuggestions = [
  {
    label: "Help me find an idea",
    instruction:
      "I don't have a story idea yet. Help me discover what I would enjoy writing. Start with one or two easy questions about stories, emotions, or situations I like. Don't ask me to decide a genre, title, length, viewpoint, or complete plot yet.",
  },
  {
    label: "I have a small spark",
    instruction:
      "I have a vague image, character, mood, or situation in mind. Ask me about that small spark and help me explore it, one question at a time, without requiring a finished story setup.",
  },
  {
    label: "Explore a few possibilities",
    instruction:
      "Offer three brief, different story seeds to react to. Keep them open-ended, explain what might be interesting about each, and let me choose or combine them before developing a plot or outline.",
  },
];
