import { useState } from "react";
import { t } from "../../lib/i18n";
import { api, message } from "../../lib/api";
import { Badge, Button, ErrorBox, MarkdownResponse } from "../../components/ui";
import { Select } from "../../components/Select";
import type { Job, Project } from "../../domain/types";

/** Render tool progress and author-reviewed proposals without applying generated content automatically. */
export function ProposalCards({
  job,
  project,
  onJob,
  onRefresh,
  beforeReview,
}: {
  job: Job;
  project: Project;
  onJob: (job: Job) => void;
  onRefresh: () => Promise<void>;
  beforeReview?: () => Promise<Project | null>;
}) {
  const [edits, setEdits] = useState<Record<string, string>>({});
  const [modes, setModes] = useState<Record<string, string>>({});
  const [targets, setTargets] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  /** Apply a single explicit decision and refresh both the shared work and current job snapshot. */
  async function review(id: string, decision: string) {
    setBusy(id);
    setError("");
    try {
      const current = beforeReview ? await beforeReview() : project;
      if (!current) return;
      const result = await api<{ project: Project; job: Job }>(
        "review_proposal",
        {
          project_id: current.id,
          revision: current.revision,
          job_id: job.id,
          proposal_id: id,
          decision,
          content: edits[id],
          mode: modes[id],
          supersedes_id:
            job.proposals?.find((proposal) => proposal.id === id)?.kind ===
            "memory"
              ? (targets[id] ??
                job.proposals.find((proposal) => proposal.id === id)?.memory
                  ?.supersedes ??
                "")
              : undefined,
        },
      );
      onJob(result.job);
      await onRefresh();
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="proposal-section">
      {!!job.steps?.length && (
        <details className="agent-trace" open={job.status === "running"}>
          <summary>
            {t("Task steps")} · {t("Model calls")} {job.calls || 0}
          </summary>
          {job.steps.map((step) => (
            <div key={step.id} className="agent-step">
              <Badge
                tone={
                  step.status === "error"
                    ? "red"
                    : step.status === "done"
                      ? "green"
                      : "blue"
                }
              >
                {step.tool}
              </Badge>
              <span>{t(step.status)}</span>
              <details>
                <summary>{t("Details")}</summary>
                <pre>{step.detail}</pre>
              </details>
            </div>
          ))}
        </details>
      )}
      {!!job.proposals?.length && (
        <p className="muted">
          {t("No changes are applied until you accept them.")}
        </p>
      )}
      {job.proposals?.map((proposal) => (
        <article className="proposal-card" key={proposal.id}>
          <header>
            <strong>{proposal.title}</strong>
            <Badge>
              {t(
                proposal.status === "accepted"
                  ? "Accepted"
                  : proposal.status === "rejected"
                    ? "Rejected"
                    : "Proposed",
              )}
            </Badge>
          </header>
          <MarkdownResponse
            text={
              proposal.content ||
              proposal.plan?.goal ||
              proposal.memory?.content ||
              ""
            }
          />
          {proposal.plan && (
            <p className="muted">
              {t("Chapter")} {proposal.plan.start_chapter}–
              {proposal.plan.end_chapter} · {proposal.plan.target_characters}{" "}
              {t("characters")}
            </p>
          )}
          {proposal.kind === "chapter" && proposal.status === "proposed" && (
            <label>
              {t("Apply prose")}
              <Select
                value={modes[proposal.id] || proposal.mode || "append"}
                onChange={(event) =>
                  setModes({ ...modes, [proposal.id]: event.target.value })
                }
              >
                <option value="append">{t("Append to chapter")}</option>
                <option value="replace">{t("Replace chapter draft")}</option>
              </Select>
            </label>
          )}
          {proposal.memory && proposal.status === "proposed" && (
            <>
              <label>
                {t("Memory update target")}
                <Select
                  aria-label={t("Memory update target")}
                  value={
                    targets[proposal.id] ?? proposal.memory.supersedes ?? ""
                  }
                  onChange={(event) =>
                    setTargets({
                      ...targets,
                      [proposal.id]: event.target.value,
                    })
                  }
                >
                  <option value="">{t("Create independent memory")}</option>
                  {project.studio.memories
                    .filter(
                      (memory) =>
                        memory.status === "confirmed" &&
                        memory.kind === proposal.memory?.kind &&
                        memory.entity.trim().toLocaleLowerCase() ===
                          proposal.memory.entity.trim().toLocaleLowerCase(),
                    )
                    .map((memory) => (
                      <option value={memory.id} key={memory.id}>
                        {memory.entity} · {memory.title} · {t("Chapter")}{" "}
                        {memory.chapter}
                      </option>
                    ))}
                </Select>
              </label>
              {project.studio.memories
                .filter(
                  (memory) =>
                    memory.id ===
                    (targets[proposal.id] ?? proposal.memory?.supersedes),
                )
                .map((memory) => (
                  <details key={memory.id}>
                    <summary>{t("Previous memory version")}</summary>
                    <MarkdownResponse text={memory.content} />
                    <p>
                      {t("Known by")}:{" "}
                      {memory.known_by.join("、") || t("Not recorded")}
                    </p>
                  </details>
                ))}
              {proposal.memory.quote && (
                <blockquote>{proposal.memory.quote}</blockquote>
              )}
            </>
          )}
          {proposal.plan?.parent_id &&
            job.proposals?.some(
              (parent) =>
                parent.status === "proposed" &&
                parent.plan?.id === proposal.plan?.parent_id,
            ) && <p className="muted">{t("Accept the parent plan first")}</p>}
          {proposal.status === "proposed" && job.status === "done" && (
            <>
              <details>
                <summary>{t("Edit before accepting")}</summary>
                <textarea
                  aria-label={t("Review proposal")}
                  value={
                    edits[proposal.id] ??
                    proposal.plan?.goal ??
                    proposal.memory?.content ??
                    proposal.content
                  }
                  onChange={(event) =>
                    setEdits({ ...edits, [proposal.id]: event.target.value })
                  }
                />
              </details>
              <div className="answer-actions">
                <Button
                  primary
                  disabled={
                    !!busy ||
                    !!(
                      proposal.plan?.parent_id &&
                      job.proposals?.some(
                        (parent) =>
                          parent.status === "proposed" &&
                          parent.plan?.id === proposal.plan?.parent_id,
                      )
                    )
                  }
                  onClick={() => void review(proposal.id, "accepted")}
                >
                  {t("Accept")}
                </Button>
                <Button
                  disabled={!!busy}
                  onClick={() => void review(proposal.id, "rejected")}
                >
                  {t("Reject")}
                </Button>
              </div>
            </>
          )}
        </article>
      ))}
      {error && <ErrorBox>{error}</ErrorBox>}
    </div>
  );
}
