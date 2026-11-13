import {
  IconBrain,
  IconCheck,
  IconLoader2,
  IconTool,
  IconX,
} from "@tabler/icons-react";
import type { Job } from "../../domain/types";
import { MarkdownResponse } from "../../components/ui";
import { t } from "../../lib/i18n";
import { useEffect, useRef, useState } from "react";
import { isVisibleChatActivity } from "./visibleText";

/** Fold completed thoughts automatically until the author explicitly chooses an expansion state. */
function Thought({ text, live }: { text: string; live: boolean }) {
  const [open, setOpen] = useState(live);
  const interacted = useRef(false);
  useEffect(() => {
    if (!interacted.current) setOpen(live);
  }, [live]);
  return (
    <details className="chat-reasoning" open={open}>
      <summary
        onClick={(event) => {
          event.preventDefault();
          interacted.current = true;
          setOpen(!open);
        }}
      >
        <IconBrain size={15} />
        <span>{t(live ? "Thinking" : "Thought summary")}</span>
        {live && <IconLoader2 className="spin" size={14} />}
      </summary>
      <div className="chat-reasoning-content">
        <MarkdownResponse text={text} streaming={live} />
      </div>
    </details>
  );
}

const tools: Record<string, string> = {
  recall: "Recall story context",
  get_entity_detail: "Read character details",
  get_chapter_window: "Read chapter prose",
  search_story_facts: "Find story evidence",
  get_plan_node: "Read story outline",
  get_world_map: "Read world map",
  get_source_detail: "Read reference material",
  get_decision_detail: "Read author decision",
  get_conversation_excerpt: "Read earlier conversation",
  get_task_step: "Read earlier tool result",
  load_skill: "Load skill",
  search: "Search web sources",
  researcher: "Research story material",
  reviewer: "Review chapter",
  writer: "Write chapter prose",
  plan: "Arrange task steps",
  propose: "Prepare proposed changes",
  propose_changes: "Prepare proposed changes",
  native_tools: "Check tool request",
};

/** Render model-provided thought summaries and tool receipts in their original order beside streamed prose. */
export function ChatActivity({ job }: { job: Job }) {
  return (
    <div className="chat-activity">
      {job.activity?.filter(isVisibleChatActivity).map((item) => {
        const live = job.status === "running" && item.status === "running";
        if (item.kind === "text")
          return (
            <MarkdownResponse
              key={item.id}
              text={item.text || ""}
              streaming={live}
              incomplete={item.status === "interrupted"}
            />
          );
        if (item.kind === "reasoning")
          return <Thought key={item.id} text={item.text || ""} live={live} />;
        const step = job.steps?.find((step) => step.id === item.step_id);
        if (!step) return null;
        const active = job.status === "running" && step.status === "running";
        const status =
          step.status === "running" && !active ? "interrupted" : step.status;
        const StatusIcon = active
          ? IconLoader2
          : status === "done"
            ? IconCheck
            : IconX;
        return (
          <details key={item.id} className={`chat-tool-card ${status}`}>
            <summary>
              <IconTool size={15} />
              <span>{t(tools[step.tool] || step.tool)}</span>
              <StatusIcon className={active ? "spin" : ""} size={15} />
              <small>
                {t(
                  active
                    ? "Running"
                    : status === "done"
                      ? "Complete"
                      : status === "error"
                        ? "Failed"
                        : "Interrupted",
                )}
              </small>
            </summary>
            {step.input && <p className="chat-tool-input">{step.input}</p>}
            {step.finished_at && step.started_at && (
              <small>
                {t("Duration")} ·{" "}
                {Math.max(
                  0,
                  (Date.parse(step.finished_at) - Date.parse(step.started_at)) /
                    1000,
                ).toFixed(1)}{" "}
                s
              </small>
            )}
            {step.detail && (
              <div className="chat-tool-result">
                <pre>{readableResult(step.detail)}</pre>
              </div>
            )}
          </details>
        );
      })}
    </div>
  );
}

/** Keep complete tool results inspectable while formatting valid structured payloads for reading. */
function readableResult(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}
