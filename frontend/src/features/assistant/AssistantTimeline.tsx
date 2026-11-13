import { useEffect, useRef, useState } from "react";
import { IconArrowDown } from "@tabler/icons-react";
import { Button, Suggestion } from "../../components/ui";
import { t } from "../../lib/i18n";
import type { Job, Prompt, Project, WritingFollowup } from "../../domain/types";
import { AssistantTurn } from "./AssistantTurn";
import { explorationSuggestions } from "./assistantPrompts";

interface Props {
  jobs: Job[];
  exploratory: boolean;
  project: Project;
  disabled: boolean;
  chapterId?: string;
  onSuggestion: (text: string) => void;
  onBranch: (
    run: Job,
    regenerate?: boolean,
    resume?: boolean,
    followup?: WritingFollowup,
  ) => Promise<void>;
  onExtract: (run: Job) => Promise<void>;
  onJob: (job: Job) => void;
  onContext: (prompt: Prompt) => void;
  onRefresh: () => Promise<void>;
  beforeStart?: () => Promise<Project | null>;
  onOpenTarget?: (kind: string, id: string) => void;
  onApply?: (job: Job) => Promise<void>;
  sessionId: string;
}

/** Follow streamed output only while the author remains near the bottom of the timeline. */
export function AssistantTimeline(props: Props) {
  const viewport = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const [showLatest, setShowLatest] = useState(false);
  const last = props.jobs.at(-1);
  useEffect(() => {
    following.current = true;
    setShowLatest(false);
    const frame = requestAnimationFrame(() => {
      if (viewport.current)
        viewport.current.scrollTop = viewport.current.scrollHeight;
    });
    return () => cancelAnimationFrame(frame);
  }, [props.sessionId]);
  useEffect(() => {
    if (following.current && viewport.current)
      viewport.current.scrollTop = viewport.current.scrollHeight;
    else if (last?.status === "running") setShowLatest(true);
  }, [last?.id, last?.text, last?.steps, last?.activity, props.jobs.length]);

  /** Resume automatic following after an explicit jump to the latest output. */
  function jumpToLatest() {
    following.current = true;
    setShowLatest(false);
    if (viewport.current)
      viewport.current.scrollTop = viewport.current.scrollHeight;
  }
  return (
    <div className="chat-timeline-wrap">
      <div
        ref={viewport}
        className="assistant-messages"
        aria-live="polite"
        aria-busy={last?.status === "running"}
        onScroll={() => {
          const element = viewport.current;
          if (!element) return;
          following.current =
            element.scrollHeight - element.scrollTop - element.clientHeight <
            80;
          if (following.current) setShowLatest(false);
        }}
      >
        {!props.jobs.length && (
          <div className="assistant-welcome">
            <h2>
              {t(
                props.exploratory
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
                  onClick={() => props.onSuggestion(t(item.instruction))}
                >
                  {t(item.label)}
                </Suggestion>
              ))}
            </div>
          </div>
        )}
        {props.jobs.map((run) => (
          <AssistantTurn
            key={run.id}
            run={run}
            project={props.project}
            disabled={props.disabled}
            chapterId={props.chapterId}
            onBranch={props.onBranch}
            onExtract={props.onExtract}
            onJob={props.onJob}
            onContext={(job) => props.onContext(job.prompt)}
            onRefresh={props.onRefresh}
            beforeStart={props.beforeStart}
            onOpenTarget={props.onOpenTarget}
            onApply={props.onApply}
          />
        ))}
      </div>
      {showLatest && (
        <Button className="chat-jump-latest" onClick={jumpToLatest}>
          <IconArrowDown size={15} />
          {t("Back to latest")}
        </Button>
      )}
    </div>
  );
}
