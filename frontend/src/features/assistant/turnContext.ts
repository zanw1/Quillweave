import type { Chapter, Project, Source } from "../../domain/types";
import type { WorkspaceContext } from "../../domain/studio";

export interface TurnChip {
  id: string;
  label: string;
}

/** Records the author removed from this turn. They return on the next send. */
export interface TurnOmissions {
  selectedText: boolean;
  chapter: boolean;
  source: boolean;
  entities: string[];
  plans: string[];
  memories: string[];
}

/** Start a turn with every available record included. */
export function emptyOmissions(): TurnOmissions {
  return {
    selectedText: false,
    chapter: false,
    source: false,
    entities: [],
    plans: [],
    memories: [],
  };
}

/** Same order and limits the story snapshot uses when it is sent. */
export function turnChips(
  project: Project,
  context: WorkspaceContext | undefined,
  chapter: Chapter | undefined,
  source: Source | undefined,
  omissions: TurnOmissions,
): TurnChip[] {
  const chips: TurnChip[] = [];
  const selected = context?.selected_text?.trim() || "";
  if (selected && !omissions.selectedText)
    chips.push({ id: "selection", label: selected });
  if (chapter && !omissions.chapter)
    chips.push({
      id: `chapter:${chapter.id}`,
      label: `${chapter.number} ${chapter.title}`,
    });
  if (source && context?.active_source_id === source.id && !omissions.source)
    chips.push({ id: `source:${source.id}`, label: source.title });
  const place = (project.studio.locations || []).find(
    (item) => item.id === context?.active_entity_id,
  );
  if (place && !omissions.entities.includes(place.id))
    chips.push({ id: `entity:${place.id}`, label: place.name });
  for (const entity of [...project.entities]
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
    .slice(0, 24)) {
    if (!omissions.entities.includes(entity.id))
      chips.push({ id: `entity:${entity.id}`, label: entity.name });
  }
  for (const plan of project.studio.plans
    .filter((item) => item.status === "confirmed")
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
    .slice(0, 16)) {
    if (!omissions.plans.includes(plan.id))
      chips.push({ id: `plan:${plan.id}`, label: plan.title });
  }
  for (const memory of project.studio.memories
    .filter((item) => item.status === "confirmed")
    .sort((a, b) => {
      const rank = (kind: string) => (kind === "rule" ? 0 : 1);
      return rank(a.kind) - rank(b.kind) || (a.id < b.id ? -1 : 1);
    })
    .slice(0, 24)) {
    if (!omissions.memories.includes(memory.id))
      chips.push({
        id: `memory:${memory.id}`,
        label: memory.title || memory.content,
      });
  }
  return chips;
}

/** Drop one chip from the records carried on this turn. */
export function omitChip(current: TurnOmissions, id: string): TurnOmissions {
  if (id === "selection") return { ...current, selectedText: true };
  if (id.startsWith("chapter:")) return { ...current, chapter: true };
  if (id.startsWith("source:")) return { ...current, source: true };
  if (id.startsWith("entity:"))
    return { ...current, entities: [...current.entities, id.slice(7)] };
  if (id.startsWith("plan:"))
    return { ...current, plans: [...current.plans, id.slice(5)] };
  if (id.startsWith("memory:"))
    return { ...current, memories: [...current.memories, id.slice(7)] };
  return current;
}

/** Fields merged into the workspace context sent with this turn. */
export function omissionFields(
  omissions: TurnOmissions,
): Partial<WorkspaceContext> {
  return {
    omit_selected_text: omissions.selectedText || undefined,
    omit_chapter: omissions.chapter || undefined,
    omit_source: omissions.source || undefined,
    omit_entity_ids: omissions.entities.length ? omissions.entities : undefined,
    omit_plan_ids: omissions.plans.length ? omissions.plans : undefined,
    omit_memory_ids: omissions.memories.length ? omissions.memories : undefined,
  };
}
