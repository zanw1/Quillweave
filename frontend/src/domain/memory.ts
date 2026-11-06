import type { StoryMemory } from "./studio";

/** Index committed replacement boundaries without treating a rejected update as an undo. */
export function memorySuccessors(memories: StoryMemory[]) {
  const result = new Map<string, number>();
  for (const memory of memories) {
    if (!memory.supersedes || memory.status === "proposed") continue;
    result.set(
      memory.supersedes,
      Math.min(result.get(memory.supersedes) ?? Infinity, memory.chapter),
    );
  }
  return result;
}

/** Apply the same narrative interval rules as the backend when browsing a historical chapter. */
export function memoryVisibleAt(
  memory: StoryMemory,
  chapter: number,
  successors: Map<string, number>,
) {
  if (
    memory.chapter > chapter ||
    (memory.through_chapter > 0 && memory.through_chapter < chapter)
  )
    return false;
  if (memory.status === "confirmed") return true;
  if (memory.status !== "superseded") return false;
  const replacement = successors.get(memory.id);
  return replacement === undefined
    ? memory.through_chapter > 0
    : chapter < replacement;
}
