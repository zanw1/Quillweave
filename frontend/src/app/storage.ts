/** Remove only the deleted story's local drafts, selection, and task or graph caches. */
export function clearProjectStorage(id: string) {
  for (const kind of ["localStorage", "sessionStorage"] as const) {
    try {
      const storage = window[kind];
      for (const key of Object.keys(storage)) {
        if (
          key === `quillweave-plan-${id}` ||
          key.startsWith(`quillweave-chat-draft-${id}-`) ||
          key.startsWith(`quillweave-session-${id}-`) ||
          key.startsWith(`quillweave-draft-${id}-`) ||
          key === `quillweave-job-${id}` ||
          key === `quillweave-graph-v4-${id}` ||
          (key === "quillweave-project" && storage.getItem(key) === id)
        )
          storage.removeItem(key);
      }
    } catch {
      /* A blocked browser cache must not turn a completed deletion into an error. */
    }
  }
}
