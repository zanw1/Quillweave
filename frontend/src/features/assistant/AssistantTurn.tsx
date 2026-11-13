import { useEffect, useState } from "react";
import { IconBook2, IconCopy, IconSparkles } from "@tabler/icons-react";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import { Badge, Button, ErrorBox, MarkdownResponse } from "../../components/ui";
import {
  actions,
  type ChangeSet,
  type Job,
  type Project,
  type WritingFollowup,
} from "../../domain/types";
import { ChangeReviewCard } from "./ChangeReviewCard";
import { ProposalCards } from "./ProposalCards";
import { visibleAssistantText, visibleAuthorInstruction } from "./visibleText";
import { ChatActivity } from "./ChatActivity";
import { isVisibleChatActivity } from "./visibleText";

interface Props {
  run: Job;
  project: Project;
  disabled: boolean;
  onBranch: (
    run: Job,
    regenerate?: boolean,
    resume?: boolean,
    followup?: WritingFollowup,
  ) => Promise<void>;
  onExtract: (run: Job) => Promise<void>;
  onJob: (job: Job) => void;
  onRefresh: () => Promise<void>;
  beforeStart?: () => Promise<Project | null>;
  onOpenTarget?: (kind: string, id: string) => void;
  onContext: (job: Job) => void;
  onApply?: (job: Job) => Promise<void>;
  chapterId?: string;
}

/** Render current and historical responses with the same status, evidence and review actions. */
export function AssistantTurn(props: Props) {
  const [detail, setDetail] = useState<Job | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(
    () => setDetail(null),
    [props.run.id, props.run.text, props.run.status],
  );
  const job = detail || props.run;
  const running = job.status === "running";
  const answer = visibleAssistantText(job.text);
  const interrupted = ["cancelled", "error", "interrupted"].includes(
    job.status,
  );
  const compatible = job.chapter_id === (props.chapterId || "");
  const writingPhases: Record<string, string> = {
    brief: "Chapter brief",
    composing: "Composing the scene",
    retrieving: "Gathering story evidence",
    drafting: "Writing prose",
    revising: "Revising the candidate",
    reviewing: "Reviewing the candidate",
    ready: "Ready for author review",
  };
  const writingPhase = writingPhases[job.writing?.phase || ""];
  const writingPurposes: Record<string, string> = {
    writing_composition: "Composing the scene",
    writing_draft: "Writing prose",
    writing_review: "Reviewing the candidate",
  };

  /** Start an explicit author-directed follow-up using the complete candidate as an unaccepted draft. */
  async function followWriting(kind: "composition" | "trial" | "revision") {
    const candidate = job.writing?.candidate_body || job.writing?.draft || "";
    const task =
      kind === "trial"
        ? t(
            "Try one short alternative scene to explore character voice. Keep it as an unaccepted trial and explain the difference briefly.",
          )
        : kind === "composition"
          ? t(
              "Reconsider the local composition from character goals, refusals, practical conditions and consequences, then write a fresh candidate. Respect locked author decisions.",
            )
          : t(
              "Revise the complete candidate using the located editing feedback. Preserve successful passages and facts outside the affected area.",
            );
    await props.onBranch(job, true, false, {
      action: kind === "trial" ? "ask" : "rewrite",
      intentHint:
        kind === "composition"
          ? "writing_recompose"
          : kind === "trial"
            ? "writing_trial"
            : "writing_revision",
      instruction: `${task}\n${job.instruction}\n<unaccepted_candidate>\n${candidate}\n</unaccepted_candidate>\n<composition>\n${JSON.stringify(job.writing?.composition || null)}\n</composition>\n<editing_feedback>\n${JSON.stringify(job.writing?.review || null)}\n</editing_feedback>`,
    });
  }

  /** Hydrate older executions on demand without changing the selected conversation. */
  async function loadDetails() {
    setBusy(true);
    setError("");
    try {
      const loaded = await api<Job>("get_job", { job_id: job.id });
      setDetail(loaded);
      props.onJob(loaded);
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  /** Surface action failures on the response that owns the action. */
  async function act(operation: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await operation();
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  /** Replace one reviewed candidate while preserving the other task fields. */
  function updateChangeSet(updated: ChangeSet) {
    const next = {
      ...job,
      changesets: job.changesets?.map((item) =>
        item.id === updated.id ? updated : item,
      ),
    };
    setDetail(next);
    props.onJob(next);
  }
  return (
    <article className="conversation discussion-turn" data-job-id={job.id}>
      <div className="user-message">
        <span>
          {t("Author ·")} {t(actions[job.action] || job.action)}
        </span>
        <p>
          {visibleAuthorInstruction(job.instruction, job.intent_hint) ||
            t(
              "Analyze the complete source and organize character trajectories and clues.",
            )}
        </p>
      </div>
      <div className="answer-heading">
        <IconSparkles size={17} />
        <span>Quillweave</span>
        <Badge
          tone={
            job.status === "done"
              ? "green"
              : job.status === "error"
                ? "red"
                : "blue"
          }
        >
          {t(
            running
              ? writingPhase || "Generating"
              : job.status === "done"
                ? "Complete"
                : job.status === "cancelled"
                  ? "Cancelled"
                  : "Incomplete",
          )}
        </Badge>
      </div>
      {job.model && <p className="chat-turn-model">{job.model}</p>}
      {job.action === "wiki" ? (
        <div className="wiki-result">
          <IconBook2 size={23} />
          <h3>{t(job.progress)}</h3>
          <p>
            {t(
              "Review each claim and its interpretation in the Character Wiki. Quotes link back to the source.",
            )}
          </p>
          <details>
            <summary>{t("View raw analysis")}</summary>
            <pre>{answer || t("Waiting for the model…")}</pre>
          </details>
        </div>
      ) : job.activity?.length ? (
        <>
          <ChatActivity job={job} />
          {!job.activity.filter(isVisibleChatActivity).some((item) => item.kind === "text" && item.text) &&
            answer && (
              <MarkdownResponse
                text={answer}
                streaming={running}
                incomplete={interrupted}
              />
            )}
        </>
      ) : answer ? (
        <MarkdownResponse
          text={answer}
          streaming={running}
          incomplete={interrupted}
        />
      ) : (
        <p className="answer-text">
          {t(
            running
              ? job.progress || "Waiting for the model…"
              : job.changesets?.length
                ? "Review changes"
                : "This task ended before returning text.",
          )}
        </p>
      )}
      {job.error && <ErrorBox>{job.error}</ErrorBox>}
      {!running && job.commit_policy === "auto" && job.progress && (
        <p className="chat-save-receipt" role="status">{t(job.progress)}</p>
      )}
      {error && <ErrorBox>{error}</ErrorBox>}
      {job.writing && (
        <section className="writing-review" aria-label={t("Writing workflow")}>
          {job.writing.review_status && (
            <p role="status">
              <Badge tone={job.writing.review_status === "complete" ? "green" : "gray"}>
                {t(
                  {
                    complete: "Review complete",
                    failed: "Review failed · Candidate retained",
                    unstructured: "Review needs checking · Candidate retained",
                    needs_plan: "Chapter outline feedback pending",
                    needs_revision: "Revision feedback pending",
                  }[job.writing.review_status] || "Review pending",
                )}
              </Badge>
            </p>
          )}
          {job.writing.composition && (
            <details open={running && job.writing.phase === "composing"}>
              <summary>{t("Selected composition")}</summary>
              <p className="preserve">{job.writing.composition.intent}</p>
              <p className="preserve">{job.writing.composition.approach}</p>
              {job.writing.composition.scene_cards?.map((scene, index) => (
                <div className="writing-scene" key={index}>
                  <strong>{scene.pov} · {scene.location}</strong>
                  <p>{scene.goal} → {scene.choice} → {scene.outcome}</p>
                  <p className="preserve">{scene.action}</p>
                  {scene.characters.map((character, characterIndex) => (
                    <p key={characterIndex}>{character.name}：{character.want}；{character.response}</p>
                  ))}
                  {scene.handoff && <p>{scene.handoff}</p>}
                </div>
              ))}
              {(
                [
                  ["Scene notes", job.writing.composition.scene_notes],
                  [
                    "Evidence requests",
                    job.writing.composition.evidence_requests,
                  ],
                  [
                    "Revelation boundaries",
                    job.writing.composition.reveal_notes,
                  ],
                  ["Open questions", job.writing.composition.open_questions],
                ] as const
              ).map(([label, values]) =>
                values?.length ? (
                  <div key={label}>
                    <strong>{t(label)}</strong>
                    <ul>
                      {values.map((value, index) => (
                        <li key={index}>{value}</li>
                      ))}
                    </ul>
                  </div>
                ) : null,
              )}
              {!!job.writing.composition.evidence_ids?.length && (
                <p className="muted">
                  {t("Evidence")} ·{" "}
                  {job.writing.composition.evidence_ids.join(" · ")}
                </p>
              )}
              {job.writing.composition.sample && (
                <blockquote className="preserve">
                  {job.writing.composition.sample}
                </blockquote>
              )}
            </details>
          )}
          <details>
            <summary>{t("Chapter brief")}</summary>
            <p className="preserve">{job.writing.brief}</p>
          </details>
          {job.writing.review && (
            <details
              open={
                job.writing.review.issues.length > 0 ||
                !!job.writing.review.warning
              }
            >
              <summary>
                {t("Editing feedback")} · {job.writing.review.issues.length}
              </summary>
              {job.writing.review.warning && (
                <p role="status">{job.writing.review.warning}</p>
              )}
              <MarkdownResponse text={job.writing.review.summary} />
              {job.writing.review.issues.map((issue, index) => (
                <article className="writing-issue" key={index}>
                  <Badge>
                    {t(
                      issue.kind === "continuity"
                        ? "Continuity"
                        : "Reading experience",
                    )}
                  </Badge>
                  {issue.scope && (
                    <Badge>{t(`Editing scope ${issue.scope}`)}</Badge>
                  )}
                  <blockquote>{issue.quote}</blockquote>
                  <p>{issue.explanation}</p>
                  <p>{issue.suggestion}</p>
                </article>
              ))}
            </details>
          )}
          {!!job.writing.stages.length && (
            <details>
              <summary>
                {t("Actual stage inputs")} · {job.writing.stages.length}
              </summary>
              <div className="studio-actions">
                {job.writing.stages.map((stage, index) => (
                  <Button
                    key={index}
                    onClick={() =>
                      props.onContext({ ...job, prompt: stage.prompt })
                    }
                  >
                    {index + 1} · {t(writingPhases[stage.name] || stage.name)}
                  </Button>
                ))}
              </div>
            </details>
          )}
          {job.writing.metrics?.map((metric, index) => (
            <p className="muted" key={`${metric.request_hash}-${index}`}>
              {t("Stage")} ·{" "}
              {t(writingPurposes[metric.purpose] || metric.purpose)} ·{" "}
              {metric.model} · {t("Duration")}{" "}
              {(metric.duration_ms / 1000).toFixed(1)} s · {t("Input tokens")}{" "}
              {metric.usage.input_tokens !== undefined
                ? metric.usage.input_tokens.toLocaleString()
                : t("Not reported")}{" "}
              / {t("Output tokens")}{" "}
              {metric.usage.output_tokens !== undefined
                ? metric.usage.output_tokens.toLocaleString()
                : t("Not reported")}
              {!!metric.usage.reasoning_tokens &&
                ` · ${t("think")} ${metric.usage.reasoning_tokens.toLocaleString()}`}
            </p>
          ))}
          {job.writing.calls !== undefined && (
            <p className="muted">
              {t("Writing model calls")} · {job.writing.calls}
            </p>
          )}
          {!running &&
            compatible &&
            job.writing.mode !== "changeset" &&
            !!job.writing.draft && (
              <div className="studio-actions">
                <Button
                  disabled={props.disabled || busy}
                  onClick={() => void act(() => followWriting("composition"))}
                >
                  {t("Rework composition")}
                </Button>
                <Button
                  disabled={props.disabled || busy}
                  onClick={() => void act(() => followWriting("trial"))}
                >
                  {t("Try a short scene")}
                </Button>
                <Button
                  disabled={props.disabled || busy}
                  onClick={() => void act(() => followWriting("revision"))}
                >
                  {t("Revise candidate")}
                </Button>
              </div>
            )}
        </section>
      )}
      <ChangeReviewCard
        project={props.project}
        changesets={running ? [] : job.changesets || []}
        onRefresh={props.onRefresh}
        onOpenTarget={props.onOpenTarget}
        beforeConfirm={props.beforeStart}
        onChangeSetUpdated={updateChangeSet}
      />
      <ProposalCards
        job={job}
        project={props.project}
        onJob={(next) => {
          setDetail(next);
          props.onJob(next);
        }}
        onRefresh={props.onRefresh}
        beforeReview={props.beforeStart}
      />
      {!running && (
        <div className="answer-actions">
          {answer && (
            <Button
              onClick={() =>
                void act(() => navigator.clipboard.writeText(answer))
              }
            >
              <IconCopy size={15} />
              {t("Copy")}
            </Button>
          )}
          {!job.model && (
            <Button disabled={busy} onClick={() => void loadDetails()}>
              {t("Load task details")}
            </Button>
          )}
          {!!job.prompt.messages.length && (
            <Button onClick={() => props.onContext(job)}>
              {t("Request context")}
            </Button>
          )}
          {job.action !== "wiki" && (
            <>
              <Button
                disabled={props.disabled || busy}
                onClick={() => void act(() => props.onBranch(job))}
              >
                {t("Edit and branch")}
              </Button>
              <Button
                disabled={props.disabled || busy}
                onClick={() => void act(() => props.onBranch(job, true))}
              >
                {t(job.status === "done" ? "Regenerate" : "Retry")}
              </Button>
              {interrupted && (
                <Button
                  disabled={props.disabled || busy}
                  onClick={() =>
                    void act(() => props.onBranch(job, true, true))
                  }
                >
                  {t(
                    job.action === "agent"
                      ? "Resume task"
                      : "Continue interrupted response",
                  )}
                </Button>
              )}
            </>
          )}
          {job.status === "done" && answer && !job.changesets?.length && (
            <Button
              disabled={props.disabled || busy}
              onClick={() => void act(() => props.onExtract(job))}
            >
              {t("Extract & stage settings")}
            </Button>
          )}
          {props.onApply &&
            compatible &&
            !job.changesets?.length &&
            job.status === "done" &&
            ["continue", "rewrite", "architecture", "outline"].includes(
              job.action,
            ) && (
              <Button
                primary
                disabled={props.disabled || busy}
                onClick={() => void act(() => props.onApply!(job))}
              >
                {t(
                  job.action === "continue"
                    ? "Append to chapter"
                    : job.action === "rewrite"
                      ? "Replace chapter draft"
                      : job.action === "outline"
                        ? "Review outline"
                        : "Apply to story setup",
                )}
              </Button>
            )}
        </div>
      )}
    </article>
  );
}
