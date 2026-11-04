import { t } from "../../lib/i18n";
import { useEffect, useState } from "react";
import {
  IconBook2,
  IconCheck,
  IconSettings,
  IconAlertCircle,
  IconArrowUpRight,
} from "@tabler/icons-react";
import { api, date, message } from "../../lib/api";
import { Badge, Button, Empty, ErrorBox, Modal } from "../../components/ui";
import type { Project, WikiCompilation, WikiHealth } from "../../domain/types";

interface Props {
  project: Project;
  view: "memory" | "health";
  onProject: (p: Project) => void;
  onChapter: (id: string) => void;
  onReview: () => void;
}

/** Present project memory and knowledge checks with clear source and review boundaries. */
export function StoryMemory({
  project,
  view,
  onProject,
  onChapter,
  onReview,
}: Props) {
  const [health, setHealth] = useState<WikiHealth | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [settings, setSettings] = useState(false);
  const [purpose, setPurpose] = useState(project.wiki_purpose);
  useEffect(() => {
    let active = true;
    api<WikiHealth>("wiki_health", { project_id: project.id })
      .then((value) => {
        if (active) setHealth(value);
      })
      .catch((e) => {
        if (active) setError(message(e));
      });
    return () => {
      active = false;
    };
  }, [project]);

  /** Update the author's tracking focus and invalidate earlier compilations. */
  async function savePurpose() {
    setBusy(true);
    setError("");
    try {
      onProject(
        await api<Project>("save_wiki_purpose", {
          project_id: project.id,
          revision: project.revision,
          purpose,
        }),
      );
      setSettings(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  /** Confirm a memory after backend checks of its source and reviewed evidence. */
  async function review(memory: WikiCompilation, status: string) {
    setBusy(true);
    setError("");
    try {
      onProject(
        await api<Project>("review_memory", {
          project_id: project.id,
          revision: project.revision,
          memory_id: memory.id,
          status,
        }),
      );
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="story-memory">
      <div className="info-strip">
        <IconBook2 size={21} />
        <div>
          <strong>{t("Tracking focus for this project")}</strong>
          <p>{project.wiki_purpose}</p>
        </div>
        <Button
          onClick={() => {
            setPurpose(project.wiki_purpose);
            setSettings(true);
          }}
        >
          <IconSettings size={15} />
          {t("Edit focus")}
        </Button>
      </div>
      {error && <ErrorBox>{error}</ErrorBox>}
      {view === "memory" && (
        <>
          <div className="memory-intro">
            <h2>{t("Build a story memory from earlier chapters")}</h2>
            <p>
              {t("Extract evidence from the complete source, then compile it into memory. Matching sources and rules reuse previous results. Author-approved memory informs future writing.")}
            </p>
          </div>
          {!health ? (
            <p className="muted">{t("Checking sources…")}</p>
          ) : !health.compilations.length ? (
            <Empty
              title={t("No story memory yet")}
              detail={t("Choose Compile Wiki in the writing workspace. Evidence is extracted first, followed by memory compilation and author review.")}
            />
          ) : (
            health.compilations.map(({ record, current, source_title }) => {
              const ids = [
                ...new Set(
                  record.sections.flatMap((section) => section.evidence_ids),
                ),
              ];
              const unreviewed = ids.filter(
                (id) =>
                  project.facts.find((f) => f.id === id)?.status !==
                  "confirmed",
              ).length;
              return (
                <article className="memory-card" key={record.id}>
                  <div className="memory-heading">
                    <h3>{source_title}</h3>
                    <Badge
                      tone={
                        !current
                          ? "red"
                          : record.status === "confirmed"
                            ? "green"
                            : record.status === "rejected"
                              ? "gray"
                              : "amber"
                      }
                    >
                      {!current
                        ? t("Dependencies changed")
                        : record.status === "confirmed"
                          ? t("Confirmed")
                          : record.status === "rejected"
                            ? t("Ignored")
                            : t("Pending review")}
                    </Badge>
                  </div>
                  <p className="memory-meta">
                    {record.chunks}{" "}{t("source chunks ·")}{" "}{record.model} ·{" "}
                    {date(record.created_at)}
                  </p>
                  {!record.sections.length && (
                    <p className="muted">
                      {t("This analysis found no evidence for a story memory.")}
                    </p>
                  )}
                  {record.sections.map((section, index) => (
                    <section className="memory-section" key={index}>
                      <p className="preserve">{section.summary}</p>
                      {section.questions.length > 0 && (
                        <div className="memory-questions">
                          <strong>{t("Questions for the author")}</strong>
                          <ul>
                            {section.questions.map((question, index) => (
                              <li key={index}>{question}</li>
                            ))}
                          </ul>
                        </div>
                      )}
                      <details>
                        <summary>
                          {t("Review")}{" "}{section.evidence_ids.length}{" "}{t("cited evidence records")}
                        </summary>
                        {section.evidence_ids.map((id) => {
                          const fact = project.facts.find((f) => f.id === id);
                          return fact ? (
                            <div className="memory-evidence" key={id}>
                              <strong>
                                {fact.entity} · {fact.claim}
                              </strong>
                              <blockquote>{fact.quote}</blockquote>
                              <Badge
                                tone={
                                  fact.status === "confirmed"
                                    ? "green"
                                    : "amber"
                                }
                              >
                                {fact.status === "confirmed"
                                  ? t("Evidence confirmed")
                                  : t("Evidence pending review")}
                              </Badge>
                              <span>
                                {fact.epistemic === "inferred"
                                  ? t("Inferred")
                                  : t("Explicit")}
                              </span>
                            </div>
                          ) : (
                            <ErrorBox key={id}>
                              {t("Cited evidence is unavailable")}
                            </ErrorBox>
                          );
                        })}
                      </details>
                    </section>
                  ))}
                  <div className="memory-actions">
                    {project.chapters.some(
                      (c) => c.id === record.source_id,
                    ) && (
                      <button
                        className="text-button"
                        onClick={() => onChapter(record.source_id)}
                      >
                        {t("Back to source")}
                        <IconArrowUpRight size={14} />
                      </button>
                    )}
                    {unreviewed > 0 && (
                      <Button onClick={onReview}>
                        {t("Review")}{" "}{unreviewed}{" "}{t("cited evidence records")}
                      </Button>
                    )}
                    {current && record.status === "pending" && (
                      <>
                        <Button
                          disabled={busy}
                          onClick={() => review(record, "rejected")}
                        >
                          {t("Ignore this memory")}
                        </Button>
                        <Button
                          primary
                          disabled={
                            busy || unreviewed > 0 || !record.sections.length
                          }
                          onClick={() => review(record, "confirmed")}
                        >
                          <IconCheck size={15} />
                          {t("Confirm story memory")}
                        </Button>
                      </>
                    )}
                    {current && record.status === "confirmed" && (
                      <Button
                        disabled={busy}
                        onClick={() => review(record, "pending")}
                      >
                        {t("Revoke confirmation")}
                      </Button>
                    )}
                  </div>
                </article>
              );
            })
          )}
        </>
      )}
      {view === "health" && (
        <>
          <div className="memory-intro">
            <h2>{t("Review the sources and links behind story knowledge")}</h2>
            <p>
              {t("Review citations, relationships, and versions. Use the story context to judge notices such as multiple recorded locations.")}
            </p>
          </div>
          {!health ? (
            <p className="muted">{t("Checking…")}</p>
          ) : health.findings.length ? (
            health.findings.map((finding, index) => (
              <article
                className="health-finding"
                key={`${finding.kind}-${index}`}
              >
                <IconAlertCircle size={20} />
                <div>
                  <h3>{t(finding.title)}</h3>
                  <p>{t(finding.detail)}</p>
                  {project.chapters.some((c) => c.id === finding.source_id) && (
                    <button
                      className="text-button"
                      onClick={() => onChapter(finding.source_id)}
                    >
                      {t("View related chapter")}
                      <IconArrowUpRight size={14} />
                    </button>
                  )}
                </div>
                <Badge tone={finding.severity === "warning" ? "amber" : "blue"}>
                  {finding.severity === "warning"
                    ? t("Needs update")
                    : finding.severity === "review"
                      ? t("Please review")
                      : t("Notice")}
                </Badge>
              </article>
            ))
          ) : (
            <Empty
              title={t("No structural issues found")}
              detail={t("Source and relationship checks passed. Continue reviewing the interpretation and story logic.")}
            />
          )}
        </>
      )}
      {settings && (
        <Modal
          title={t("Project tracking focus")}
          onClose={() => setSettings(false)}
        >
          <form
            className="modal-content"
            onSubmit={(e) => {
              e.preventDefault();
              void savePurpose();
            }}
          >
            <label>
              {t("What should the knowledge system track?")}
              <textarea
                className="source-input"
                value={purpose}
                maxLength={4000}
                onChange={(e) => setPurpose(e.target.value)}
                placeholder={t("For example, who knows the truth, what a character conceals, when a clue appears, and the evidence for each change.")}
              />
            </label>
            <p className="muted">
              {t("Compile related sources again after changing this focus. Evidence and history remain available.")}
            </p>
            {error && <ErrorBox>{error}</ErrorBox>}
            <div className="modal-actions">
              <Button onClick={() => setSettings(false)}>{t("Cancel")}</Button>
              <Button primary type="submit" disabled={busy || !purpose.trim()}>
                {t("Save tracking focus")}
              </Button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  );
}
