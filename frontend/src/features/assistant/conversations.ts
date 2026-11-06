import { useEffect, useRef, useState } from "react";
import { api } from "../../lib/api";
import type { Conversation, ConversationTurn } from "../../domain/studio";
import type { Job } from "../../domain/types";

/** Read optional local draft storage without making blocked storage a conversation failure. */
export function localDraft(key: string, fallback = "") {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

/** Retain an unsent draft independently from task completion and remote draft synchronization. */
export function saveLocalDraft(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* The editor remains usable when storage is unavailable. */
  }
}

/** Pair durable turns for the existing Markdown task renderer without discarding older history. */
export function conversationJobs(
  session: Conversation | null,
  runs: Job[],
): Job[] {
  if (!session) return [];
  const result: Job[] = [];
  let user: ConversationTurn | undefined;
  for (const turn of session.turns) {
    if (turn.role === "user") {
      user = turn;
      continue;
    }
    if (!user || turn.status !== "done") continue;
    const run = runs.find((value) => value.id === turn.job_id);
    result.push(
      run || {
        id: turn.job_id || user.id,
        project_id: session.project_id,
        chapter_id: session.chapter_id,
        action: turn.action || "ask",
        instruction: user.content,
        text: turn.content,
        status: turn.status,
        progress: "",
        error: "",
        created_at: turn.created_at,
        model: "",
        prompt: {
          messages: [],
          context: [],
          source_hash: "",
          source_id: "",
          chars: 0,
        },
        session_id: session.id,
      },
    );
  }
  return result;
}

/** Manage story-owned sessions, safe selection, durable drafts and non-destructive rewind branches. */
export function useConversations(
  projectId: string,
  chapterId: string,
  restore: (draft: string, job: Job | null, restoreDraft?: boolean) => void,
) {
  const [sessions, setSessions] = useState<Conversation[]>([]);
  const [session, setSession] = useState<Conversation | null>(null);
  const [loading, setLoading] = useState(true);
  const current = useRef<Conversation | null>(null);
  const restoreRef = useRef(restore);
  restoreRef.current = restore;
  const sequence = useRef(0);
  const creation = useRef<Promise<Conversation> | null>(null);
  const selectionKey = `quillweave-session-${projectId}-${chapterId || "plan"}`;
  const draftKey = `quillweave-chat-draft-${projectId}-${chapterId || "plan"}-${session?.id || "new"}`;

  /** Load session metadata and retain the current selection when a task finishes. */
  async function reload(selectId?: string) {
    const version = ++sequence.current;
    const list = await api<Conversation[]>("list_conversations", {
      project_id: projectId,
      chapter_id: chapterId,
    });
    if (version !== sequence.current) return;
    setSessions(list);
    const selected =
      selectId ?? current.current?.id ?? localDraft(selectionKey);
    const next = list.find((item) => item.id === selected) || list[0] || null;
    const changed = next?.id !== current.current?.id;
    current.current = next;
    setSession(next);
    setLoading(false);
    if (next) saveLocalDraft(selectionKey, next.id);
    if (changed && next) await restoreSession(next, version);
  }

  /** Restore a selected draft and its latest task while ignoring stale asynchronous responses. */
  async function restoreSession(next: Conversation, version: number) {
    const key = `quillweave-chat-draft-${projectId}-${chapterId || "plan"}-${next.id}`;
    const draft = localDraft(key, next.draft);
    restoreRef.current(draft, null);
    const latest = [...next.turns].reverse().find((turn) => turn.job_id);
    if (!latest) return;
    try {
      const job = await api<Job>("get_job", { job_id: latest.job_id });
      if (version === sequence.current && current.current?.id === next.id)
        restoreRef.current(localDraft(key, next.draft), job, false);
    } catch {
      /* Archived turns remain visible even when a task record is unavailable. */
    }
  }

  useEffect(() => {
    current.current = null;
    setSession(null);
    setLoading(true);
    void reload().catch(() => setLoading(false));
    return () => {
      sequence.current++;
    };
  }, [projectId, chapterId]);

  /** Start a fresh branch scope only when explicitly requested or when the first message is sent. */
  async function create(restoreDraft = true) {
    if (!creation.current)
      creation.current = api<Conversation>("create_conversation", {
        project_id: projectId,
        chapter_id: chapterId,
      });
    try {
      const next = await creation.current;
      current.current = next;
      setSession(next);
      setSessions((list) => [next, ...list]);
      saveLocalDraft(selectionKey, next.id);
      sequence.current++;
      if (restoreDraft)
        restoreRef.current(
          localDraft(
            `quillweave-chat-draft-${projectId}-${chapterId || "plan"}-${next.id}`,
          ),
          null,
        );
      return next;
    } finally {
      creation.current = null;
    }
  }

  /** Provide a stable session identity before submitting a request. */
  async function ensure() {
    return current.current || create(false);
  }

  /** Switch conversation without deleting any history or already accepted work. */
  async function select(id: string) {
    const next = sessions.find((item) => item.id === id);
    if (!next) return;
    const version = ++sequence.current;
    current.current = next;
    setSession(next);
    saveLocalDraft(selectionKey, id);
    await restoreSession(next, version);
  }

  /** Branch before a chosen user turn, restoring its text for edits or regeneration. */
  async function fork(turnId: string) {
    if (!current.current) throw new Error("Conversation not found");
    const next = await api<Conversation>("fork_conversation", {
      project_id: projectId,
      session_id: current.current.id,
      turn_id: turnId,
    });
    current.current = next;
    setSession(next);
    setSessions((list) => [next, ...list]);
    saveLocalDraft(selectionKey, next.id);
    sequence.current++;
    restoreRef.current(next.draft, null);
    return next;
  }

  /** Rename only the selected session using its latest server revision. */
  async function rename(title: string) {
    if (!current.current) return;
    const next = await api<Conversation>("update_conversation", {
      project_id: projectId,
      session_id: current.current.id,
      revision: current.current.revision,
      title,
    });
    current.current = next;
    setSession(next);
    setSessions((list) =>
      list.map((item) => (item.id === next.id ? next : item)),
    );
  }

  /** Synchronize a draft after reading the latest revision, keeping local edits on conflicts. */
  async function persistDraft(value: string, expectedId: string) {
    if (!expectedId || current.current?.id !== expectedId) return;
    const fresh = await api<Conversation>("get_conversation", {
      project_id: projectId,
      session_id: expectedId,
    });
    if (
      current.current?.id !== expectedId ||
      localDraft(
        `quillweave-chat-draft-${projectId}-${chapterId || "plan"}-${expectedId}`,
      ) !== value
    )
      return;
    const next = await api<Conversation>("update_conversation", {
      project_id: projectId,
      session_id: expectedId,
      revision: fresh.revision,
      draft: value,
    });
    if (current.current?.id === expectedId) {
      current.current = next;
      setSession(next);
    }
  }

  return {
    sessions,
    session,
    loading,
    draftKey,
    reload,
    create,
    ensure,
    select,
    fork,
    rename,
    persistDraft,
  };
}
