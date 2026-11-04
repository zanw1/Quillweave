export interface WritingMethod {
  id: string;
  stage: string;
  title: string;
  subtitle: string;
  description: string;
  questions: string[];
  scope: string;
  author: string;
  url: string;
  prompt: string;
}
/** Provide original summaries and questions with links to their sources. */
export const methods: WritingMethod[] = [
  {
    id: "snowflake",
    stage: "Planning",
    title: "Start with one sentence",
    subtitle: "Snowflake method · Expand gradually",
    description:
      "Identify who wants what and what stands in the way. Expand the characters and plot gradually, revisiting earlier choices as the story develops.",
    questions: [
      "What situation does the protagonist most want to change?",
      "Why is the obstacle difficult to bypass?",
      "Does this sentence capture the story's distinctive conflict?",
    ],
    scope:
      "Useful for advance planning or for organizing an exploratory first draft.",
    author: "Randy Ingermanson",
    url: "https://www.advancedfictionwriting.com/articles/snowflake-method/",
    prompt:
      "Summarize the central conflict in one sentence, then propose three directions for the story. Leave the choice of ending to the author.",
  },
  {
    id: "scene",
    stage: "Scenes",
    title: "Give each scene a change",
    subtitle: "Goal → Obstacle → Consequence",
    description:
      "A character acts toward a concrete goal. An obstacle forces a choice, and its result changes the next step. The change can be small or quiet.",
    questions: [
      "What does the viewpoint character want right now?",
      "What obstructs that goal?",
      "What has changed in relationships, information, or circumstances by the end?",
    ],
    scope:
      "Useful for examining scene progression and causality. Reflective and everyday passages can use gentler change.",
    author: "K. M. Weiland",
    url: "https://www.helpingwritersbecomeauthors.com/structuring-your-storys-scenes-pt-2/amp/",
    prompt:
      "Review the chapter's goals, obstacles, choices, and consequences. Cite scenes that produce little change.",
  },
  {
    id: "arc",
    stage: "Characters",
    title: "What lies behind a character's want",
    subtitle: "Character arc · Want / Need",
    description:
      "Track the external result a character pursues and the beliefs or habits they need to confront. Examine how key choices change the relationship between them.",
    questions: [
      "What does the character say they want?",
      "What belief does their behavior reveal?",
      "Which choice challenges or reinforces that belief?",
    ],
    scope:
      "Useful for change arcs and for comparing flat or negative arcs. Different characters can follow different kinds of development.",
    author: "K. M. Weiland",
    url: "https://www.helpingwritersbecomeauthors.com/character-arcs-3/",
    prompt:
      "Analyze the character's external want, possible inner need, key choices, and consequences. Distinguish explicit facts from inference and cite the evidence.",
  },
  {
    id: "progress",
    stage: "Structure",
    title: "Develop the reader's expectations",
    subtitle: "Promises and progress",
    description:
      "An opening mystery, relationship, or goal creates expectations. Later chapters can show meaningful movement in those questions.",
    questions: [
      "What does the opening lead readers to expect will be resolved?",
      "Which expectation does this chapter develop?",
      "Does an interim answer raise a new question?",
    ],
    scope:
      "Useful for novel pacing and serial planning. Adapt the rhythm to the genre.",
    author: "Writing Excuses 10.31",
    url: "https://writingexcuses.com/writing-excuses-10-31-how-do-i-control-the-readers-sense-of-progress/",
    prompt:
      "List the main promises made to readers, the progress toward them, and unresolved questions. Identify the relevant chapters.",
  },
  {
    id: "world",
    stage: "Worldbuilding",
    title: "Rules, limits, and costs",
    subtitle: "World consistency",
    description:
      "When an unusual ability resolves an important conflict, readers need to understand its limits. Track constraints and costs before relying on the ability as a solution.",
    questions: [
      "Did readers know about this ability beforehand?",
      "Do its limits and costs affect the choice?",
      "Does the solution follow from established rules?",
    ],
    scope:
      "Drawn from Sanderson's work on magic systems. Mysterious or soft systems can retain unknowns while managing reader expectations.",
    author: "Brandon Sanderson",
    url: "https://faq.brandonsanderson.com/knowledge-base/what-are-sandersons-laws-of-magic/",
    prompt:
      "Check the chapter's abilities and world rules. Cite solutions with insufficient setup and limits or costs that have been overlooked.",
  },
  {
    id: "wiki",
    stage: "Revision",
    title: "Build memory from traceable evidence",
    subtitle: "LLM Wiki · Sources and derived knowledge",
    description:
      "Keep the original chapters and organize analysis into linked entries. Recheck derived knowledge when the source changes. Use it in future reasoning after author approval.",
    questions: [
      "Which source passage supports each claim?",
      "Is this a fact, an interpretation, or an author plan?",
      "Which entries need another review after the source changes?",
    ],
    scope:
      "This method organizes knowledge and its evidence. Review the interpretation as well as the matching quote.",
    author: "Andrej Karpathy · LLM Wiki",
    url: "https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f",
    prompt:
      "Check for conflicts between the current chapter and the confirmed Wiki. Cite source evidence and distinguish facts, interpretations, and author plans.",
  },
];
