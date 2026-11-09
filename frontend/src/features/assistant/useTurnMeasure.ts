import { useEffect, useState } from "react";
import { api } from "../../lib/api";
import type { Prompt, WorkspaceContext } from "../../domain/types";
import { omissionFields, type TurnOmissions } from "./turnContext";

/** Measure the prompt that would be sent, without starting a model call. */
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
}) {
  const [chars, setChars] = useState<number | null>(null);
  useEffect(() => {
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
      })
        .then((prompt) => {
          if (!cancelled) setChars(prompt.chars);
        })
        .catch(() => {
          if (!cancelled) setChars(null);
        });
    }, 600);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [
    input.action,
    input.chapterId,
    input.context,
    input.enabled,
    input.instruction,
    input.intent,
    input.omissions,
    input.projectId,
    input.sessionId,
    input.sourceId,
  ]);
  return chars;
}
