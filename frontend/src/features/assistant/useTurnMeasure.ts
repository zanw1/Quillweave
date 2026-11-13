import { useEffect, useState } from "react";
import { api } from "../../lib/api";
import type {
  ChatModelSelection,
  Prompt,
  WorkspaceContext,
} from "../../domain/types";
import { omissionFields, type TurnOmissions } from "./turnContext";

/** Prepare the same selected model, skills and evidence as submission, discarding stale measurements. */
export function useTurnMeasure(input: {
  enabled: boolean;
  projectId: string;
  chapterId: string;
  sourceId: string;
  action: string;
  instruction: string;
  intent: string;
  sessionId: string;
  context?: WorkspaceContext;
  omissions: TurnOmissions;
  selection?: ChatModelSelection;
  skills: string[];
  searchSelection?: { mode: string; profile_id?: string };
  commitPolicy: "review" | "auto";
}) {
  const [prompt, setPrompt] = useState<Prompt | null>(null);
  const identity = JSON.stringify(input);
  useEffect(() => {
    setPrompt(null);
    if (!input.enabled) return;
    let cancelled = false;
    const timer = window.setTimeout(() => {
      void api<Prompt>("prepare_ai", {
        project_id: input.projectId,
        chapter_id: input.chapterId,
        source_id: input.sourceId,
        action: input.action,
        instruction: input.instruction.trim(),
        intent_hint: input.intent || undefined,
        workspace_context: {
          ...input.context,
          active_chapter_id: input.chapterId || undefined,
          ...omissionFields(input.omissions),
        },
        session_id: input.sessionId,
        model_selection: input.selection,
        skill_ids: input.skills,
        search_selection: input.searchSelection,
        commit_policy: input.commitPolicy,
      })
        .then((value) => {
          if (!cancelled) setPrompt(value);
        })
        .catch(() => {
          if (!cancelled) setPrompt(null);
        });
    }, 450);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [identity]);
  return prompt;
}
