import { useCallback, useEffect, useState } from "react";
import { message } from "../../../lib/api";
import { notifySearchChanged, searchApi } from "../../../lib/search";
import type {
  SearchCatalog,
  SearchProfile,
  SearchSettingsView,
} from "../../../domain/search";
import type { SecretUpdate } from "../../../domain/types";

/** Load metadata only, keeping production service calls behind explicit user search/test actions. */
export function useSearchSettings() {
  const [view, setView] = useState<SearchSettingsView | null>(null);
  const [catalog, setCatalog] = useState<SearchCatalog | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const reload = useCallback(async () => {
    setLoading(true);
    try {
      const [next, capabilities] = await Promise.all([
        searchApi<SearchSettingsView>("get_search_settings"),
        searchApi<SearchCatalog>("search_capabilities"),
      ]);
      setView(next);
      setCatalog(capabilities);
      setError("");
      return next;
    } catch (error) {
      setError(message(error));
      throw error;
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    let active = true;
    void reload().catch(() => {});
    const changed = () => {
      if (active) void reload().catch(() => {});
    };
    window.addEventListener("quillweave-search-changed", changed);
    return () => {
      active = false;
      window.removeEventListener("quillweave-search-changed", changed);
    };
  }, [reload]);

  /** Commit one CAS-protected operation and retain all editor drafts if the backend rejects it. */
  async function mutate(operation: string, values: object) {
    if (!view) throw new Error("Search settings are still loading");
    const next = await searchApi<SearchSettingsView>("mutate_search_settings", {
      operation,
      expected_revision: view.revision,
      model_revision: view.model_revision,
      ...values,
    });
    setView(next);
    notifySearchChanged();
    return next;
  }
  return { view, catalog, error, setError, loading, reload, mutate, setView };
}

/** Serialize credentials only in the explicit update map, never into the profile's public metadata. */
export function searchProfilePayload(
  profile: SearchProfile,
  update?: SecretUpdate,
) {
  return { profile, key_updates: update ? { [profile.id]: update } : {} };
}

/** Compare API origins/prefixes for custom connections without classifying them from their model name. */
export function matchingSearchBase(first: string, second: string): boolean {
  try {
    const normalize = (input: string) => {
      const url = new URL(input);
      return (
        url.origin +
        url.pathname
          .replace(
            /\/(chat\/completions|responses|tools\/(search|search_pro|fetch))\/?$/,
            "",
          )
          .replace(/\/v1\/?$/, "")
          .replace(/\/+$/, "")
      );
    };
    return normalize(first) === normalize(second);
  } catch {
    return false;
  }
}

/** Treat unproven auto/native routes conservatively rather than promising a hard per-call limit. */
export function knownSearchLimit(profile: SearchProfile): boolean {
  return (
    profile.mode !== "native" &&
    (profile.family !== "openrouter" ||
      ["exa", "parallel", "perplexity", "firecrawl"].includes(
        profile.engine || "",
      ))
  );
}
