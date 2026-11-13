import { useEffect, useRef, useState } from "react";
import { api } from "../../lib/api";
import type { Conversation, ConversationTurn } from "../../domain/studio";
import type { Job } from "../../domain/types";

/** Read optional local storage while keeping conversation drafts usable when storage is blocked. */
export function localDraft(key: string, fallback = "") {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

/** Persist an unsent draft independently of asynchronous task completion. */
export function saveLocalDraft(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* Keep the in-memory draft. */
  }
}

/** Pair all durable terminal states, preserving partial answers and recoverable failed turns. */
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
    if (!user) continue;
    const run = runs.find(
      (value) => value.id === turn.job_id && value.session_id === session.id,
    );
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

/** Manage authoritative session selection separately from paged search results and pending operations. */
export function useConversations(
  projectId: string,
  chapterId: string,
  restore: (draft: string, job: Job | null, restoreDraft?: boolean) => void,
) {
  const [sessions, setSessions] = useState<Conversation[]>([]);
  const [session, setSession] = useState<Conversation | null>(null);
  const [loading, setLoading] = useState(true);
  const [hasMore, setHasMore] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [manageVersion, setManageVersion] = useState(0);
  const current = useRef<Conversation | null>(null);
  const restoreRef = useRef(restore);
  restoreRef.current = restore;
  const sequence = useRef(0);
  const listSequence = useRef(0);
  const scope = `${projectId}-${chapterId || "plan"}`;
  const scopeRef = useRef(scope);
  scopeRef.current = scope;
  const filters = useRef({ query: "", archived: false, offset: 0 });
  const selectionKey = `quillweave-session-${scope}`;
  const draftKey = `quillweave-chat-draft-${scope}-${session?.id || "new"}`;

  /** Restore a draft immediately and hydrate its latest task only if selection remains unchanged. */
  async function restoreSession(next: Conversation | null, version: number) {
    const key = `quillweave-chat-draft-${scope}-${next?.id || "new"}`;
    restoreRef.current(localDraft(key, next?.draft || ""), null);
    const latest = [...(next?.turns || [])]
      .reverse()
      .find((turn) => turn.job_id);
    if (!next || !latest) return;
    try {
      const job = await api<Job>("get_job", { job_id: latest.job_id });
      if (version === sequence.current && current.current?.id === next.id)
        restoreRef.current(localDraft(key, next.draft), job, false);
    } catch {
      /* Durable turns remain available when an execution record is missing. */
    }
  }

  /** Select the complete owning session, including sessions outside the current search page. */
  async function select(id: string) {
    const version = ++sequence.current;
    const capturedScope = scope;
    setLoading(true);
    try {
      const next = await api<Conversation>("get_conversation", {
        project_id: projectId,
        session_id: id,
      });
      if (version !== sequence.current || capturedScope !== scopeRef.current)
        return null;
      current.current = next;
      setSession(next);
      saveLocalDraft(selectionKey, next.id);
      await restoreSession(next, version);
      return next;
    } finally {
      if (version === sequence.current) setLoading(false);
    }
  }

  /** Query every stored page on the server without changing the conversation being viewed. */
  async function search(query = "", archived = false, append = false) {
    const version = ++listSequence.current;
    const capturedScope = scope;
    const offset = append ? filters.current.offset : 0;
    filters.current = { query, archived, offset };
    setLoadingMore(true);
    try {
      const list = await api<Conversation[]>("list_conversations", {
        project_id: projectId,
        chapter_id: chapterId,
        query,
        archived,
        offset,
      });
      if (
        version !== listSequence.current ||
        capturedScope !== scopeRef.current
      )
        return;
      setSessions((previous) =>
        append
          ? [
              ...new Map(
                [...previous, ...list].map((item) => [item.id, item]),
              ).values(),
            ]
          : list,
      );
      filters.current.offset = offset + list.length;
      setHasMore(list.length === 100);
    } finally {
      if (version === listSequence.current) setLoadingMore(false);
    }
  }

  /** Refresh metadata while retaining the selected session identity and the unsent draft. */
  async function reload(selectId?: string) {
    const version = sequence.current;
    const capturedScope = scope;
    const selected =
      selectId ?? current.current?.id ?? localDraft(selectionKey);
    await search(filters.current.query, filters.current.archived);
    if (version !== sequence.current || capturedScope !== scopeRef.current)
      return;
    if (selected) {
      try {
        const next = await api<Conversation>("get_conversation", {
          project_id: projectId,
          session_id: selected,
        });
        if (version !== sequence.current || capturedScope !== scopeRef.current)
          return;
        if (!next.archived) {
          const changed = current.current?.id !== next.id;
          current.current = next;
          setSession(next);
          saveLocalDraft(selectionKey, next.id);
          if (changed) await restoreSession(next, version);
          return;
        }
      } catch {
        /* A deleted or archived remembered selection opens a fresh draft. */
      }
    }
    current.current = null;
    setSession(null);
    await restoreSession(null, version);
  }

  useEffect(() => {
    current.current = null;
    setSession(null);
    filters.current = { query: "", archived: false, offset: 0 };
    setLoading(true);
    const version = ++sequence.current;
    void reload()
      .catch(() => {})
      .finally(() => {
        if (version === sequence.current) setLoading(false);
      });
    return () => {
      sequence.current++;
      listSequence.current++;
    };
  }, [projectId, chapterId]);

  /** Create a new session only if no later user selection has superseded this operation. */
  async function create(restoreDraft = true) {
    const version = ++sequence.current;
    const capturedScope = scope;
    const next = await api<Conversation>("create_conversation", {
      project_id: projectId,
      chapter_id: chapterId,
    });
    if (capturedScope !== scopeRef.current || version !== sequence.current)
      throw new Error(
        "Conversation changed while creating a new session. Your draft is kept.",
      );
    current.current = next;
    setSession(next);
    setSessions((list) => [
      next,
      ...list.filter((item) => item.id !== next.id),
    ]);
    saveLocalDraft(selectionKey, next.id);
    if (restoreDraft) await restoreSession(next, version);
    return next;
  }

  /** Supply the current nonarchived session before a request is admitted. */
  async function ensure() {
    return current.current && !current.current.archived
      ? current.current
      : create(false);
  }

  /** Branch within the selected owner and ignore responses superseded by a later selection. */
  async function fork(turnId: string) {
    if (!current.current) throw new Error("Conversation not found");
    const owner = current.current.id;
    const version = ++sequence.current;
    const next = await api<Conversation>("fork_conversation", {
      project_id: projectId,
      session_id: owner,
      turn_id: turnId,
    });
    if (version !== sequence.current || current.current?.id !== owner)
      throw new Error(
        "Conversation changed while creating a branch. Your draft is kept.",
      );
    current.current = next;
    setSession(next);
    setSessions((list) => [next, ...list]);
    saveLocalDraft(selectionKey, next.id);
    await restoreSession(next, version);
    return next;
  }

  /** Remove one user turn and everything after it in place, so a retry regenerates here. */
  async function truncate(turnId: string) {
    if (!current.current) throw new Error("Conversation not found");
    const owner = current.current.id;
    const version = ++sequence.current;
    const next = await api<Conversation>("truncate_conversation", {
      project_id: projectId,
      session_id: owner,
      turn_id: turnId,
    });
    if (version !== sequence.current || current.current?.id !== owner)
      throw new Error(
        "Conversation changed while retrying. Your draft is kept.",
      );
    current.current = next;
    setSession(next);
    setSessions((list) =>
      list.map((item) => (item.id === next.id ? next : item)),
    );
    return next;
  }

  /** Rename the captured session without replacing a newer selected session. */
  async function rename(title: string) {
    const owner = current.current;
    if (!owner) return;
    const next = await api<Conversation>("update_conversation", {
      project_id: projectId,
      session_id: owner.id,
      revision: owner.revision,
      title,
    });
    setSessions((list) =>
      list.map((item) => (item.id === next.id ? next : item)),
    );
    if (current.current?.id === owner.id) {
      current.current = next;
      setSession(next);
    }
  }

  /** Archive or restore a captured session using the latest server revision. */
  async function archive(id: string, archived: boolean) {
    const fresh = await api<Conversation>("get_conversation", {
      project_id: projectId,
      session_id: id,
    });
    await api<Conversation>("archive_conversation", {
      project_id: projectId,
      session_id: id,
      revision: fresh.revision,
      archived,
    });
    if (archived && current.current?.id === id) {
      const version = ++sequence.current;
      current.current = null;
      setSession(null);
      saveLocalDraft(selectionKey, "");
      await restoreSession(null, version);
    }
    await search(filters.current.query, filters.current.archived);
  }

  /** Persist a captured draft only while it still belongs to the selected session. */
  async function persistDraft(value: string, expectedId: string) {
    if (!expectedId || current.current?.id !== expectedId) return;
    const fresh = await api<Conversation>("get_conversation", {
      project_id: projectId,
      session_id: expectedId,
    });
    if (
      current.current?.id !== expectedId ||
      localDraft(`quillweave-chat-draft-${scope}-${expectedId}`) !== value
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
    selected: () => current.current,
    sessions,
    session,
    loading,
    hasMore,
    loadingMore,
    draftKey,
    manageVersion,
    open: () => setManageVersion((value) => value + 1),
    search,
    loadMore: () =>
      search(filters.current.query, filters.current.archived, true),
    reload,
    create,
    ensure,
    select,
    fork,
    truncate,
    rename,
    archive,
    persistDraft,
  };
}
