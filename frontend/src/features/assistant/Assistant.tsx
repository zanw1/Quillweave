import { t } from "../../lib/i18n";
import { useEffect, useRef, useState } from "react";
import {
  IconSparkles,
  IconArrowUp,
  IconPlayerStop,
  IconCopy,
  IconSettings,
  IconHistory,
  IconChevronDown,
  IconBook2,
} from "@tabler/icons-react";
import { api, message } from "../../lib/api";
import { selectedProfile } from "../settings/models";
import {
  Badge,
  Button,
  ErrorBox,
  Modal,
  Suggestion,
  MarkdownResponse,
} from "../../components/ui";
import {
  actions,
  type Chapter,
  type Job,
  type Message,
  type Project,
  type Prompt,
  type Source,
  type ModelSettings,
  type ModelSettingsResponse,
} from "../../domain/types";

interface Props {
  project: Project;
  chapter?: Chapter;
  source?: Source;
  initialAction?: string;
  exploring?: boolean;
  canHumanize?: boolean;
  seed?: string;
  onSettings: () => void;
  onRefresh: () => Promise<void>;
  beforeStart?: () => Promise<Project | null>;
  onApply?: (job: Job) => Promise<void>;
}

const conversationActions = ["ask", "architecture", "outline"];
const outlineInstruction =
  "Create a complete chapter-by-chapter outline from our discussion and the current story bible. Include the premise, character arcs, act progression, and each chapter's viewpoint, goal, obstacle, choice, consequence, and clues. Identify assumptions and open questions.";
const humanizeInstruction =
  "Polish the current chapter to reduce formulaic AI phrasing. Preserve its original language, plot, names, relationships, viewpoint, chronology, clues, and approximate length; keep the author's style and each character's voice. Trim redundant explanations and stock phrasing, and vary mechanical sentence patterns. Keep intentional imagery, repetition, and punctuation. Make no new plot developments. Return only the complete polished chapter. For this editing task, preserve the source length instead of expanding to the project's chapter-length target.";

const explorationSuggestions = [
  {
    label: "Help me find an idea",
    instruction:
      "I don't have a story idea yet. Help me discover what I would enjoy writing. Start with one or two easy questions about stories, emotions, or situations I like. Don't ask me to decide a genre, title, length, viewpoint, or complete plot yet.",
  },
  {
    label: "I have a small spark",
    instruction:
      "I have a vague image, character, mood, or situation in mind. Ask me about that small spark and help me explore it, one question at a time, without requiring a finished story setup.",
  },
  {
    label: "Explore a few possibilities",
    instruction:
      "Offer three brief, different story seeds to react to. Keep them open-ended, explain what might be interesting about each, and let me choose or combine them before developing a plot or outline.",
  },
];

/** Stream proposals into the conversation for explicit author review. */
export function Assistant({
  project,
  chapter,
  source,
  initialAction = "ask",
  exploring = false,
  canHumanize = !!chapter?.body.trim(),
  seed = "",
  onSettings,
  onRefresh,
  beforeStart,
  onApply,
}: Props) {
  const [instruction, setInstruction] = useState(seed);
  const [action, setAction] = useState(initialAction);
  const [job, setJob] = useState<Job | null>(null);
  const [runs, setRuns] = useState<Job[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [applying, setApplying] = useState(false);
  const applyingRef = useRef(false);
  const [force, setForce] = useState(false);
  const [context, setContext] = useState<Prompt | null>(null);
  const [showHistory, setShowHistory] = useState(false);
  const [settings, setSettings] = useState<ModelSettings | null>(null);
  const planning = !chapter && !source && !!onApply;
  const exploratory = planning && exploring;
  const availableActions = chapter
    ? ["ask", "continue", "rewrite", "review", "wiki"]
    : source
      ? ["ask", "wiki"]
      : conversationActions;
  const conversation = (
    job?.status === "done" && !runs.some((run) => run.id === job.id)
      ? [job, ...runs]
      : runs
  )
    .filter(
      (run) =>
        run.project_id === project.id &&
        run.status === "done" &&
        conversationActions.includes(run.action) &&
        run.chapter_id === (chapter?.id || ""),
    )
    .sort((first, second) => second.created_at.localeCompare(first.created_at))
    .slice(0, 3)
    .reverse();
  const primary = settings
    ? selectedProfile(
        settings,
        project,
        action === "wiki" ? "wiki_extract" : action,
      )
    : undefined;
  const secondary =
    settings && action === "wiki"
      ? selectedProfile(settings, project, "wiki_compile")
      : undefined;
  const model = primary
    ? `${primary.name} · ${primary.config.model}${action === "wiki" ? ` → ${secondary?.name || t("Memory model unassigned")}` : ""}`
    : "No model assigned to this task";
  const end = useRef<HTMLDivElement>(null);
  const refreshRef = useRef(onRefresh);
  refreshRef.current = onRefresh;

  useEffect(() => {
    if (seed) {
      setInstruction(seed);
      setAction("ask");
    }
  }, [seed]);
  useEffect(() => {
    let active = true;
    api<ModelSettingsResponse>("get_model_settings")
      .then((r) => {
        if (active) setSettings(r.settings);
      })
      .catch(() => {});
    api<Job[]>("get_runs", { project_id: project.id })
      .then((items) => {
        if (active) setRuns(items);
      })
      .catch((e) => {
        if (active) setError(message(e));
      });
    const running = sessionStorage.getItem(`quillweave-job-${project.id}`);
    if (running)
      api<Job>("get_job", { job_id: running })
        .then((value) => {
          if (active) setJob(value);
        })
        .catch(() => sessionStorage.removeItem(`quillweave-job-${project.id}`));
    return () => {
      active = false;
    };
  }, [project.id]);
  useEffect(() => {
    if (!job || job.status !== "running") return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    /** Poll the current task, clean up timers, and refresh knowledge after completion. */
    async function poll() {
      try {
        const current = await api<Job>("get_job", { job_id: job!.id });
        if (stopped) return;
        setJob(current);
        if (current.status === "running") timer = setTimeout(poll, 450);
        else {
          sessionStorage.removeItem(`quillweave-job-${project.id}`);
          setRuns((previous) => [
            current,
            ...previous.filter((r) => r.id !== current.id),
          ]);
          if (current.action === "wiki" && current.status === "done")
            await refreshRef.current();
        }
      } catch (e) {
        if (!stopped) {
          setError(message(e));
          timer = setTimeout(poll, 2000);
        }
      }
    }
    timer = setTimeout(poll, 200);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [job?.id, job?.status, project.id]);
  useEffect(() => {
    if (job?.status === "running")
      end.current?.scrollIntoView({ block: "nearest" });
  }, [job?.text]);

  /** Save current author edits before submitting the selected AI task. */
  async function start(
    preview = false,
    requestedAction = action,
    requestedInstruction = instruction,
  ) {
    setError("");
    setBusy(true);
    try {
      const saved = beforeStart ? await beforeStart() : project;
      if (!saved) return;
      const history: Message[] = conversationActions.includes(requestedAction)
        ? conversation.flatMap((r) => [
            { role: "user" as const, content: r.instruction },
            {
              role: "assistant" as const,
              content: r.text,
              source_job_id: r.id,
            },
          ])
        : [];
      const args = {
        project_id: saved.id,
        chapter_id: chapter?.id || "",
        source_id: source?.id || "",
        action: requestedAction,
        instruction:
          requestedAction === "wiki" ? "" : requestedInstruction.trim(),
        history,
        force: requestedAction === "wiki" && force,
      };
      if (preview) {
        setContext(await api<Prompt>("prepare_ai", args));
        return;
      }
      const next = await api<Job>("start_ai", args);
      setJob(next);
      sessionStorage.setItem(`quillweave-job-${project.id}`, next.id);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  /** Generate an outline using the same completed turns shown in the conversation. */
  function generateOutline() {
    setAction("outline");
    setInstruction(outlineInstruction);
    void start(false, "outline", outlineInstruction);
  }

  /** Save the current chapter and request a language-only rewrite for author review. */
  function humanizeChapter() {
    const request = t(humanizeInstruction);
    setAction("rewrite");
    setInstruction(request);
    void start(false, "rewrite", request);
  }

  /** Apply one proposal at a time and retain author drafts after validation errors. */
  async function applyResult() {
    if (!job || !onApply || applyingRef.current) return;
    applyingRef.current = true;
    setApplying(true);
    setError("");
    try {
      await onApply(job);
    } catch (e) {
      setError(message(e));
    } finally {
      applyingRef.current = false;
      setApplying(false);
    }
  }

  /** Request cancellation before a task enters the commit stage. */
  async function stop() {
    if (!job) return;
    setError("");
    try {
      const result = await api<{ cancelled: boolean }>("cancel_ai", {
        job_id: job.id,
      });
      if (!result.cancelled)
        setError(
          "The task has ended or entered the commit stage. Cancellation is unavailable.",
        );
    } catch (e) {
      setError(message(e));
    }
  }

  const running = job?.status === "running";
  const compatible = job?.chapter_id === (chapter?.id || "");
  return (
    <aside className="assistant-panel">
      <header className="panel-heading">
        <h3>
          <IconSparkles size={20} />
          {t("Writing assistant")}
        </h3>
        <button
          disabled={!!running}
          className="icon-button"
          title={t("Task history")}
          aria-label={t("Task history")}
          onClick={() => setShowHistory(true)}
        >
          <IconHistory size={19} />
        </button>
      </header>
      <div className="assistant-meta">
        <span className={`connection ${primary ? "connected" : ""}`} />
        <span>{t(model || "Model not configured")}</span>
        <button className="text-button" onClick={onSettings}>
          <IconSettings size={15} />
          {t("Settings")}
        </button>
      </div>
      <div
        className="assistant-messages"
        aria-live="polite"
        aria-busy={!!running}
      >
        {conversation
          .filter((run) => run.id !== job?.id)
          .map((run) => (
            <div className="conversation discussion-turn" key={run.id}>
              <div className="user-message">
                <span>
                  {t("Author ·")} {t(actions[run.action])}
                </span>
                <p>{run.instruction}</p>
              </div>
              <div className="answer-heading">
                <IconSparkles size={17} />
                <span>Quillweave</span>
              </div>
              <MarkdownResponse text={run.text} />
            </div>
          ))}
        {!job ? (
          <div className="assistant-welcome">
            <div className="soft-icon">
              <IconSparkles size={27} stroke={1.5} />
            </div>
            {exploratory ? (
              <>
                <p className="eyebrow">{t("Start wherever you are")}</p>
                <h2>{t("No idea yet? We can start there.")}</h2>
                <p>
                  {t(
                    "A mood, a character, something you enjoy — or simply wanting to write. We can discover the rest together.",
                  )}
                </p>
                <div className="suggestions">
                  {explorationSuggestions.map((item) => (
                    <Suggestion
                      key={item.label}
                      onClick={() => {
                        setAction("ask");
                        setInstruction(t(item.instruction));
                      }}
                    >
                      {t(item.label)}
                    </Suggestion>
                  ))}
                </div>
              </>
            ) : (
              <>
                <p className="eyebrow">{t("Write the next part")}</p>
                <h2>
                  {t("What happens")}
                  <br />
                  {t("next?")}
                </h2>
                <p>
                  {t("Explore a turn, discover a motivation,")}
                  <br />
                  {t("or put an idea into words.")}
                </p>
                <div className="suggestions">
                  <Suggestion
                    onClick={() => {
                      setAction("ask");
                      setInstruction(
                        "Suggest three directions for the current plot and explain the cost of each.",
                      );
                    }}
                  >
                    {t("Explore the next turn")}
                  </Suggestion>
                  {chapter ? (
                    <Suggestion
                      onClick={() => {
                        setAction("review");
                        setInstruction(
                          "Check whether the characters' actions fit their motivations, citing the source text.",
                        );
                      }}
                    >
                      {t("Review character motivation")}
                    </Suggestion>
                  ) : (
                    <Suggestion
                      onClick={() => {
                        setAction("architecture");
                        setInstruction(
                          "Use the current ideas to propose character goals, obstacles, and world rules.",
                        );
                      }}
                    >
                      {t("Develop characters and setting")}
                    </Suggestion>
                  )}
                  {chapter || source ? (
                    <Suggestion
                      onClick={() => {
                        setAction("wiki");
                        setInstruction("");
                      }}
                    >
                      {source
                        ? t("Compile this source into the Wiki")
                        : t("Track this chapter's characters and clues")}
                    </Suggestion>
                  ) : (
                    <Suggestion
                      onClick={() => {
                        setAction("outline");
                        setInstruction(
                          "Expand the current story setup into a chapter outline, with goals, obstacles, and consequences.",
                        );
                      }}
                    >
                      {t("Create a chapter outline")}
                    </Suggestion>
                  )}
                </div>
              </>
            )}
          </div>
        ) : (
          <div className="conversation">
            <div className="user-message">
              <span>
                {t("Author ·")} {t(actions[job.action])}
              </span>
              <p>
                {job.instruction ||
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
                {job.status === "running"
                  ? t("Generating")
                  : job.status === "done"
                    ? t("Complete")
                    : job.status === "cancelled"
                      ? t("Cancelled")
                      : t("Incomplete")}
              </Badge>
            </div>
            {job.action === "wiki" ? (
              <div className="wiki-result">
                <IconBook2 size={25} />
                <h3>{t(job.progress)}</h3>
                <p>
                  {t(
                    "Review each claim and its interpretation in the Character Wiki. Quotes link back to the source.",
                  )}
                </p>
                <details>
                  <summary>{t("View raw analysis")}</summary>
                  <pre>{job.text || t("Waiting for the model…")}</pre>
                </details>
              </div>
            ) : job.text ? (
              <MarkdownResponse
                text={job.text}
                streaming={running}
                incomplete={
                  job.status === "cancelled" || job.status === "error"
                }
              />
            ) : (
              <p className="answer-text">{t("Preparing context…")}</p>
            )}
            {job.error && <ErrorBox>{job.error}</ErrorBox>}
            {job.status === "done" && (
              <div className="answer-actions">
                {planning && compatible && job.action === "ask" && (
                  <Button
                    primary
                    disabled={busy || applying}
                    onClick={generateOutline}
                  >
                    {t("Generate outline from discussion")}
                  </Button>
                )}
                <Button
                  onClick={() =>
                    navigator.clipboard
                      .writeText(job.text)
                      .catch((e) => setError(message(e)))
                  }
                >
                  <IconCopy size={15} />
                  {t("Copy")}
                </Button>
                {onApply &&
                  compatible &&
                  (chapter
                    ? ["continue", "rewrite"].includes(job.action)
                    : planning &&
                      ["architecture", "outline"].includes(job.action)) && (
                    <Button primary disabled={applying} onClick={applyResult}>
                      {job.action === "continue"
                        ? t("Append to chapter")
                        : job.action === "rewrite"
                          ? t("Replace chapter draft")
                          : job.action === "outline"
                            ? t("Review outline")
                            : t("Apply to story setup")}
                    </Button>
                  )}
              </div>
            )}
            {job.prompt.context.length > 0 && (
              <button
                className="context-link"
                onClick={() => setContext(job.prompt)}
              >
                {t("Used")} {job.prompt.context.length} {t("source excerpts")}{" "}
                <IconChevronDown size={14} />
              </button>
            )}
            <div ref={end} />
          </div>
        )}
      </div>
      <div className="assistant-composer">
        {action === "wiki" && (
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={force}
              disabled={busy || !!running}
              onChange={(event) => setForce(event.target.checked)}
            />
            {t("Recompile using the model")}
          </label>
        )}
        {error && <ErrorBox>{error}</ErrorBox>}
        {source && (
          <p className="composer-note">
            {t("Source:")}
            {source.title}
          </p>
        )}
        <div className="composer-toolbar">
          <select
            aria-label={t("AI action")}
            value={action}
            onChange={(e) => setAction(e.target.value)}
            disabled={!!running}
          >
            {Object.entries(actions)
              .filter(([key]) => availableActions.includes(key))
              .map(([key, label]) => (
                <option key={key} value={key}>
                  {t(label)}
                </option>
              ))}
          </select>
          <button
            className="text-button"
            disabled={busy || !!running || action === "wiki"}
            onClick={() => start(true)}
          >
            {t("Preview context")}
          </button>
        </div>
        {chapter && onApply && (
          <div className="prose-polish">
            <Button
              disabled={busy || !!running || applying || !canHumanize}
              onClick={humanizeChapter}
            >
              <IconSparkles size={16} />
              {t("Humanize chapter")}
            </Button>
            <p className="composer-note">{t("Keeps the plot and character voices. Review before applying.")}</p>
          </div>
        )}
        {planning && (
          <p className="composer-note">
            {t(
              exploratory
                ? "Nothing needs to be decided before we talk. Ideas stay as proposals until you choose to save them as story details."
                : "Discuss your story, generate an outline, then review and apply it. The last three completed turns are included in the context.",
            )}
          </p>
        )}
        <textarea
          aria-label={t("Writing request")}
          placeholder={
            action === "wiki"
              ? t(
                  "Compile the source using this project's tracking focus. You can edit the focus in Story memory.",
                )
              : exploratory
                ? t(
                    "You can start with: I want to write, but I don't know what yet…",
                  )
                : t(
                    "Describe your idea or the writing question you want to explore…",
                  )
          }
          value={action === "wiki" ? "" : instruction}
          onChange={(e) => setInstruction(e.target.value)}
          maxLength={8000}
          disabled={!!running || action === "wiki"}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              if (!busy && !running) void start();
            }
          }}
        />
        <div className="composer-footer">
          <span>{action === "wiki" ? 0 : instruction.length} / 8,000</span>
          {running ? (
            <Button onClick={stop}>
              <IconPlayerStop size={17} />
              {t("Stop")}
            </Button>
          ) : (
            <Button
              primary
              disabled={busy || (action !== "wiki" && !instruction.trim())}
              onClick={() => start()}
            >
              <IconArrowUp size={18} />
              {busy ? t("Preparing") : t("Send")}
            </Button>
          )}
        </div>
        <p className="composer-note">
          {t(
            "You review and decide. Content is sent to the model you configure.",
          )}
        </p>
      </div>
      {context && (
        <Modal
          title={t("Request context")}
          onClose={() => setContext(null)}
          wide
        >
          <div className="modal-content">
            <p className="muted">
              {t("Total")} {context.chars.toLocaleString()}{" "}
              {t(
                "characters, including the story bible, current chapter, and retrieved evidence below.",
              )}
            </p>
            {context.context.map((item) => (
              <details className="context-item" key={item.id}>
                <summary>
                  {item.title}
                  <small>{item.id}</small>
                </summary>
                <p className="preserve">{item.text}</p>
              </details>
            ))}
            <details className="context-item">
              <summary>{t("View the outgoing prompt")}</summary>
              <pre>
                {context.messages
                  .map((m) => `${m.role}\n${m.content}`)
                  .join("\n\n")}
              </pre>
            </details>
          </div>
        </Modal>
      )}
      {showHistory && (
        <Modal
          title={t("AI task history")}
          onClose={() => setShowHistory(false)}
        >
          <div className="modal-content">
            {!runs.length && (
              <p className="muted">
                {t("Completed tasks are stored locally.")}
              </p>
            )}
            {runs.map((run) => (
              <button
                className="history-row"
                key={run.id}
                onClick={() => {
                  setJob(run);
                  setShowHistory(false);
                }}
              >
                <div>
                  <strong>{t(actions[run.action])}</strong>
                  <p>{run.instruction || t(run.progress)}</p>
                </div>
                <Badge>
                  {run.status === "done" ? t("Complete") : t("Incomplete")}
                </Badge>
              </button>
            ))}
          </div>
        </Modal>
      )}
    </aside>
  );
}
