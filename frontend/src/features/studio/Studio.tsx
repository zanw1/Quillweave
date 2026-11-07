import { useState } from "react";
import { IconSettings, IconArrowRight } from "@tabler/icons-react";
import { api } from "../../lib/api";
import { t } from "../../lib/i18n";
import { Badge, Button, Modal } from "../../components/ui";
import { ChangeReviewCard } from "../assistant/ChangeReviewCard";
import { ProjectModels } from "../settings/models";
import { StoryGoals, formNames, stageNames } from "./StoryGoals";
import type { ChangeSet, Project } from "../../domain/types";
import "./studio.css";

interface Props {
  project: Project;
  onProject: (project: Project) => void;
  onSettings: () => void;
  onAsk: (text: string, action?: string) => void;
  onSelectFlowStep: (step: string) => void;
  onOpenTarget: (kind: string, id: string) => void;
}

/** Present the saved story, current work and pending changes in the shared workspace. */
export function Studio({
  project,
  onProject,
  onSettings,
  onAsk,
  onSelectFlowStep,
  onOpenTarget,
}: Props) {
  const [goals, setGoals] = useState(false);
  const [receipt, setReceipt] = useState<ChangeSet | null>(null);
  const pending = (project.studio.changesets || []).filter(
    (cs) => cs.status === "staged" || cs.status === "conflicted",
  );
  const decisions = project.studio.decisions.filter(
    (item) => item.status === "accepted",
  );
  const confirmedPlans = project.studio.plans.filter(
    (item) => item.status === "confirmed",
  );
  const written = project.chapters.filter((item) => item.body.trim());
  const hasPlans = confirmedPlans.length > 0 || project.chapters.length > 0;
  const hasSettings =
    project.entities.length > 0 ||
    (project.studio.locations || []).length > 0 ||
    project.studio.memories.some((item) => item.status === "confirmed");
  const nextStep = hasPlans
    ? "write"
    : hasSettings
      ? "plan"
      : project.premise.trim() || decisions.length
        ? "world"
        : "setup";
  const nextLabel = {
    setup: "Discuss the story",
    world: "Develop the setting",
    plan: "Plan the next chapters",
    write: "Continue writing",
  }[nextStep];

  /** Reload confirmed content and independently stored review candidates after an author action. */
  async function refresh() {
    onProject(await api<Project>("get_project", { project_id: project.id }));
  }

  return (
    <div className="workspace-overview">
      <header>
        <div>
          <h1>{project.title || t("Untitled story")}</h1>
          <div className="workspace-overview-actions">
            <Badge>{t(formNames[project.studio.mode])}</Badge>
            <Badge>{t(stageNames[project.studio.stage])}</Badge>
          </div>
        </div>
        <Button onClick={() => setGoals(true)}>
          <IconSettings size={16} />
          {t("Story goals")}
        </Button>
      </header>
      <section className="workspace-overview-section">
        <h2>{t("Premise")}</h2>
        {project.premise.trim() ? (
          <p className="workspace-overview-summary">{project.premise}</p>
        ) : (
          <p className="muted">
            {t("Start with an idea. Decide when you are ready to outline.")}
          </p>
        )}
        <div className="workspace-overview-actions">
          <Button
            primary
            onClick={() =>
              nextStep === "setup"
                ? onAsk(
                    t(
                      "Help me explore the central idea, character motivation and main conflict. Ask about the most important missing detail first.",
                    ),
                  )
                : onSelectFlowStep(nextStep)
            }
          >
            {t(nextLabel)}
            <IconArrowRight size={16} />
          </Button>
          <Button
            onClick={() =>
              onAsk(
                t(
                  "Review our discussion and prepare a structured setting preview for my confirmation.",
                ),
              )
            }
          >
            {t("Prepare setting changes")}
          </Button>
        </div>
      </section>
      <div className="workspace-progress">
        <div>
          <strong>{project.entities.length}</strong>
          <small>{t("Characters & world entries")}</small>
        </div>
        <div>
          <strong>{confirmedPlans.length}</strong>
          <small>{t("Confirmed plans")}</small>
        </div>
        <div>
          <strong>{written.length}</strong>
          <small>{t("Written chapters")}</small>
        </div>
      </div>
      {pending.length > 0 && (
        <section className="workspace-overview-section">
          <h2>
            {t("Pending changes")} · {pending.length}
          </h2>
          <ChangeReviewCard
            project={project}
            changesets={pending}
            onRefresh={refresh}
            onOpenTarget={onOpenTarget}
            onChangeSetUpdated={(cs) => {
              if (cs.status !== "staged") setReceipt(cs);
            }}
          />
        </section>
      )}
      {receipt && !pending.some((cs) => cs.id === receipt.id) && (
        <ChangeReviewCard
          project={project}
          changesets={[receipt]}
          onRefresh={refresh}
          onChangeSetUpdated={setReceipt}
          onOpenTarget={onOpenTarget}
        />
      )}
      <details className="workspace-overview-section">
        <summary>{t("Change history")}</summary>
        <ChangeReviewCard
          project={project}
          changesets={(project.studio.changesets || [])
            .filter(
              (cs) =>
                !["staged", "conflicted"].includes(cs.status) &&
                cs.id !== receipt?.id,
            )
            .slice(-10)
            .reverse()}
          onRefresh={refresh}
          onOpenTarget={onOpenTarget}
        />
      </details>
      {!!decisions.length && (
        <section className="workspace-overview-section">
          <h2>{t("Saved author decisions")}</h2>
          {decisions.map((item) => (
            <details key={item.id}>
              <summary>{item.title}</summary>
              <p className="workspace-overview-summary">{item.content}</p>
            </details>
          ))}
        </section>
      )}
      {project.outline.trim() && (
        <section className="workspace-overview-section">
          <h2>{t("Story outline")}</h2>
          <p className="workspace-overview-summary">{project.outline}</p>
          <Button onClick={() => onSelectFlowStep("plan")}>
            {t("Open outline")}
          </Button>
        </section>
      )}
      {goals && (
        <Modal title={t("Story goals")} wide onClose={() => setGoals(false)}>
          <StoryGoals
            project={project}
            onProject={onProject}
            onClose={() => setGoals(false)}
          />
          <details className="modal-content">
            <summary>{t("Model assignments")}</summary>
            <ProjectModels project={project} onProject={onProject} />
            <Button onClick={onSettings}>{t("Model settings")}</Button>
          </details>
        </Modal>
      )}
    </div>
  );
}
