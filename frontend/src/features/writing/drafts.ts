import type { Chapter } from "../../domain/types";

export type ChapterDraft = Pick<
  Chapter,
  "title" | "body" | "synopsis" | "status" | "target_words"
> & { base_version?: string };

const sessionDrafts = new Map<string, ChapterDraft>();
let closeProtection = false;

/** Protect every retained draft, including those whose editor is no longer mounted. */
function protectSessionDrafts() {
  if (closeProtection) return;
  window.addEventListener("beforeunload", (event) => {
    if (!sessionDrafts.size) return;
    event.preventDefault();
    event.returnValue = "";
  });
  closeProtection = true;
}

/** Keep the latest input available across chapter, page and project navigation. */
export function rememberChapterDraft(key: string, draft: ChapterDraft) {
  sessionDrafts.set(key, draft);
  protectSessionDrafts();
}

/** Prefer the session copy when browser storage could not accept a newer draft. */
export function retainedChapterDraft(key: string): ChapterDraft | undefined {
  return sessionDrafts.get(key);
}

/** Persist a draft when possible while retaining a recoverable session copy on failure. */
export function cacheChapterDraft(key: string, draft: ChapterDraft): boolean {
  rememberChapterDraft(key, draft);
  try {
    localStorage.setItem(key, JSON.stringify(draft));
    return true;
  } catch {
    return false;
  }
}

/** Remove a saved or deliberately deleted draft without letting storage errors break navigation. */
export function forgetChapterDraft(key: string) {
  sessionDrafts.delete(key);
  try {
    localStorage.removeItem(key);
  } catch {
    /* The saved project remains authoritative when browser storage is unavailable. */
  }
}

/** Release session drafts after the author deletes the entire owning project. */
export function clearProjectDrafts(projectId: string) {
  const prefix = `quillweave-draft-${projectId}-`;
  for (const key of sessionDrafts.keys()) {
    if (key.startsWith(prefix)) forgetChapterDraft(key);
  }
}
