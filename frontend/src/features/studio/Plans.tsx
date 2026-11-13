import { useEffect, useState } from "react";
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
import { publishSetupDraft } from "./setupDrafts";
import { ReviewSetupButton } from "./ReviewSetupButton";

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
      ? parent.level === "volume"
        ? "chapter"
        : levels[Math.min(4, levels.indexOf(parent.level) + 1)]
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
    flexibility: "flexible",
    open_questions: [],
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
  onFocus,
  onChapter,
  focusId,
}: {
  project: Project;
  onProject: (project: Project) => void;
  onAsk: (text: string, action?: string, intent?: string) => void;
  onFocus?: (id: string) => void;
  onChapter?: (id: string) => void;
  focusId?: string;
}) {
  const [editing, setEditing] = useState<StoryPlan | null>(null);
  const [selectedId, setSelectedId] = useState(focusId || "");
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const plansById = new Map(project.studio.plans.map((plan) => [plan.id, plan]));
  const selectedPlan =
    plansById.get(focusId || selectedId) ||
    project.studio.plans[0];
  const rows = orderedPlans(project.studio.plans);
  const duplicateOutlines =
    selectedPlan?.level === "chapter"
      ? project.studio.plans.filter(
          (plan) =>
            plan.level === "chapter" && plan.status === "confirmed" &&
            plan.start_chapter === selectedPlan.start_chapter,
        )
      : [];
  /** Limit materialization to confirmed descendants of the currently focused plan. */
  const scopedChapterPlans = rows.filter(({ plan }) => {
    if (plan.level !== "chapter" || plan.status !== "confirmed") return false;
    let current: StoryPlan | undefined = plan;
    while (current) {
      if (current.id === selectedPlan?.id) return true;
      current = plansById.get(current.parent_id);
    }
    return false;
  });
  useEffect(() => {
    if (selectedPlan && !focusId) onFocus?.(selectedPlan.id);
  }, [selectedPlan?.id, focusId]);
  useEffect(() => {
    if (!focusId) return;
    setCollapsed((current) => {
      const next = new Set(current);
      let parentId = project.studio.plans.find(
        (plan) => plan.id === focusId,
      )?.parent_id;
      while (parentId) {
        next.delete(parentId);
        parentId = project.studio.plans.find(
          (plan) => plan.id === parentId,
        )?.parent_id;
      }
      return next.size === current.size ? current : next;
    });
  }, [focusId, project.id]);

  /** Select a plan while keeping the assistant's focused planning context in sync. */
  function selectPlan(id: string) {
    setSelectedId(id);
    onFocus?.(id);
  }

  /** Collapse only directory descendants, retaining the selected detail for reading. */
  function togglePlan(id: string) {
    setCollapsed((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  /** Hide rows beneath a collapsed ancestor without changing their saved hierarchy. */
  function visiblePlan(plan: StoryPlan) {
    let parentId = plan.parent_id;
    while (parentId) {
      if (collapsed.has(parentId)) return false;
      parentId =
        project.studio.plans.find((parent) => parent.id === parentId)
          ?.parent_id || "";
    }
    return true;
  }
  useEffect(() => {
    publishSetupDraft(
      "plan",
      editing
        ? {
            label: editing.title || t("Add plan"),
            text: JSON.stringify(editing),
          }
        : null,
    );
    return () => publishSetupDraft("plan", null);
  }, [editing]);
  const [base, setBase] = useState(project.revision);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (focusId)
      document
        .getElementById(`plan-${focusId}`)
        ?.scrollIntoView({ block: "nearest" });
  }, [focusId]);

  /** Capture the revision belonging to the draft so stale edits cannot overwrite newer plans. */
  function open(plan: StoryPlan) {
    setEditing({ ...plan, characters: (plan.characters || []).map(characterName) });
    setBase(project.revision);
    setError("");
  }

  /** Render legacy entity identifiers as names while retaining ordinary author-entered names. */
  function characterName(value: string) {
    return project.entities.find((entity) => entity.id === value)?.name || value;
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
      if (command === "save_plan") {
        const saved =
          next.studio.plans.find((plan) => plan.id === editing?.id) ||
          next.studio.plans.find(
            (plan) => !project.studio.plans.some((old) => old.id === plan.id),
          );
        if (saved) selectPlan(saved.id);
      }
      if (command === "delete_plan") selectPlan(next.studio.plans[0]?.id || "");
      setEditing(null);
      if (command === "create_planned_chapters") {
        const created = next.chapters.find(
          (chapter) => !project.chapters.some((old) => old.id === chapter.id),
        );
        if (created) onChapter?.(created.id);
      }
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="studio-section plans-section">
      <div className="studio-section-heading">
        <div>
          <h2>{t("Plans")}</h2>
          <p className="muted">
            {t("Plan the current scope. Units and scenes are optional.")}
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
        <ReviewSetupButton onAsk={onAsk} />
        <Button
          disabled={!selectedPlan}
          onClick={() =>
            onAsk(
              `${t("Read the saved prose in this planning scope. Review continuing consequences, independent character choices, repeated payoffs and the original reading experience. Propose only affected future plan changes, retaining unresolved questions.")}\n${JSON.stringify(selectedPlan)}`,
              "agent",
            )
          }
        >
          {t("Review this story unit")}
        </Button>
        <Button
          disabled={
            busy || scopedChapterPlans.length === 0
          }
          onClick={() =>
            void change("create_planned_chapters", {
              plan_id: selectedPlan?.id || "",
            })
          }
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
      <div className="plan-workspace">
        {!!rows.length && (
          <nav className="plan-directory" aria-label={t("Plan directory")}>
            {rows
              .filter(({ plan }) => visiblePlan(plan))
              .map(({ plan, depth }) => {
                const hasChildren = project.studio.plans.some(
                  (child) => child.parent_id === plan.id,
                );
                return (
                  <div
                    className="plan-tree-row"
                    key={plan.id}
                    style={{ paddingLeft: depth * 14 }}
                  >
                    {hasChildren ? (
                      <button
                        type="button"
                        className="plan-tree-toggle"
                        aria-label={t(
                          collapsed.has(plan.id)
                            ? "Expand plan"
                            : "Collapse plan",
                        )}
                        aria-expanded={!collapsed.has(plan.id)}
                        onClick={() => togglePlan(plan.id)}
                      >
                        {collapsed.has(plan.id) ? "▸" : "▾"}
                      </button>
                    ) : (
                      <span className="plan-tree-toggle" />
                    )}
                    <button
                      type="button"
                      className={`plan-tree-select${selectedPlan?.id === plan.id ? " active" : ""}`}
                      aria-current={
                        selectedPlan?.id === plan.id ? "true" : undefined
                      }
                      onClick={() => selectPlan(plan.id)}
                    >
                      <small>
                        {t(planLevels[plan.level])} · {plan.start_chapter}
                        {plan.end_chapter !== plan.start_chapter
                          ? `–${plan.end_chapter || "…"}`
                          : ""}
                      </small>
                      <span>{plan.title}</span>
                    </button>
                  </div>
                );
              })}
          </nav>
        )}
        <div className="plan-list">
          {(selectedPlan ? [selectedPlan] : []).map((plan) => (
            <article
              className="plan-card"
              key={plan.id}
              id={`plan-${plan.id}`}
              onClick={() => onFocus?.(plan.id)}
              onFocusCapture={() => onFocus?.(plan.id)}
            >
              <header>
                <Badge tone="blue">{t(planLevels[plan.level])}</Badge>
                <h3>{plan.title}</h3>
                <Badge>
                  {t(
                    plan.status === "confirmed"
                      ? "Confirmed"
                      : plan.status === "rejected"
                        ? "Rejected"
                        : "Proposed",
                  )}
                </Badge>
                {plan.flexibility && (
                  <Badge>
                    {t(
                      plan.flexibility === "fixed"
                        ? "Locked direction"
                        : "Revisable direction",
                    )}
                  </Badge>
                )}
              </header>
              {duplicateOutlines.length > 1 && (
                <div className="studio-actions">
                  <p className="muted">
                    {t("Other confirmed outlines for this chapter will become proposals.")}
                  </p>
                  <Button
                    disabled={busy}
                    onClick={() =>
                      void change("save_plan", {
                        plan: { ...plan, status: "confirmed" },
                        resolve_conflicts: true,
                      })
                    }
                  >
                    {t("Use this outline")}
                  </Button>
                </div>
              )}
              <dl>
                <dt>{t("Goal")}</dt>
                <dd>{plan.goal || t("To be developed")}</dd>
                <dt>{t("Conflict")}</dt>
                <dd>{plan.conflict || "—"}</dd>
                <dt>{t("Choice / action")}</dt>
                <dd>{plan.choice || t("To be developed")}</dd>
                <dt>{t("Consequence")}</dt>
                <dd>{plan.consequence || "—"}</dd>
              </dl>
              {!!plan.open_questions?.length && (
                <p className="preserve">
                  {t("Open questions")} · {plan.open_questions.join("\n")}
                </p>
              )}
              <p className="muted">
                {t("Chapter")} {plan.start_chapter}–{plan.end_chapter} ·{" "}
                {plan.target_characters > 0
                  ? `${t("Target characters")} ${plan.target_characters.toLocaleString()}`
                  : t("Target length not set")}{" "}
                · {plan.pacing}
              </p>
              {!!plan.characters?.length && (
                <p className="muted">
                  {t("Characters involved")} ·{" "}
                  {plan.characters.map(characterName).join("、")}
                </p>
              )}
              {!!plan.clues?.length && (
                <p className="preserve">
                  {t("Clues and payoffs")} · {plan.clues.join("\n")}
                </p>
              )}
              <div className="studio-actions">
                {plan.level === "chapter" &&
                  project.chapters.some(
                    (chapter) => chapter.number === plan.start_chapter,
                  ) && (
                    <Button
                      onClick={() =>
                        onChapter?.(
                          project.chapters.find(
                            (chapter) => chapter.number === plan.start_chapter,
                          )!.id,
                        )
                      }
                    >
                      {t("Open chapter")}
                    </Button>
                  )}
                <Button onClick={() => open(plan)}>{t("Edit")}</Button>
                {plan.level !== "scene" && (
                  <Button onClick={() => open(blankPlan(plan))}>
                    {t("Add plan")}
                  </Button>
                )}
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
                {t("Status")}
                <Select
                  value={editing.status}
                  onChange={(event) =>
                    setEditing({ ...editing, status: event.target.value })
                  }
                >
                  <option value="confirmed">{t("Confirmed")}</option>
                  <option value="proposed">{t("Proposed")}</option>
                  <option value="rejected">{t("Rejected")}</option>
                </Select>
              </label>
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
            <label>
              {t("Plan flexibility")}
              <Select
                value={editing.flexibility || ""}
                onChange={(event) =>
                  setEditing({
                    ...editing,
                    flexibility: event.target.value as StoryPlan["flexibility"],
                  })
                }
              >
                <option value="">{t("Existing author decision")}</option>
                <option value="fixed">{t("Locked direction")}</option>
                <option value="flexible">{t("Revisable direction")}</option>
              </Select>
            </label>
            <label>
              {t("Open questions")}
              <textarea
                value={editing.open_questions?.join("\n") || ""}
                onChange={(event) =>
                  setEditing({
                    ...editing,
                    open_questions: event.target.value
                      .split("\n")
                      .filter(Boolean),
                  })
                }
              />
            </label>
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
