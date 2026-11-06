import { desktopCall, isDesktop } from "./api";
import { getLanguage, t } from "./i18n";
import { searchTerminal, type SearchRun } from "../domain/search";

interface Envelope<T> {
  ok: boolean;
  result?: T;
  error?: string;
  error_code?: string;
  details?: Record<string, unknown>;
}

export class SearchCommandError extends Error {
  code: string;
  details?: Record<string, unknown>;
  /** Retain a stable code for conflict handling while displaying the translated human message. */
  constructor(envelope: Envelope<unknown>) {
    super(t(envelope.error || "Search operation failed"));
    this.code = envelope.error_code || "SEARCH_ERROR";
    this.details = envelope.details;
  }
}

/** Call the real shared search boundary with matching native/HTTP envelopes and optional local polling cancellation. */
export async function searchApi<T>(
  command: string,
  args: object = {},
  signal?: AbortSignal,
): Promise<T> {
  const payload = { ...args, ui_language: getLanguage() };
  let envelope: Envelope<T>;
  if (isDesktop())
    envelope = await desktopCall<Envelope<T>>(
      "DispatchEnvelope",
      command,
      JSON.stringify(payload),
    );
  else {
    const response = await fetch("/api/command", {
      method: "POST",
      signal,
      headers: {
        "Content-Type": "application/json",
        "X-Quillweave-Client": "1",
      },
      body: JSON.stringify({ command, args: payload }),
    });
    envelope = await response.json().catch(() => {
      throw new Error(t("The local search service is unavailable"));
    });
  }
  if (!envelope.ok) throw new SearchCommandError(envelope);
  return envelope.result as T;
}

/** Notify sibling settings sections after an explicit successful save without sharing drafts or credentials. */
export function notifySearchChanged() {
  window.dispatchEvent(new Event("quillweave-search-changed"));
}

/** Wait for the persisted outcome; a successful transport response with failed business status is still a failure. */
export async function followSearch(
  run: SearchRun,
  onUpdate: (run: SearchRun) => void,
  signal?: AbortSignal,
): Promise<SearchRun> {
  let current = run;
  while (!signal?.aborted) {
    onUpdate(current);
    if (searchTerminal(current.status)) return current;
    await new Promise<void>((resolve, reject) => {
      const timer = window.setTimeout(() => {
        signal?.removeEventListener("abort", abort);
        resolve();
      }, 250);
      /** Remove only this pending poll timer when its view unmounts. */
      function abort() {
        window.clearTimeout(timer);
        reject(new DOMException("Aborted", "AbortError"));
      }
      signal?.addEventListener("abort", abort, { once: true });
    });
    current = await searchApi<SearchRun>(
      "get_search_run",
      { run_id: run.id, project_id: run.project_id || "" },
      signal,
    );
  }
  throw new DOMException("Aborted", "AbortError");
}
