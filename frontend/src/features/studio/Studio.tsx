import { useEffect, useState } from "react";
import { IconBulb, IconGitBranch, IconSettings } from "@tabler/icons-react";
import { api, message } from "../../lib/api";
import { getLanguage, t } from "../../lib/i18n";
import { Badge, Button, ErrorBox, Modal } from "../../components/ui";
import { Assistant } from "../assistant/Assistant";
import { ProjectModels } from "../settings/models";
import { StoryGoals, formNames, stageNames } from "./StoryGoals";
import { Plans } from "./Plans";
import { Memories } from "./Memories";
import { Research } from "./Research";
import { StudioMethods } from "./Methods";
import type { Job, Project, WikiHealth } from "../../domain/types";
import "./studio.css";

interface Props {
  project: Project;
  onProject: (project: Project) => void;
  onSettings: () => void;
  onWiki?: () => void;
  onWrite?: () => void;
  initialSeed?: string;
  onSeedConsumed?: () => void;
}
const tabs = {
  discussion: "Discussion",
  plans: "Plans",
  memory: "Story memory",
  research: "Research",
  methods: "Methods and experiments",
};

/** Keep a single author-controlled work shared by discussion, planning, knowledge and research. */
export function Studio({
  project,
  onProject,
  onSettings,
  onWiki,
  onWrite,
  initialSeed = "",
  onSeedConsumed,
}: Props) {
  const [tab, setTab] = useState<keyof typeof tabs>("discussion");
  const [goals, setGoals] = useState(false);
  const [seed, setSeed] = useState({ text: "", action: "agent", version: 0 });
  useEffect(() => {
    if (initialSeed) {
      setSeed((previous) => ({
        text: initialSeed,
        action: "agent",
        version: previous.version + 1,
      }));
      setTab("discussion");
    }
  }, [initialSeed]);
  const [review, setReview] = useState<{
    text: string;
    action: string;
    revision: number;
  } | null>(null);
  const [decision, setDecision] = useState(false);
  const [decisionTitle, setDecisionTitle] = useState("");
  const [decisionText, setDecisionText] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [health, setHealth] = useState<{
    warnings: string[];
    findings: WikiHealth["findings"];
  }>({ warnings: [], findings: [] });
  useEffect(() => {
    let active = true;
    void api<typeof health>("studio", { project_id: project.id })
      .then((value) => {
        if (active) setHealth(value);
      })
      .catch((error) => {
        if (active) setError(message(error));
      });
    return () => {
      active = false;
    };
  }, [project.id, project.revision]);

  /** Restore current shared state after accepted proposals or completed Wiki work. */
  async function refresh() {
    onProject(await api<Project>("get_project", { project_id: project.id }));
  }

  /** Populate a focused request without automatically submitting it or changing creative stages. */
  function ask(text: string, action = "agent") {
    setSeed((previous) => ({ text, action, version: previous.version + 1 }));
    setTab("discussion");
  }

  /** Advance to planning only after an explicit author action. */
  async function beginPlanning() {
    setBusy(true);
    setError("");
    try {
      const next = await api<Project>("save_studio", {
        project_id: project.id,
        revision: project.revision,
        studio: { ...project.studio, stage: "planning" },
      });
      onProject(next);
      setTab("plans");
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  /** Apply an edited legacy outline or setup response only from its inspected project revision. */
  async function applyReview() {
    if (!review?.text.trim()) return;
    setBusy(true);
    setError("");
    try {
      const key = review.action === "outline" ? "outline" : "premise";
      const next = await api<Project>("update_project", {
        project_id: project.id,
        revision: review.revision,
        title: project.title,
        genre: project.genre,
        premise: project.premise,
        style: project.style,
        outline: project.outline,
        [key]: review.text,
      });
      onProject(next);
      setReview(null);
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  /** Record the author's chosen creative decision without inferring approval from an AI answer. */
  async function saveDecision() {
    setBusy(true);
    setError("");
    try {
      onProject(
        await api<Project>("save_decision", {
          project_id: project.id,
          revision: project.revision,
          decision: {
            id: "",
            title: decisionTitle,
            content: decisionText,
            status: "accepted",
            session_id: "",
            created_at: "",
          },
        }),
      );
      setDecision(false);
      setDecisionText("");
      setDecisionTitle("");
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="studio-shell">
      <header className="studio-heading">
        <div>
          <p className="eyebrow">{t("Creative studio")}</p>
          <h1>{project.title}</h1>
          <div className="studio-actions">
            <Badge>{t(formNames[project.studio.mode])}</Badge>
            <Badge tone="blue">{t(stageNames[project.studio.stage])}</Badge>
            {project.studio.target_chapters > 0 && (
              <span>
                {project.studio.target_chapters} {t("chapters")}
              </span>
            )}
            {project.studio.target_characters > 0 && (
              <span>
                {project.studio.target_characters.toLocaleString()}{" "}
                {t("characters")}
              </span>
            )}
          </div>
        </div>
        <div className="studio-actions">
          <Button onClick={() => setDecision(true)}>
            <IconGitBranch size={16} />
            {t("Remember this decision")}
          </Button>
          <Button onClick={() => setGoals(true)}>
            <IconSettings size={16} />
            {t("Story goals")}
          </Button>
          {project.studio.stage !== "planning" && (
            <Button disabled={busy} onClick={() => void beginPlanning()}>
              {t("Start planning")}
            </Button>
          )}
          {onWrite && !!project.chapters.length && (
            <Button onClick={onWrite}>{t("Writing")}</Button>
          )}
        </div>
      </header>
      <nav className="studio-tabs" aria-label={t("Creative studio")}>
        {Object.entries(tabs).map(([key, title]) => (
          <button
            key={key}
            aria-current={tab === key ? "page" : undefined}
            className={tab === key ? "active" : ""}
            onClick={() => setTab(key as keyof typeof tabs)}
          >
            {t(title)}
          </button>
        ))}
      </nav>
      {error && <ErrorBox>{error}</ErrorBox>}
      {!!health.warnings.length && (
        <details className="studio-warning">
          <summary>
            {t("Planning warnings")} ({health.warnings.length})
          </summary>
          {health.warnings.map((warning) => (
            <p key={warning}>{warning}</p>
          ))}
        </details>
      )}
      <div className={`studio-body tab-${tab}`}>
        {tab === "discussion" && (
          <>
            <aside className="studio-brief">
              <IconBulb size={23} />
              <h2>{t("Start wherever you are")}</h2>
              <p>
                {t("Start with an idea. Decide when you are ready to outline.")}
              </p>
              <Button
                onClick={() =>
                  ask(
                    getLanguage() === "zh-CN"
                      ? "从核心创意出发，帮我比较几种不同故事方向。先了解人物欲望、缺陷、冲突与代价，每次只追问关键缺口。"
                      : "Help compare distinct story directions. Start with the central idea, character desires, flaws, conflict and costs. Ask only focused missing questions.",
                  )
                }
              >
                {t("Explore a few possibilities")}
              </Button>
              <Button
                onClick={() =>
                  ask(
                    getLanguage() === "zh-CN"
                      ? "请结合本作品创意提供至少5个有区分度的书名和5个主角名字，说明理由与文化适配，遵守禁用词。"
                      : "Suggest at least five distinctive titles and five protagonist names, explaining their meaning and cultural fit. Respect forbidden names.",
                    "naming",
                  )
                }
              >
                {t("Explore names and titles")}
              </Button>
              <h3>{t("Saved author decisions")}</h3>
              {project.studio.decisions
                .filter((item) => item.status === "accepted")
                .slice(-8)
                .map((item) => (
                  <details key={item.id}>
                    <summary>{item.title}</summary>
                    <p>{item.content}</p>
                  </details>
                ))}
            </aside>
            <Assistant
              project={project}
              initialAction="agent"
              exploring={
                project.studio.stage === "idea" && !project.premise.trim()
              }
              seed={seed.text}
              seedAction={seed.action}
              seedVersion={seed.version}
              onSeedConsumed={() => {
                setSeed((previous) => ({ ...previous, text: "" }));
                onSeedConsumed?.();
              }}
              onSettings={onSettings}
              onRefresh={refresh}
              onApply={async (job: Job) =>
                setReview({
                  text: job.text,
                  action: job.action,
                  revision: project.revision,
                })
              }
            />
          </>
        )}
        {tab === "plans" && (
          <Plans project={project} onProject={onProject} onAsk={ask} />
        )}
        {tab === "memory" && (
          <>
            <Memories
              project={project}
              onProject={onProject}
              onWiki={onWiki || (() => {})}
            />
            <section className="studio-section">
              <h3>{t("Review hints")}</h3>
              {health.findings.map((finding, index) => (
                <article className="research-card" key={index}>
                  <Badge>{finding.kind}</Badge>
                  <strong>{finding.title}</strong>
                  <p>{t(finding.detail)}</p>
                  <small>{finding.source_id}</small>
                </article>
              ))}
            </section>
          </>
        )}
        {tab === "research" && (
          <Research project={project} onProject={onProject} onAsk={ask} />
        )}
        {tab === "methods" && (
          <StudioMethods project={project} onProject={onProject} />
        )}
      </div>
      {goals && (
        <Modal title={t("Story goals")} wide onClose={() => setGoals(false)}>
          <StoryGoals
            project={project}
            onProject={onProject}
            onClose={() => setGoals(false)}
          />
          <div className="modal-content">
            <ProjectModels project={project} onProject={onProject} />
          </div>
        </Modal>
      )}
      {review && (
        <Modal
          title={t("Review proposal")}
          wide
          onClose={() => setReview(null)}
        >
          <div className="modal-content studio-form">
            <label>
              {t("Edit before accepting")}
              <textarea
                value={review.text}
                onChange={(event) =>
                  setReview({ ...review, text: event.target.value })
                }
                rows={14}
              />
            </label>
            {error && <ErrorBox>{error}</ErrorBox>}
            <div className="modal-actions">
              <Button onClick={() => setReview(null)}>{t("Cancel")}</Button>
              <Button
                primary
                disabled={busy}
                onClick={() => void applyReview()}
              >
                {t("Accept")}
              </Button>
            </div>
          </div>
        </Modal>
      )}
      {decision && (
        <Modal
          title={t("Remember this decision")}
          onClose={() => setDecision(false)}
        >
          <form
            className="modal-content studio-form"
            onSubmit={(event) => {
              event.preventDefault();
              void saveDecision();
            }}
          >
            <label>
              {t("Decision title")}
              <input
                value={decisionTitle}
                onChange={(event) => setDecisionTitle(event.target.value)}
                required
                maxLength={120}
              />
            </label>
            <label>
              {t("Decision content")}
              <textarea
                value={decisionText}
                onChange={(event) => setDecisionText(event.target.value)}
                required
                maxLength={16000}
              />
            </label>
            {error && <ErrorBox>{error}</ErrorBox>}
            <Button primary type="submit" disabled={busy}>
              {t("Save")}
            </Button>
          </form>
        </Modal>
      )}
    </div>
  );
}
