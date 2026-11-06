import { useCallback, useEffect, useRef, useState } from "react";
import { message } from "../../lib/api";
import { getLanguage } from "../../lib/i18n";
import { followSearch, searchApi, SearchCommandError } from "../../lib/search";
import type { SearchRun, SearchSelection } from "../../domain/search";

export interface SearchIntent {
  purpose: "test" | "research";
  operation: "search" | "fetch";
  query: string;
  url?: string;
  project_id?: string;
  session_id?: string;
  model_profile_id?: string;
  selection?: SearchSelection;
}

/** Keep durable research identity across lost acknowledgements, local polling errors and view navigation. */
export function useSearchRun(projectId = "") {
  const [run, setRun] = useState<SearchRun | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const controller = useRef<AbortController | null>(null);
  const pending = useRef<{
    key: string;
    requestId: string;
    runId?: string;
  } | null>(null);
  const storageKey = `quillweave-search-run:${projectId || "global"}`;

  /** Persist only a run ID, never a credential, supplier response or complete request. */
  const remember = useCallback(
    (id: string) => {
      try {
        localStorage.setItem(storageKey, id);
      } catch {
        /* Storage is optional. */
      }
    },
    [storageKey],
  );
  useEffect(() => {
    controller.current?.abort();
    const next = new AbortController();
    controller.current = next;
    pending.current = null;
    setRun(null);
    setBusy(false);
    setError("");
    let id = "";
    try {
      id = localStorage.getItem(storageKey) || "";
    } catch {
      /* No persistent browser storage. */
    }
    if (id) {
      setBusy(true);
      void searchApi<SearchRun>(
        "get_search_run",
        {
          ...(id.startsWith("request:")
            ? { request_id: id.slice(8) }
            : { run_id: id }),
          project_id: projectId,
        },
        next.signal,
      )
        .then(async (current) => {
          remember(current.id);
          if (next.signal.aborted) return;
          setRun(current);
          if (["queued", "running"].includes(current.status)) {
            setBusy(true);
            await followSearch(
              current,
              (value) => {
                if (!next.signal.aborted) setRun(value);
              },
              next.signal,
            );
          }
        })
        .catch((error) => {
          if (!next.signal.aborted) {
            if (
              error instanceof SearchCommandError &&
              error.code === "SEARCH_RUN_NOT_FOUND"
            ) {
              try {
                localStorage.removeItem(storageKey);
              } catch {
                /* Optional storage. */
              }
            } else setError(message(error));
          }
        })
        .finally(() => {
          if (!next.signal.aborted) setBusy(false);
        });
    }
    return () => next.abort();
  }, [projectId, storageKey]);

  /** Reuse an unacknowledged request or a known active run instead of buying another provider request on retry. */
  async function start(intent: SearchIntent): Promise<SearchRun | undefined> {
    if (busy) return;
    controller.current?.abort();
    const next = new AbortController();
    controller.current = next;
    setBusy(true);
    setError("");
    const key = JSON.stringify({ ...intent, ui_language: getLanguage() });
    if (!pending.current || pending.current.key !== key)
      pending.current = { key, requestId: crypto.randomUUID() };
    const accepted = pending.current;
    remember(`request:${accepted.requestId}`);
    try {
      const current = accepted.runId
        ? await searchApi<SearchRun>(
            "get_search_run",
            { run_id: accepted.runId, project_id: projectId },
            next.signal,
          )
        : await searchApi<SearchRun>("start_search_run", {
            ...intent,
            request_id: accepted.requestId,
          });
      accepted.runId = current.id;
      remember(current.id);
      if (next.signal.aborted) return;
      setRun(current);
      const finished = await followSearch(
        current,
        (value) => {
          if (!next.signal.aborted) setRun(value);
        },
        next.signal,
      );
      if (pending.current === accepted) pending.current = null;
      return finished;
    } catch (error) {
      if (!next.signal.aborted) setError(message(error));
      return undefined;
    } finally {
      if (controller.current === next && !next.signal.aborted) setBusy(false);
    }
  }
  /** Stop the accepted server run; unmounting only stops polling and does not pretend to cancel remote work. */
  async function cancel() {
    if (!run) return;
    try {
      const current = await searchApi<SearchRun>("cancel_search_run", {
        run_id: run.id,
        project_id: projectId,
      });
      setRun(current);
      setError("");
    } catch (error) {
      setError(message(error));
    }
  }
  /** An explicit configuration edit begins a new user intent; stale results remain visibly historical until cleared. */
  function reset() {
    pending.current = null;
    setError("");
  }
  return { run, setRun, busy, error, setError, start, cancel, reset };
}
