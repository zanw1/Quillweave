import { useEffect, useRef, useState, type CSSProperties } from "react";
import {
  IconHistory,
  IconSettings,
  IconSparkles,
  IconX,
} from "@tabler/icons-react";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import { Button, ErrorBox } from "../../components/ui";
import { searchSelection } from "../../domain/search";
import type {
  Chapter,
  Job,
  Message,
  Project,
  Prompt,
  Source,
  WorkspaceContext,
  WritingFollowup,
} from "../../domain/types";
import { AssistantModals } from "./AssistantSidebar";
import { AssistantComposer } from "./AssistantComposer";
import { AssistantTimeline } from "./AssistantTimeline";
import { ConversationTools } from "./ConversationTools";
import { ChatSearchModal, ChatSkillsModal } from "./ChatToolsModal";
import { chatCommands, completeChatCommand, parseChatCommand, taskCommitPolicy } from "./chatCommands";
import {
  conversationJobs,
  localDraft,
  saveLocalDraft,
  useConversations,
} from "./conversations";
import { conversationActions, humanizeInstruction } from "./assistantPrompts";
import {
  emptyOmissions,
  omissionFields,
  omitChip,
  turnChips,
  type TurnOmissions,
} from "./turnContext";
import { ResizeHandle, composerSizeBounds, useStoredSize } from "./resize";
import { useChatOptions } from "./useChatOptions";
import { useTurnMeasure } from "./useTurnMeasure";
import "./chat.css";

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

/** Coordinate conversation-owned drafts and immutable submissions through the existing native/HTTP API. */
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
      seed || (initialAction === "wiki" ? "/wiki" : ""),
    ),
  );
  const [action, setAction] = useState(initialAction);
  const [intentHint, setIntentHint] = useState("");
  const [omissions, setOmissions] = useState<TurnOmissions>(emptyOmissions);
  const [job, setJob] = useState<Job | null>(null);
  const [runs, setRuns] = useState<Job[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [sessionError, setSessionError] = useState("");
  const [context, setContext] = useState<Prompt | null>(null);
  const [showHistory, setShowHistory] = useState(false);
  const [modelOpen, setModelOpen] = useState(false);
  const [toolModal, setToolModal] = useState<"search" | "skills" | null>(null);
  const composerRef = useRef<HTMLDivElement>(null);
  const [composerHeight, setComposerHeight] = useStoredSize(
    "quillweave-composer-height",
  );
  const manager = useConversations(
    project.id,
    scopeId,
    (draft, restored, restoreDraft = true) => {
      if (restoreDraft) setInstruction(draft);
      setJob(restored);
    },
  );
  const options = useChatOptions(project, manager.session, manager.draftKey);
  const managerRef = useRef(manager);
  managerRef.current = manager;
  const inputRef = useRef(instruction);
  inputRef.current = instruction;
  const jobRef = useRef(job);
  jobRef.current = job;
  const refreshRef = useRef(onRefresh);
  refreshRef.current = onRefresh;
  const requestId = useRef("");
  const requestSnapshot = useRef("");
  const sending = useRef(false);
  const wikiDraftInitialized = useRef(false);
  const activeSession = manager.session?.id || "";
  const conversation = conversationJobs(manager.session, runs);
  const visibleJob = job && job.session_id === activeSession ? job : null;
  const timeline =
    visibleJob && !conversation.some((run) => run.id === visibleJob.id)
      ? [...conversation, visibleJob]
      : conversation.map((run) =>
          run.id === visibleJob?.id ? visibleJob : run,
        );
  const projectRunning =
    runs.find((run) => run.status === "running") ||
    (job?.status === "running" ? job : null);
  const running = visibleJob?.status === "running";
  const measured = useTurnMeasure({
    enabled: action !== "wiki" && !manager.loading,
    projectId: project.id,
    chapterId: chapter?.id || "",
    sourceId: source?.id || "",
    action,
    instruction,
    intent: intentHint,
    sessionId: activeSession,
    context: workspaceContext,
    omissions,
    selection: options.selection,
    skills: options.skills,
    searchSelection: searchSelection(options.search),
    commitPolicy: options.commitPolicy === "task"
      ? taskCommitPolicy(action, intentHint)
      : options.commitPolicy,
  });
  const chips =
    measured?.carried_records ||
    turnChips(project, workspaceContext, chapter, source, omissions);
  const primary = options.settings?.profiles.find(
    (profile) => profile.id === options.selection?.profile_id,
  );

  /** Merge snapshots by stable job identity without replacing another viewed conversation's response. */
  function updateJob(current: Job) {
    setRuns((previous) => [
      current,
      ...previous.filter((item) => item.id !== current.id),
    ]);
    setJob((previous) => (previous?.id === current.id ? current : previous));
  }

  useEffect(() => {
    if (manager.loading) return;
    const key = manager.draftKey;
    const id = manager.session?.id;
    saveLocalDraft(key, instruction);
    const timer = setTimeout(() => {
      if (id)
        void managerRef.current.persistDraft(instruction, id).catch(() => {});
    }, 700);
    return () => clearTimeout(timer);
  }, [instruction, manager.draftKey, manager.loading, manager.session?.id]);

  useEffect(() => {
    if (
      job &&
      job.status !== "running" &&
      job.session_id === managerRef.current.session?.id
    )
      void managerRef.current
        .reload()
        .catch((error) => setSessionError(message(error)));
  }, [job?.id, job?.status]);

  useEffect(() => {
    setIntentHint("");
    setOmissions(emptyOmissions());
    setAction(initialAction === "wiki" ? "wiki" : "agent");
    setError("");
  }, [activeSession, project.id]);

  useEffect(() => {
    if (manager.loading || wikiDraftInitialized.current) return;
    wikiDraftInitialized.current = true;
    if (initialAction === "wiki" && !inputRef.current.trim())
      setInstruction("/wiki");
  }, [manager.loading, initialAction]);

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
    setRuns([]);
    api<Job[]>("get_runs", { project_id: project.id })
      .then((items) => {
        if (active) setRuns(items);
      })
      .catch((error) => {
        if (active) setError(message(error));
      });
    return () => {
      active = false;
    };
  }, [project.id]);

  const runningIds = [
    ...new Set([
      ...runs.filter((run) => run.status === "running").map((run) => run.id),
      ...(job?.status === "running" ? [job.id] : []),
    ]),
  ]
    .sort()
    .join(",");
  useEffect(() => {
    if (!runningIds) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    /** Poll active executions independently of the currently viewed conversation. */
    async function poll() {
      try {
        const snapshots = await Promise.all(
          runningIds
            .split(",")
            .map((id) => api<Job>("get_job", { job_id: id })),
        );
        if (stopped) return;
        for (const current of snapshots) {
          updateJob(current);
          if (current.status !== "running") {
            if (current.status === "done") await refreshRef.current();
            if (current.session_id === managerRef.current.session?.id)
              await managerRef.current.reload();
          }
        }
        if (!stopped) timer = setTimeout(poll, 350);
      } catch (error) {
        if (!stopped) {
          setError(message(error));
          timer = setTimeout(poll, 1800);
        }
      }
    }
    timer = setTimeout(poll, 150);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [runningIds, project.id]);

  /** Freeze the selected model, skill catalog identities and context before admitting a task. */
  async function start(
    preview = false,
    requestedAction = action,
    requestedInstruction = instruction,
    resumeJobId = "",
    extractionJobId = "",
    compact = false,
    requestedIntent = intentHint,
    force = false,
  ) {
    if (
      !preview &&
      (sending.current ||
        projectRunning ||
        (requestedAction !== "wiki" && !requestedInstruction.trim()))
    )
      return;
    const capturedSelection = options.selection
      ? { ...options.selection }
      : undefined;
    const capturedSkills = [...options.skills];
    const capturedSearch = options.search;
    const capturedCommitPolicy = options.commitPolicy === "task"
      ? taskCommitPolicy(requestedAction, requestedIntent)
      : options.commitPolicy;
    const capturedDraftKey = manager.draftKey;
    const owner = managerRef.current.selected()?.id;
    setError("");
    setBusy(true);
    if (!preview) sending.current = true;
    try {
      const saved = beforeStart ? await beforeStart() : project;
      if (!saved) return;
      if (owner !== managerRef.current.selected()?.id)
        throw new Error(
          "Conversation changed while preparing the request. Your draft is kept.",
        );
      const session = preview
        ? managerRef.current.selected()
        : await managerRef.current.ensure();
      const snapshot = JSON.stringify({
        session: session?.id,
        revision: saved.revision,
        action: requestedAction,
        chapter: chapter?.id,
        source: source?.id,
        workspace: workspaceContext,
        omissions,
        intent: requestedIntent,
        resume: resumeJobId,
        instruction: requestedInstruction.trim(),
        search: capturedSearch,
        extraction: extractionJobId,
        model: capturedSelection,
        skills: capturedSkills,
        commitPolicy: capturedCommitPolicy,
        compact,
        force,
      });
      if (requestSnapshot.current !== snapshot) {
        requestSnapshot.current = snapshot;
        requestId.current = crypto.randomUUID();
      }
      const history: Message[] = conversationActions.includes(requestedAction)
        ? conversation.flatMap((run) => [
            { role: "user" as const, content: run.instruction },
            {
              role: "assistant" as const,
              content: run.text,
              source_job_id: run.id,
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
        intent_hint: requestedIntent || undefined,
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
        search_selection: searchSelection(capturedSearch),
        model_selection: capturedSelection,
        skill_ids: capturedSkills,
        commit_policy: capturedCommitPolicy,
        compact,
      };
      const prepared = await api<Prompt>("prepare_ai", args);
      if (preview) {
        setContext(prepared);
        return;
      }
      if (prepared.budget && !prepared.budget.can_send)
        throw new Error(
          prepared.budget.warning ||
            t(
              "This turn exceeds the model context window. The text you typed is kept.",
            ),
        );
      if (session?.id !== managerRef.current.selected()?.id)
        throw new Error(
          "Conversation changed while preparing the request. Your draft is kept.",
        );
      const next = await api<Job>("start_ai", args);
      requestId.current = "";
      requestSnapshot.current = "";
      setIntentHint("");
      setOmissions(emptyOmissions());
      setAction("agent");
      setRuns((previous) => [
        next,
        ...previous.filter((run) => run.id !== next.id),
      ]);
      if (managerRef.current.selected()?.id === session?.id) {
        setJob(next);
        if (inputRef.current.trim() === instruction.trim()) {
          setInstruction("");
          saveLocalDraft(managerRef.current.draftKey, "");
        }
        options.update({ search: "inherit" });
        await managerRef.current.reload(session?.id);
      }
      if (localDraft(capturedDraftKey).trim() === instruction.trim())
        saveLocalDraft(capturedDraftKey, "");
    } catch (error) {
      setError(message(error));
    } finally {
      sending.current = false;
      setBusy(false);
    }
  }

  /** Locate the selected response's owning session before any branch, retry or resume operation. */
  async function branchAt(
    run: Job,
    regenerate = false,
    resume = false,
    followup?: WritingFollowup,
  ) {
    setSessionError("");
    try {
      const session =
        managerRef.current.selected()?.id === run.session_id
          ? managerRef.current.selected()
          : await managerRef.current.select(run.session_id || "");
      const turn = session?.turns.find(
        (turn) =>
          turn.role === "user" &&
          (turn.job_id === run.id || turn.id === run.id),
      );
      if (!turn) throw new Error("Select a user message to rewind or edit");
      if (resume && run.action === "agent") {
        await start(false, "agent", turn.content, run.id);
        return;
      }
      if (regenerate) {
        // Retry regenerates in place: the turn and its response are removed from this
        // conversation, then re-sent, instead of forking a child session.
        await managerRef.current.truncate(turn.id);
        await start(
          false,
          followup?.action || turn.action,
          followup?.instruction || turn.content,
          resume ? run.id : "",
          "",
          false,
          followup?.intentHint || "",
        );
        return;
      }
      await managerRef.current.fork(turn.id);
    } catch (error) {
      setSessionError(message(error));
    }
  }

  /** Extract only the selected historical response while keeping its session ownership. */
  async function extract(run: Job) {
    if (run.session_id && run.session_id !== managerRef.current.selected()?.id)
      await managerRef.current.select(run.session_id);
    await start(
      false,
      "agent",
      t("Prepare structured changes from this discussion for review."),
      "",
      run.id,
      false,
      "commit_extract",
    );
  }

  /** Open a historical execution together with its complete owning conversation. */
  async function selectRun(run: Job) {
    try {
      if (
        run.session_id &&
        run.session_id !== managerRef.current.selected()?.id
      )
        await managerRef.current.select(run.session_id);
      if (
        !run.session_id ||
        managerRef.current.selected()?.id === run.session_id
      ) {
        setJob(run);
        updateJob(run);
      }
    } catch (error) {
      setSessionError(message(error));
    }
  }

  /** Dispatch local controls and writing intents from the same slash-command registry. */
  async function command(name: string, args = "") {
    const definition = chatCommands.find((item) => item.name === name);
    /** Consume only the command token that opened this local control. */
    function clearLocalCommand() {
      if (parseChatCommand(inputRef.current)?.name === name) setInstruction("");
    }
    if (!definition)
      throw new Error(t("Unknown command /{0}. Your draft is kept.", name));
    if (name === "model") {
      if (args) {
        const profile = options.settings?.profiles.find(
          (item) =>
            item.enabled &&
            [
              item.id,
              item.name.toLowerCase(),
              item.config.model.toLowerCase(),
            ].includes(args.toLowerCase()),
        );
        if (!profile)
          throw new Error(t("Model not found. Choose an enabled connection."));
        options.update({
          selection: { profile_id: profile.id, output_mode: "auto" },
        });
        setInstruction("");
      } else {
        clearLocalCommand();
        setModelOpen(true);
      }
      return;
    }
    if (name === "context") {
      await start(true, "agent", args || instruction);
      clearLocalCommand();
      return;
    }
    if (name === "new") {
      await managerRef.current.create();
      return;
    }
    if (name === "resume") {
      clearLocalCommand();
      managerRef.current.open();
      return;
    }
    if (name === "skill") {
      if (!args) {
        clearLocalCommand();
        setToolModal("skills");
        return;
      }
      const matches = options.skillsCatalog.filter(
        (skill) =>
          skill.id === args ||
          skill.name.toLocaleLowerCase() === args.toLocaleLowerCase(),
      );
      if (matches.length !== 1)
        throw new Error(t("Choose a unique skill from the catalog."));
      options.update({
        skills: [...new Set([...options.skills, matches[0].id])],
      });
      setInstruction("");
      return;
    }
    if (name === "search") {
      if (!args) {
        clearLocalCommand();
        setToolModal("search");
        return;
      }
      await start(false, "agent", args, "", "", false, "web_search");
      return;
    }
    if (name === "compact") {
      await start(
        false,
        "agent",
        t(
          "Save a conversation checkpoint, then briefly state the remaining goal and open questions.",
        ),
        "",
        "",
        true,
      );
      return;
    }
    if (name === "polish") {
      if (!chapter || !canHumanize)
        throw new Error(
          t("Open a chapter with prose before using this command."),
        );
      await start(false, "rewrite", args || t(humanizeInstruction));
      return;
    }
    if (name === "write" || name === "review") {
      if (name === "write" && (!chapter || workspaceContext?.active_plan_id)) {
        await start(
          false,
          "agent",
          args ||
            t(
              "Use the writer tool to write the next unwritten chapter from the confirmed chapter outline. Select its plan_id, create the chapter, review the prose and save it.",
            ),
          "",
          "",
          false,
          "write_from_outline",
        );
        return;
      }
      if (!chapter)
        throw new Error(t("Open a chapter before using this command."));
      await start(
        false,
        name === "write" ? "continue" : "review",
        args || t(definition.prompt || definition.description),
        "",
        "",
        false,
        name === "write" ? "write_from_outline" : "",
      );
      return;
    }
    if (name === "wiki") {
      if (!chapter && !source)
        throw new Error(t("Select a chapter or source to analyze"));
      await start(false, "wiki", "", "", "", false, "", args === "--force");
      return;
    }
    await start(
      false,
      "agent",
      args || t(definition.prompt || definition.description),
      "",
      "",
      false,
      definition.intent || "",
    );
  }

  /** Treat escaped leading slashes as ordinary prose and preserve unrecognized commands for editing. */
  async function submit() {
    setError("");
    try {
      const parsed = parseChatCommand(instruction);
      if (parsed) await command(parsed.name, parsed.args);
      else
        await start(
          false,
          action,
          instruction.startsWith("//") ? instruction.slice(1) : instruction,
        );
    } catch (error) {
      setError(message(error));
    }
  }

  /** Cancel only the execution currently displayed, never an unrelated conversation's task. */
  async function stop() {
    const target = jobRef.current;
    if (
      !target ||
      target.session_id !== managerRef.current.selected()?.id ||
      target.status !== "running"
    )
      return;
    try {
      const result = await api<{ cancelled: boolean }>("cancel_ai", {
        job_id: target.id,
      });
      if (!result.cancelled)
        setError(
          t(
            "The task has ended or entered the commit stage. Cancellation is unavailable.",
          ),
        );
    } catch (error) {
      setError(message(error));
    }
  }

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
        <small
          className="assistant-current-model"
          title={primary?.config.model}
        >
          {primary?.name || t("Choose model")}
        </small>
        <button
          className="icon-button"
          title={t("Settings")}
          aria-label={t("Settings")}
          onClick={onSettings}
        >
          <IconSettings size={17} />
        </button>
        <button
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
      <ConversationTools manager={manager} disabled={busy} />
      {sessionError && <ErrorBox>{sessionError}</ErrorBox>}
      {projectRunning && !running && (
        <div className="chat-background-task">
          <Button onClick={() => void selectRun(projectRunning)}>
            {t("View the running task")}
          </Button>
        </div>
      )}
      <AssistantTimeline
        jobs={timeline}
        exploratory={!chapter && !source && exploring}
        project={project}
        disabled={busy || !!projectRunning}
        chapterId={chapter?.id}
        sessionId={activeSession}
        onSuggestion={(text) => {
          setAction("agent");
          setInstruction(text);
        }}
        onBranch={branchAt}
        onExtract={extract}
        onJob={updateJob}
        onContext={setContext}
        onRefresh={onRefresh}
        beforeStart={beforeStart}
        onOpenTarget={onOpenTarget}
        onApply={onApply}
      />
      <ResizeHandle
        axis="y"
        label={t("Drag to resize the composer")}
        resetLabel={t("Double-click to reset the size")}
        value={composerHeight}
        onChange={setComposerHeight}
        bounds={composerSizeBounds}
        target={composerRef}
      />
      {source && (
        <div className="chat-source-label">
          <span>{source.title}</span>
          {onClearSource && (
            <button className="text-button" onClick={onClearSource}>
              {t("Clear")}
            </button>
          )}
        </div>
      )}
      <AssistantComposer
        input={instruction}
        onInput={setInstruction}
        onSubmit={() => void submit()}
        onStop={() => void stop()}
        onCommand={(name) =>
          void command(name).catch((error) => setError(message(error)))
        }
        onRecall={() => {
          const prior = [...(manager.session?.turns || [])]
            .reverse()
            .find((turn) => turn.role === "user");
          if (prior) setInstruction(prior.content);
        }}
        busy={busy || (!!projectRunning && !running)}
        running={!!running}
        loading={manager.loading}
        error={error || options.error}
        prompt={measured}
        chips={chips}
        onRemoveChip={(id) => setOmissions((current) => omitChip(current, id))}
        selectedSkills={options.skills}
        skills={options.skillsCatalog}
        onRemoveSkill={(id) =>
          options.update({
            skills: options.skills.filter((value) => value !== id),
          })
        }
        settings={options.settings}
        catalog={options.catalog}
        selection={options.selection}
        onSelection={(selection) => options.update({ selection })}
        modelOpen={modelOpen}
        onModelOpen={setModelOpen}
        onSettings={onSettings}
        composerRef={composerRef}
        resized={composerHeight != null}
        commitPolicy={options.commitPolicy}
        onCommitPolicy={(commitPolicy) => options.update({ commitPolicy })}
        searchLabel={
          options.search !== "inherit"
            ? t("Search for next submission")
            : undefined
        }
      />
      <AssistantModals
        context={context}
        showHistory={showHistory}
        runs={runs}
        onCloseContext={() => setContext(null)}
        onCloseHistory={() => setShowHistory(false)}
        onSelectRun={(run) => void selectRun(run)}
      />
      {toolModal === "search" && (
        <ChatSearchModal
          value={options.search}
          onChange={(search) => options.update({ search })}
          onClose={() => setToolModal(null)}
          onRequest={() => {
            setToolModal(null);
            setInstruction(completeChatCommand(inputRef.current, "search"));
          }}
        />
      )}
      {toolModal === "skills" && (
        <ChatSkillsModal
          skills={options.skillsCatalog}
          selected={options.skills}
          onChange={(skills) => options.update({ skills })}
          onReload={options.reload}
          onClose={() => setToolModal(null)}
        />
      )}
    </aside>
  );
}
