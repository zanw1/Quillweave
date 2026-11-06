import { Select } from "../../components/Select";
import { t } from "../../lib/i18n";
import { useEffect, useState } from "react";
import { api, message } from "../../lib/api";
import { Button, ErrorBox } from "../../components/ui";
import type {
  ModelProfile,
  ModelSettings,
  ModelSettingsResponse,
  Project,
} from "../../domain/types";

/** Keep global routing in its owning module so the main settings editor stays below the file-size limit. */
export function GlobalModelRoutes({
  settings,
  onChange,
}: {
  settings: ModelSettings;
  onChange: (settings: ModelSettings) => void;
}) {
  return (
    <section className="model-routing-card">
      <div className="settings-card-heading">
        <div>
          <h2>{t("Global default and task routing")}</h2>
          <p>
            {t(
              "Unassigned tasks use the default profile. Each project can override task routing in Story planning.",
            )}
          </p>
        </div>
      </div>
      <label>
        {t("Default model profile")}
        <Select
          value={settings.default_profile}
          onChange={(event) =>
            onChange({ ...settings, default_profile: event.target.value })
          }
        >
          <option value="">{t("Unassigned (offline writing)")}</option>
          {settings.default_profile &&
            !settings.profiles.some(
              (profile) =>
                profile.id === settings.default_profile && profile.enabled,
            ) && (
              <option value={settings.default_profile}>
                {t("Current default profile unavailable")}
              </option>
            )}
          {settings.profiles
            .filter((profile) => profile.enabled)
            .map((profile) => (
              <option key={profile.id} value={profile.id}>
                {profile.name} ·{" "}
                {profile.config.model || t("Model ID required")}
              </option>
            ))}
        </Select>
      </label>
      <div className="settings-divider" />
      <ModelAssignments
        settings={settings}
        routes={settings.routes}
        inheritLabel={t("Use default model")}
        onChange={(routes) => onChange({ ...settings, routes })}
      />
      <p className="field-hint">
        {t(
          "Wiki extraction and memory compilation can use different models. Assign a usable profile to each task.",
        )}
      </p>
    </section>
  );
}

export const modelTasks: Record<string, string> = {
  ask: "Discussion",
  continue: "Chapter continuation",
  rewrite: "Chapter rewrite",
  architecture: "Story planning",
  outline: "Chapter outline",
  review: "Review consistency",
  wiki_extract: "Wiki evidence extraction",
  wiki_compile: "Wiki memory compilation",
};

/** Display model precedence consistently with the Go selector. */
export function selectedProfile(
  settings: ModelSettings,
  project: Project,
  task: string,
): ModelProfile | undefined {
  task =
    (
      {
        agent: "ask",
        naming: "architecture",
        research: "ask",
        memory_update: "wiki_compile",
      } as Record<string, string>
    )[task] || task;
  const id =
    project.model_overrides[task] ||
    settings.routes[task] ||
    settings.default_profile;
  return settings.profiles.find(
    (profile) => profile.id === id && profile.enabled,
  );
}

/** Keep invalid references visible in shared task selectors. */
export function ModelAssignments({
  settings,
  routes,
  onChange,
  inheritLabel,
}: {
  settings: ModelSettings;
  routes: Record<string, string>;
  onChange: (routes: Record<string, string>) => void;
  inheritLabel: string;
}) {
  const available = settings.profiles.filter((profile) => profile.enabled);
  return (
    <div className="model-assignments">
      {Object.entries(modelTasks).map(([task, label]) => (
        <label key={task}>
          {t(label)}
          <Select
            aria-label={t(label)}
            value={routes[task] || ""}
            onChange={(event) =>
              onChange({ ...routes, [task]: event.target.value })
            }
          >
            <option value="">{inheritLabel}</option>
            {routes[task] && !available.some((p) => p.id === routes[task]) && (
              <option value={routes[task]}>
                {t("Unavailable profile (select another)")}
              </option>
            )}
            {available.map((profile) => (
              <option key={profile.id} value={profile.id}>
                {profile.name} · {profile.config.model}
              </option>
            ))}
          </Select>
        </label>
      ))}
    </div>
  );
}

/** Save project-specific task routing. */
export function ProjectModels({
  project,
  onProject,
}: {
  project: Project;
  onProject: (project: Project) => void;
}) {
  const [settings, setSettings] = useState<ModelSettings | null>(null);
  const [routes, setRoutes] = useState(project.model_overrides);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  useEffect(() => {
    let active = true;
    api<ModelSettingsResponse>("get_model_settings")
      .then((result) => {
        if (active) setSettings(result.settings);
      })
      .catch((e) => {
        if (active) setError(message(e));
      });
    return () => {
      active = false;
    };
  }, []);
  /** Retain selected routes after a backend revision conflict. */
  async function save() {
    setBusy(true);
    setError("");
    try {
      onProject(
        await api<Project>("set_project_models", {
          project_id: project.id,
          revision: project.revision,
          overrides: routes,
        }),
      );
      setSaved(true);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="project-models">
      <div className="label-row">
        <h3>{t("Models for this project")}</h3>
        <Button disabled={busy || !settings} onClick={save}>
          {saved ? t("Selection saved") : t("Save project routing")}
        </Button>
      </div>
      <p className="muted">
        {t(
          "Inherit global task routing, or choose profiles for this project's writing, review, and Wiki work.",
        )}
      </p>
      {error && <ErrorBox>{error}</ErrorBox>}
      {settings && (
        <ModelAssignments
          settings={settings}
          routes={routes}
          inheritLabel={t("Inherit global routing")}
          onChange={(value) => {
            setRoutes(value);
            setSaved(false);
          }}
        />
      )}
    </section>
  );
}
