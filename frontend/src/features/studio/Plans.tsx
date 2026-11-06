import { useState } from "react";
import { t } from "../../lib/i18n";
import { api, message } from "../../lib/api";
import {
  Badge,
  Button,
  Empty,
  ErrorBox,
  Modal,
  RequiredLabel,
} from "../../components/ui";
import { Select } from "../../components/Select";
import type { Project } from "../../domain/types";
import type { StoryPlan } from "../../domain/studio";

export const planLevels: Record<string, string> = {
  book: "Book",
  volume: "Volume",
  unit: "Unit",
  chapter: "Chapter",
  scene: "Scene",
};

/** Create an editable plan with every persisted field explicitly initialized. */
export function blankPlan(parent?: StoryPlan): StoryPlan {
  const levels = Object.keys(planLevels);
  return {
    id: "",
    parent_id: parent?.id || "",
    level: parent
      ? levels[Math.min(4, levels.indexOf(parent.level) + 1)]
      : "volume",
    title: "",
    theme: "",
    goal: "",
    conflict: "",
    choice: "",
    consequence: "",
    pacing: "",
    pov: "",
    characters: [],
    clues: [],
    start_chapter: parent?.start_chapter || 1,
    end_chapter: parent?.end_chapter || 1,
    target_characters: 0,
    status: "confirmed",
  };
}

/** Order the hierarchy by narrative position while displaying orphan-free depth indentation. */
function orderedPlans(
  plans: StoryPlan[],
  parent = "",
  depth = 0,
): { plan: StoryPlan; depth: number }[] {
  return plans
    .filter((plan) => plan.parent_id === parent)
    .sort((a, b) => a.start_chapter - b.start_chapter)
    .flatMap((plan) => [
      { plan, depth },
      ...orderedPlans(plans, plan.id, depth + 1),
    ]);
}

/** Edit hierarchical planning without flattening author choices into an opaque outline string. */
export function Plans({
  project,
  onProject,
  onAsk,
}: {
  project: Project;
  onProject: (project: Project) => void;
  onAsk: (text: string, action?: string) => void;
}) {
  const [editing, setEditing] = useState<StoryPlan | null>(null);
  const [base, setBase] = useState(project.revision);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  /** Capture the revision belonging to the draft so stale edits cannot overwrite newer plans. */
  function open(plan: StoryPlan) {
    setEditing(plan);
    setBase(project.revision);
    setError("");
  }

  /** Save a validated plan or materialize confirmed chapter goals through real domain commands. */
  async function change(
    command: string,
    args: Record<string, unknown>,
    revision = project.revision,
  ) {
    setBusy(true);
    setError("");
    try {
      const next = await api<Project>(command, {
        project_id: project.id,
        revision,
        ...args,
      });
      onProject(next);
      setEditing(null);
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="studio-section">
      <div className="studio-section-heading">
        <div>
          <h2>{t("Plans")}</h2>
          <p className="muted">
            {t("Book")} → {t("Volume")} → {t("Unit")} → {t("Chapter")} →{" "}
            {t("Scene")}
          </p>
        </div>
        <Button primary onClick={() => open(blankPlan())}>
          {t("Add plan")}
        </Button>
      </div>
      <div className="studio-actions">
        <Button onClick={() => onAsk(t("Ask for a volume plan"), "outline")}>
          {t("Ask for a volume plan")}
        </Button>
        <Button onClick={() => onAsk(t("Refine upcoming chapters"), "outline")}>
          {t("Refine upcoming chapters")}
        </Button>
        <Button
          disabled={
            busy ||
            !project.studio.plans.some(
              (plan) => plan.level === "chapter" && plan.status === "confirmed",
            )
          }
          onClick={() => void change("create_planned_chapters", {})}
        >
          {t("Create chapter drafts")}
        </Button>
      </div>
      {error && !editing && <ErrorBox>{error}</ErrorBox>}
      {!project.studio.plans.length && (
        <Empty
          title={t("No plans yet")}
          detail={t(
            "Start with an idea. Decide when you are ready to outline.",
          )}
        />
      )}
      <div className="plan-list">
        {orderedPlans(project.studio.plans).map(({ plan, depth }) => (
          <article
            className="plan-card"
            key={plan.id}
            style={{ marginLeft: Math.min(depth, 4) * 18 }}
          >
            <header>
              <Badge tone="blue">{t(planLevels[plan.level])}</Badge>
              <h3>{plan.title}</h3>
              <Badge>
                {t(plan.status === "confirmed" ? "Confirmed" : "Proposed")}
              </Badge>
            </header>
            <p>{plan.goal}</p>
            <dl>
              <dt>{t("Conflict")}</dt>
              <dd>{plan.conflict || "—"}</dd>
              <dt>{t("Consequence")}</dt>
              <dd>{plan.consequence || "—"}</dd>
            </dl>
            <p className="muted">
              {t("Chapter")} {plan.start_chapter}–{plan.end_chapter} ·{" "}
              {plan.target_characters.toLocaleString()} {t("characters")} ·{" "}
              {plan.pacing}
            </p>
            <div className="studio-actions">
              <Button onClick={() => open(plan)}>{t("Edit")}</Button>
              <Button onClick={() => open(blankPlan(plan))}>
                {t("Add plan")}
              </Button>
              <Button
                disabled={busy}
                onClick={() => {
                  if (window.confirm(`${t("Delete")}: ${plan.title}?`))
                    void change("delete_plan", { plan_id: plan.id });
                }}
              >
                {t("Delete")}
              </Button>
            </div>
          </article>
        ))}
      </div>
      {editing && (
        <Modal
          title={t(editing.id ? "Edit plan" : "Add plan")}
          onClose={() => setEditing(null)}
          wide
        >
          <form
            className="modal-content studio-form"
            onSubmit={(event) => {
              event.preventDefault();
              void change("save_plan", { plan: editing }, base);
            }}
          >
            <div className="form-grid">
              <label>
                {t("Level")}
                <Select
                  value={editing.level}
                  onChange={(event) =>
                    setEditing({ ...editing, level: event.target.value })
                  }
                >
                  {Object.entries(planLevels).map(([key, value]) => (
                    <option value={key} key={key}>
                      {t(value)}
                    </option>
                  ))}
                </Select>
              </label>
              <label>
                {t("Parent plan")}
                <Select
                  value={editing.parent_id}
                  onChange={(event) =>
                    setEditing({ ...editing, parent_id: event.target.value })
                  }
                >
                  <option value="">{t("No parent")}</option>
                  {project.studio.plans
                    .filter((plan) => plan.id !== editing.id)
                    .map((plan) => (
                      <option value={plan.id} key={plan.id}>
                        {plan.title}
                      </option>
                    ))}
                </Select>
              </label>
            </div>
            <label>
              <RequiredLabel>{t("Title")}</RequiredLabel>
              <input
                value={editing.title}
                onChange={(event) =>
                  setEditing({ ...editing, title: event.target.value })
                }
                maxLength={120}
                required
              />
            </label>
            <div className="form-grid">
              {(
                [
                  ["start_chapter", "Start chapter"],
                  ["end_chapter", "End chapter"],
                  ["target_characters", "Target characters"],
                ] as const
              ).map(([key, title]) => (
                <label key={key}>
                  {t(title)}
                  <input
                    type="number"
                    min={0}
                    value={editing[key]}
                    onChange={(event) =>
                      setEditing({
                        ...editing,
                        [key]: Number(event.target.value),
                      })
                    }
                  />
                </label>
              ))}
            </div>
            {(
              [
                ["theme", "Theme"],
                ["goal", "Goal"],
                ["conflict", "Conflict"],
                ["choice", "Choice"],
                ["consequence", "Consequence"],
              ] as const
            ).map(([key, title]) => (
              <label key={key}>
                {t(title)}
                <textarea
                  value={editing[key]}
                  onChange={(event) =>
                    setEditing({ ...editing, [key]: event.target.value })
                  }
                />
              </label>
            ))}
            <div className="form-grid">
              <label>
                {t("Pacing")}
                <input
                  value={editing.pacing}
                  onChange={(event) =>
                    setEditing({ ...editing, pacing: event.target.value })
                  }
                />
              </label>
              <label>
                {t("Viewpoint")}
                <input
                  value={editing.pov}
                  onChange={(event) =>
                    setEditing({ ...editing, pov: event.target.value })
                  }
                />
              </label>
            </div>
            <div className="form-grid">
              <label>
                {t("Characters involved")}
                <textarea
                  value={editing.characters?.join("\n") || ""}
                  onChange={(event) =>
                    setEditing({
                      ...editing,
                      characters: event.target.value
                        .split("\n")
                        .filter(Boolean),
                    })
                  }
                />
              </label>
              <label>
                {t("Clues and payoffs")}
                <textarea
                  value={editing.clues?.join("\n") || ""}
                  onChange={(event) =>
                    setEditing({
                      ...editing,
                      clues: event.target.value.split("\n").filter(Boolean),
                    })
                  }
                />
              </label>
            </div>
            {error && <ErrorBox>{error}</ErrorBox>}
            <div className="modal-actions">
              <Button onClick={() => setEditing(null)}>{t("Cancel")}</Button>
              <Button primary type="submit" disabled={busy}>
                {t("Save")}
              </Button>
            </div>
          </form>
        </Modal>
      )}
    </section>
  );
}
