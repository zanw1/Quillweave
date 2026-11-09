import { t } from "../../lib/i18n";
import {
  conversationJobs,
  localDraft,
  saveLocalDraft,
  useConversations,
} from "./conversations";
import { ConversationTools } from "./ConversationTools";
import { ProposalCards } from "./ProposalCards";
import { QuickIntentBar } from "./QuickIntentBar";
import { ChangeReviewCard } from "./ChangeReviewCard";
import { AssistantModals, ExtractChangeSetButton } from "./AssistantSidebar";
import { visibleAssistantText } from "./visibleText";
import { ContextChips } from "./ContextChips";
import {
  emptyOmissions,
  omissionFields,
  omitChip,
  turnChips,
  type TurnOmissions,
} from "./turnContext";
import { useTurnMeasure } from "./useTurnMeasure";
import {
  conversationActions,
  explorationSuggestions,
  humanizeInstruction,
  outlineInstruction,
} from "./assistantPrompts";
import {
  ResizeHandle,
  composerSizeBounds,
  useStoredSize,
} from "./resize";
import { SearchRequestChoice } from "../search/SearchRequestChoice";
import { searchSelection } from "../../domain/search";
import { useEffect, useRef, useState, type CSSProperties } from "react";
import {
  IconSparkles,
  IconArrowUp,
  IconPlayerStop,
  IconCopy,
  IconSettings,
  IconHistory,
  IconChevronDown,
  IconBook2,
  IconX,
} from "@tabler/icons-react";
import { api, message } from "../../lib/api";
import { selectedProfile } from "../settings/models";
import {
  Badge,
  Button,
  ErrorBox,
  Suggestion,
  MarkdownResponse,
} from "../../components/ui";
import {
  actions,
  type ChangeSet,
  type Chapter,
  type Job,
  type Message,
  type Project,
  type Prompt,
  type Source,
  type ModelSettings,
  type ModelSettingsResponse,
  type WorkspaceContext,
} from "../../domain/types";

interface Props {
  project: Project;
  chapter?: Chapter;
  source?: Source;
  initialAction?: string;
  exploring?: boolean;
  canHumanize?: boolean;
  seed?: string;
  seedAction?: string;
  seedIntent?: string;
  seedVersion?: number;
  onSeedConsumed?: () => void;
  onSettings: () => void;
  onRefresh: () => Promise<void>;
  beforeStart?: () => Promise<Project | null>;
  onApply?: (job: Job) => Promise<void>;
  global?: boolean;
  workspaceContext?: WorkspaceContext;
  onOpenTarget?: (kind: string, id: string) => void;
  onClose?: () => void;
  onClearSource?: () => void;
}

/** Stream proposals into the conversation for explicit author review. */
export function Assistant({
  project,
  chapter,
  source,
  initialAction = "agent",
  exploring = false,
  canHumanize = !!chapter?.body.trim(),
  seed = "",
  seedAction = "agent",
  seedIntent = "",
  seedVersion = 0,
  onSeedConsumed,
  onSettings,
  onRefresh,
  beforeStart,
  onApply,
  global: globalScope = false,
  workspaceContext,
  onOpenTarget,
  onClose,
  onClearSource,
}: Props) {
  const scopeId = globalScope ? "" : chapter?.id || source?.id || "";
  const [instruction, setInstruction] = useState(() =>
    localDraft(
      `quillweave-chat-draft-${project.id}-${scopeId || "plan"}-new`,
      seed,
    ),
  );
  const [action, setAction] = useState(initialAction);
  const [intentHint, setIntentHint] = useState("");
  const [omissions, setOmissions] = useState<TurnOmissions>(emptyOmissions);
  const [extraChangeSets, setExtraChangeSets] = useState<ChangeSet[]>([]);
  const [searchChoice, setSearchChoice] = useState("inherit");
  const [job, setJob] = useState<Job | null>(null);
  const [runs, setRuns] = useState<Job[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [applying, setApplying] = useState(false);
  const applyingRef = useRef(false);
  const [force, setForce] = useState(false);
  const [context, setContext] = useState<Prompt | null>(null);
  const [showHistory, setShowHistory] = useState(false);
  const composerRef = useRef<HTMLDivElement>(null);
  const [composerHeight, setComposerHeight] = useStoredSize(
    "quillweave-composer-height",
  );
  const [settings, setSettings] = useState<ModelSettings | null>(null);
  const manager = useConversations(
    project.id,
    scopeId,
    (draft, restored, restoreDraft = true) => {
      if (restoreDraft) setInstruction(draft);
      setJob(restored);
    },
  );
  const managerRef = useRef(manager);
  managerRef.current = manager;
  const instructionRef = useRef(instruction);
  instructionRef.current = instruction;
  const draftKeyRef = useRef(manager.draftKey);
  draftKeyRef.current = manager.draftKey;
  const requestId = useRef("");
  const requestSnapshot = useRef("");
  const [sessionError, setSessionError] = useState("");
  const planning = !chapter && !source;
  const exploratory = planning && exploring;
  const conversation = conversationJobs(manager.session, runs);
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
  const windowChars = primary?.config.context_chars || 24000;
  const chips = turnChips(project, workspaceContext, chapter, source, omissions);
  const model = primary
    ? `${primary.name} · ${primary.config.model}${action === "wiki" ? ` → ${secondary?.name || t("Memory model unassigned")}` : ""}`
    : "No model assigned to this task";
  const end = useRef<HTMLDivElement>(null);
  const refreshRef = useRef(onRefresh);
  refreshRef.current = onRefresh;

  useEffect(() => {
    if (manager.loading) return;
    saveLocalDraft(manager.draftKey, instruction);
    const id = manager.session?.id;
    const timer = setTimeout(() => {
      if (id)
        void managerRef.current.persistDraft(instruction, id).catch(() => {});
    }, 700);
    return () => clearTimeout(timer);
  }, [instruction, manager.draftKey, manager.loading, manager.session?.id]);
  useEffect(() => {
    if (job && job.status !== "running")
      void managerRef.current
        .reload()
        .catch((error) => setSessionError(message(error)));
  }, [job?.id, job?.status]);

  useEffect(() => {
    if (seed && !manager.loading) {
      setInstruction(seed);
      setAction(seedAction);
      setIntentHint(seedIntent);
      onSeedConsumed?.();
    }
  }, [seed, seedAction, seedIntent, seedVersion, manager.loading]);
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
          if (current.status === "done") await refreshRef.current();
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
  const turnChars = useTurnMeasure({
    enabled: action !== "wiki",
    projectId: project.id,
    chapterId: chapter?.id || "",
    sourceId: source?.id || "",
    action,
    instruction,
    intent: intentHint,
    sessionId: manager.session?.id || "",
    context: workspaceContext,
    omissions,
  });
  useEffect(() => {
    if (job?.status === "running")
      end.current?.scrollIntoView({ block: "nearest" });
  }, [job?.text]);

  /** Save current author edits before submitting the selected AI task. */
  async function start(
    preview = false,
    requestedAction = action,
    requestedInstruction = instruction,
    resumeJobId = "",
    extractionJobId = "",
  ) {
    if (
      !preview &&
      (busy ||
        job?.status === "running" ||
        (requestedAction !== "wiki" && !requestedInstruction.trim()))
    )
      return;
    if (
      !preview &&
      requestedAction !== "wiki" &&
      (requestedInstruction.trim().length > windowChars ||
        (turnChars != null && turnChars > windowChars))
    ) {
      setError(
        t(
          "This turn exceeds the model context window. The text you typed is kept.",
        ),
      );
      return;
    }
    setError("");
    setBusy(true);
    try {
      const saved = beforeStart ? await beforeStart() : project;
      if (!saved) return;
      const session = preview ? manager.session : await manager.ensure();
      const snapshot = JSON.stringify({
        session: session?.id,
        revision: saved.revision,
        action: requestedAction,
        chapter: chapter?.id,
        source: source?.id,
        workspace: workspaceContext,
        omissions,
        intent: intentHint,
        resume: resumeJobId,
        instruction: requestedInstruction.trim(),
        search: searchChoice,
        extraction: extractionJobId,
        force: requestedAction === "wiki" && force,
      });
      if (requestSnapshot.current !== snapshot) {
        requestSnapshot.current = snapshot;
        requestId.current = crypto.randomUUID();
      }
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
        intent_hint: intentHint || undefined,
        extraction_job_id: extractionJobId,
        workspace_context: {
          ...workspaceContext,
          active_chapter_id: chapter?.id || undefined,
          ...omissionFields(omissions),
        },
        history,
        session_id: session?.id || "",
        request_id: requestId.current,
        resume_job_id: resumeJobId,
        force: requestedAction === "wiki" && force,
        search_selection: searchSelection(searchChoice),
      };
      if (preview) {
        setContext(await api<Prompt>("prepare_ai", args));
        return;
      }
      const next = await api<Job>("start_ai", args);
      requestId.current = "";
      requestSnapshot.current = "";
      setIntentHint("");
      setOmissions(emptyOmissions());
      if (globalScope) setAction("agent");
      setExtraChangeSets([]);
      setJob(next);
      const clearSent =
        instructionRef.current.trim() === requestedInstruction.trim() ||
        !instructionRef.current.trim();
      if (clearSent) setInstruction("");
      saveLocalDraft(
        draftKeyRef.current,
        clearSent ? "" : instructionRef.current,
      );
      saveLocalDraft(
        `quillweave-chat-draft-${project.id}-${scopeId || "plan"}-new`,
        "",
      );
      await managerRef.current
        .reload(session?.id)
        .catch((error) => setSessionError(message(error)));
    } catch (e) {
      if (!preview && !instructionRef.current.trim()) {
        setInstruction(requestedInstruction);
        saveLocalDraft(draftKeyRef.current, requestedInstruction);
      }
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

  /** Resume an interrupted agent in its conversation or branch an author-selected message for editing. */
  async function branchAt(jobId: string, regenerate = false, resume = false) {
    setSessionError("");
    try {
      const turn = manager.session?.turns.find(
        (turn) =>
          turn.role === "user" && (turn.job_id === jobId || turn.id === jobId),
      );
      if (!turn) throw new Error("Select a user message to rewind or edit");
      if (resume && turn.action === "agent") {
        await start(false, "agent", turn.content, jobId);
        return;
      }
      await manager.fork(turn.id);
      if (regenerate) {
        setAction(turn.action);
        setInstruction(turn.content);
        await start(false, turn.action, turn.content, resume ? jobId : "");
      }
    } catch (error) {
      setSessionError(message(error));
    }
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
  const answer = visibleAssistantText(job?.text || "");
  const compatible = job?.chapter_id === (chapter?.id || "");
  return (
    <aside
      className="assistant-panel"
      style={
        composerHeight == null
          ? undefined
          : ({ "--composer-height": `${composerHeight}px` } as CSSProperties)
      }
    >
      <header className="panel-heading">
        <h3>
          <IconSparkles size={20} />
          {t(globalScope ? "Creative partner" : "Writing assistant")}
        </h3>
        <small className="assistant-current-model">{t(model)}</small>
        <button
          className="icon-button assistant-inline-settings"
          title={t("Settings")}
          aria-label={t("Settings")}
          onClick={onSettings}
        >
          <IconSettings size={17} />
        </button>
        <button
          disabled={!!running}
          className="icon-button"
          title={t("Task history")}
          aria-label={t("Task history")}
          onClick={() => setShowHistory(true)}
        >
          <IconHistory size={19} />
        </button>
        {onClose && (
          <button
            className="icon-button"
            aria-label={t("Collapse writing assistant")}
            onClick={onClose}
          >
            <IconX size={18} />
          </button>
        )}
      </header>
      {workspaceContext && (
        <div className="workspace-focus">
          <span>
            {t(
              {
                setup: "Workspace overview",
                world: "Story settings",
                plan: "Outline",
                write: "Manuscript",
              }[workspaceContext.active_step || "setup"] ||
                "Workspace overview",
            )}
          </span>
          {chapter && <span>· {chapter.title}</span>}
        </div>
      )}
      <ConversationTools manager={manager} disabled={!!running || busy} />
      {sessionError && <ErrorBox>{sessionError}</ErrorBox>}
      <div
        className="assistant-messages"
        aria-live="polite"
        aria-busy={!!running}
      >
        {conversation
          .filter((run) => run.id !== job?.id)
          .map((run) => (
            <div
              className="conversation discussion-turn"
              key={run.id}
              data-job-id={run.id}
            >
              <div className="user-message">
                <span>
                  {t("Author ·")} {t(actions[run.action])}
                </span>
                <p>{run.instruction}</p>
                <button
                  className="text-button"
                  disabled={!!running || busy}
                  onClick={() => void branchAt(run.id)}
                >
                  {t("Edit and branch")}
                </button>
                <button
                  className="text-button"
                  disabled={!!running || busy}
                  onClick={() => void branchAt(run.id, true)}
                >
                  {t("Regenerate")}
                </button>
              </div>
              <div className="answer-heading">
                <IconSparkles size={17} />
                <span>Quillweave</span>
              </div>
              <MarkdownResponse text={visibleAssistantText(run.text)} />
            </div>
          ))}
        {!job ? (
          <div className="assistant-welcome">
            <h2>
              {t(
                exploratory
                  ? "No idea yet? We can start there."
                  : "What would you like to work on?",
              )}
            </h2>
            <p>
              {t(
                "Discuss your story here. Review proposed changes before saving.",
              )}
            </p>
            <div className="suggestions">
              {explorationSuggestions.map((item) => (
                <Suggestion
                  key={item.label}
                  onClick={() => {
                    setAction("agent");
                    setInstruction(t(item.instruction));
                  }}
                >
                  {t(item.label)}
                </Suggestion>
              ))}
            </div>
          </div>
        ) : (
          <div className="conversation" data-job-id={job.id}>
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
                  <pre>{answer || t("Waiting for the model…")}</pre>
                </details>
              </div>
            ) : answer ? (
              <MarkdownResponse
                text={answer}
                streaming={running}
                incomplete={
                  job.status === "cancelled" || job.status === "error"
                }
              />
            ) : (
              <p className="answer-text">
                {t(
                  job.status === "running"
                    ? "Preparing context…"
                    : job.changesets?.length
                      ? "Review changes"
                      : "This task ended before returning text.",
                )}
              </p>
            )}
            {job.error && <ErrorBox>{job.error}</ErrorBox>}
            {job.status === "done" && (
              <div className="answer-actions">
                {planning &&
                  compatible &&
                  project.studio?.stage === "planning" &&
                  job.action === "ask" && (
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
                      .writeText(answer)
                      .catch((e) => setError(message(e)))
                  }
                >
                  <IconCopy size={15} />
                  {t("Copy")}
                </Button>
                {answer && !job.changesets?.length && (
                  <ExtractChangeSetButton
                    projectId={project.id}
                    sessionId={manager.session?.id || ""}
                    text={answer}
                    disabled={busy || applying}
                    onExtracted={(cs) =>
                      setExtraChangeSets((prev) => [
                        cs,
                        ...prev.filter((item) => item.id !== cs.id),
                      ])
                    }
                    onError={setError}
                    onRequest={() =>
                      start(
                        false,
                        "agent",
                        t(
                          "Prepare structured changes from this discussion for review.",
                        ),
                        "",
                        job.id,
                      )
                    }
                  />
                )}
                {onApply &&
                  !job.changesets?.length &&
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
            <ChangeReviewCard
              project={project}
              changesets={
                job.status === "running"
                  ? []
                  : [...(job.changesets || []), ...extraChangeSets]
              }
              onRefresh={onRefresh}
              onOpenTarget={onOpenTarget}
              beforeConfirm={beforeStart}
              onChangeSetUpdated={(updated) => {
                setJob((prev) =>
                  prev
                    ? {
                        ...prev,
                        changesets: prev.changesets?.map((c) =>
                          c.id === updated.id ? updated : c,
                        ),
                      }
                    : prev,
                );
                setExtraChangeSets((prev) =>
                  prev.map((c) => (c.id === updated.id ? updated : c)),
                );
              }}
            />
            <ProposalCards
              job={job}
              project={project}
              onJob={setJob}
              onRefresh={onRefresh}
              beforeReview={beforeStart}
            />
            {answer &&
              ["cancelled", "error", "interrupted"].includes(job.status) &&
              !["agent", "wiki", "memory_update"].includes(job.action) && (
                <Button
                  disabled={busy}
                  onClick={() => void branchAt(job.id, true, true)}
                >
                  {t("Continue interrupted response")}
                </Button>
              )}
            {job.status !== "running" && job.action !== "wiki" && (
              <div className="answer-actions">
                <Button disabled={busy} onClick={() => void branchAt(job.id)}>
                  {t("Rewind here")}
                </Button>
                <Button
                  disabled={busy}
                  onClick={() => void branchAt(job.id, true)}
                >
                  {t(job.status === "done" ? "Regenerate" : "Retry")}
                </Button>
                {job.action === "agent" &&
                  ["cancelled", "error", "interrupted"].includes(
                    job.status,
                  ) && (
                    <Button
                      disabled={busy || !!running || manager.loading}
                      onClick={() => void branchAt(job.id, true, true)}
                    >
                      {t("Resume task")}
                    </Button>
                  )}
              </div>
            )}
            <div ref={end} />
          </div>
        )}
      </div>
      <ResizeHandle
        axis="y"
        label={t("Drag to resize the composer")}
        resetLabel={t("Double-click to reset the size")}
        value={composerHeight}
        onChange={setComposerHeight}
        bounds={composerSizeBounds}
        target={composerRef}
      />
      <div
        className={`assistant-composer${composerHeight == null ? "" : " is-resized"}`}
        ref={composerRef}
      >
        <QuickIntentBar
          intentHint={intentHint}
          disabled={busy || !!running}
          hasChapter={!!chapter}
          onSelectIntent={(hint, defaultPrompt) => {
            setIntentHint(hint);
            setAction("agent");
            if (defaultPrompt && !instruction.trim()) {
              setInstruction(defaultPrompt);
            }
          }}
        />
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
            {onClearSource && (
              <button className="text-button" onClick={onClearSource}>
                {t("Clear")}
              </button>
            )}
          </p>
        )}
        <div className="composer-toolbar">
          {action === "agent" && (
            <SearchRequestChoice
              value={searchChoice}
              onChange={setSearchChoice}
              disabled={busy || !!running}
            />
          )}
          <button
            className="text-button"
            disabled={busy || !!running || action === "wiki"}
            onClick={() => start(true)}
          >
            {t("Preview context")}
          </button>
        </div>
        {chapter && (
          <div className="prose-polish">
            <Button
              disabled={busy || !!running || applying || !canHumanize}
              onClick={humanizeChapter}
            >
              <IconSparkles size={16} />
              {t("Humanize chapter")}
            </Button>
            <p className="composer-note">
              {t(
                "Keeps the plot and character voices. Review before applying.",
              )}
            </p>
          </div>
        )}
        {planning && !conversation.length && !job && (
          <p className="composer-note">
            {t(
              exploratory
                ? "Nothing needs to be decided before we talk. Ideas stay as proposals until you choose to save them as story details."
                : "Start with an idea. Decide when you are ready to outline.",
            )}
          </p>
        )}
        <ContextChips
          chips={chips}
          onRemove={(id) => setOmissions((current) => omitChip(current, id))}
        />
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
          disabled={action === "wiki" || manager.loading}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing || e.keyCode === 229) return;
            if (e.key === "ArrowUp" && !instruction) {
              const prior = [...(manager.session?.turns || [])]
                .reverse()
                .find((turn) => turn.role === "user");
              if (prior) {
                e.preventDefault();
                setInstruction(prior.content);
              }
            }
            if (e.key === "Enter" && (!e.shiftKey || e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              if (!busy && !running) void start();
            }
          }}
        />
        <div className="composer-footer">
          <span className={turnChars != null && turnChars > windowChars ? "is-over" : undefined}>
            {(turnChars ?? instruction.length).toLocaleString()} /{" "}
            {windowChars.toLocaleString()}
          </span>
          {running ? (
            <Button onClick={stop}>
              <IconPlayerStop size={17} />
              {t("Stop")}
            </Button>
          ) : (
            <Button
              primary
              disabled={
                busy ||
                manager.loading ||
                (action !== "wiki" && !instruction.trim())
              }
              onClick={() => start()}
            >
              <IconArrowUp size={18} />
              {busy ? t("Preparing") : t("Send")}
            </Button>
          )}
        </div>
        <p className="composer-note keyboard-hint">
          {t("Enter to send · Shift+Enter for a new line")} ·{" "}
          {t("Draft saved locally · Selected context is sent to your model")}
        </p>
      </div>
      <AssistantModals
        context={context}
        showHistory={showHistory}
        runs={runs}
        onCloseContext={() => setContext(null)}
        onCloseHistory={() => setShowHistory(false)}
        onSelectRun={(run) => setJob(run)}
      />
    </aside>
  );
}
