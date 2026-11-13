import { useEffect, useRef, useState } from "react";
import { api, message } from "../../lib/api";
import type {
  ChatModelSelection,
  ChatSkill,
  ModelSettings,
  ModelSettingsResponse,
  Project,
  ProviderCatalog,
} from "../../domain/types";
import type { Conversation } from "../../domain/studio";
import { selectedProfile } from "../settings/models";
import { localDraft, saveLocalDraft } from "./conversations";

interface DraftOptions {
  selection?: ChatModelSelection;
  skills: string[];
  search: string;
  commitPolicy: "task" | "review" | "auto";
}

/** Read per-conversation composer choices while retaining an inherited model for older sessions. */
function readOptions(
  key: string,
  session: Conversation | null,
  settings: ModelSettings | null,
  project: Project,
): DraftOptions {
  let saved: Partial<DraftOptions> = {};
  try {
    const parsed = JSON.parse(localDraft(key, "{}"));
    if (parsed && typeof parsed === "object") saved = parsed;
  } catch {
    /* Ignore unusable local metadata. */
  }
  const fallback = settings
    ? selectedProfile(settings, project, "agent")
    : undefined;
  return {
    selection:
      saved.selection ||
      session?.model_selection ||
      (fallback ? { profile_id: fallback.id, output_mode: "auto" } : undefined),
    skills: Array.isArray(saved.skills)
      ? saved.skills.filter((id) => typeof id === "string")
      : [],
    search: typeof saved.search === "string" ? saved.search : "inherit",
    commitPolicy: saved.commitPolicy === "auto" || saved.commitPolicy === "review" ? saved.commitPolicy : "task",
  };
}

/** Own draft model, skill and search choices separately from already accepted task snapshots. */
export function useChatOptions(
  project: Project,
  session: Conversation | null,
  draftKey: string,
) {
  const [settings, setSettings] = useState<ModelSettings | null>(null);
  const [catalog, setCatalog] = useState<ProviderCatalog | null>(null);
  const [skillsCatalog, setSkillsCatalog] = useState<ChatSkill[]>([]);
  const [error, setError] = useState("");
  const key = `${draftKey}-options`;
  const [state, setState] = useState<{ key: string; options: DraftOptions }>(
    () => ({ key, options: { skills: [], search: "inherit", commitPolicy: "task" } }),
  );

  const projectRef = useRef(project.id);
  projectRef.current = project.id;
  const requestSequence = useRef(0);

  /** Refresh catalogs independently so a failed optional Skill source cannot hide model connections. */
  async function reload() {
    const id = project.id;
    const version = ++requestSequence.current;
    const [models, metadata, available] = await Promise.allSettled([
      api<ModelSettingsResponse>("get_model_settings"),
      api<ProviderCatalog>("provider_presets"),
      api<{ skills: ChatSkill[] }>("list_skills", { project_id: id }),
    ]);
    if (projectRef.current !== id || version !== requestSequence.current)
      return;
    if (models.status === "fulfilled") setSettings(models.value.settings);
    if (metadata.status === "fulfilled") setCatalog(metadata.value);
    if (available.status === "fulfilled")
      setSkillsCatalog(available.value.skills || []);
    const failure = [models, metadata, available].find(
      (result) => result.status === "rejected",
    );
    setError(failure?.status === "rejected" ? message(failure.reason) : "");
  }
  useEffect(() => {
    void reload();
    return () => {
      requestSequence.current++;
    };
  }, [project.id]);
  useEffect(() => {
    setState({ key, options: readOptions(key, session, settings, project) });
  }, [key, settings, session?.model_selection?.profile_id]);
  const options =
    state.key === key
      ? state.options
      : readOptions(key, session, settings, project);

  /** Persist only the choices of the draft that produced this update. */
  function update(patch: Partial<DraftOptions>) {
    const next = { ...readOptions(key, session, settings, project), ...patch };
    setState((current) =>
      current.key === key ? { key, options: next } : current,
    );
    saveLocalDraft(key, JSON.stringify(next));
  }
  return {
    ...options,
    settings,
    catalog,
    skillsCatalog,
    error,
    reload,
    update,
  };
}
