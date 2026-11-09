import { useEffect, useState } from "react";
import { t } from "../../lib/i18n";

export interface SetupDraft {
  key: string;
  label: string;
  text: string;
}

const drafts = new Map<string, SetupDraft>();
const listeners = new Set<() => void>();

/** Publish or clear one unsaved settings form so a whole-setup review can see it. */
export function publishSetupDraft(
  key: string,
  draft: { label: string; text: string } | null,
) {
  if (!draft || !draft.text.trim()) drafts.delete(key);
  else drafts.set(key, { key, ...draft });
  listeners.forEach((listener) => listener());
}

/** Read the forms the author has open but has not saved. */
export function currentSetupDrafts(): SetupDraft[] {
  return [...drafts.values()];
}

/** Stay current while an editor opens or closes. */
export function useSetupDrafts() {
  const [items, setItems] = useState(currentSetupDrafts);
  useEffect(() => {
    const update = () => setItems(currentSetupDrafts());
    listeners.add(update);
    return () => {
      listeners.delete(update);
    };
  }, []);
  return items;
}

/** Instruction for comparing the whole saved setup plus open drafts. */
export function wholeSetupRequest() {
  let text = t(
    "Compare the entire saved story setup, including wiki entries, clues, plans, story goals and the map, with any unsaved drafts below. Stage every addition and change with propose_changes for my confirmation. Do not treat this as only the page I have open, and do not claim anything is saved before I confirm.",
  );
  const open = currentSetupDrafts();
  if (open.length)
    text +=
      "\n\n" +
      t("Unsaved drafts") +
      "\n" +
      open.map((item) => `### ${item.label}\n${item.text}`).join("\n\n");
  return text;
}
