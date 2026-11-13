import { useState } from "react";
import { IconDatabaseImport } from "@tabler/icons-react";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import { Badge, Button, Modal } from "../../components/ui";
import {
  actions,
  type ChangeSet,
  type Job,
  type Prompt,
} from "../../domain/types";

interface ExtractButtonProps {
  projectId: string;
  sessionId?: string;
  text: string;
  disabled?: boolean;
  onExtracted: (changeset: ChangeSet) => void;
  onError: (err: string) => void;
  onRequest?: () => Promise<void>;
}

/** Extract structured story settings from any assistant message bubble into a reviewable ChangeSet. */
export function ExtractChangeSetButton({
  projectId,
  sessionId = "",
  text,
  disabled,
  onExtracted,
  onError,
  onRequest,
}: ExtractButtonProps) {
  const [extracting, setExtracting] = useState(false);

  /** Stage the selected response through its owning conversation. */
  async function extract() {
    if (!text.trim() || extracting) return;
    setExtracting(true);
    try {
      if (onRequest) {
        await onRequest();
        return;
      }
      const cs = await api<ChangeSet>("extract_changeset", {
        project_id: projectId,
        session_id: sessionId,
        text,
      });
      onExtracted(cs);
    } catch (e) {
      onError(message(e));
    } finally {
      setExtracting(false);
    }
  }

  return (
    <Button disabled={disabled || extracting} onClick={() => void extract()}>
      <IconDatabaseImport size={15} />
      {extracting ? t("Extracting…") : t("Extract & stage settings")}
    </Button>
  );
}

interface ModalsProps {
  context: Prompt | null;
  showHistory: boolean;
  runs: Job[];
  onCloseContext: () => void;
  onCloseHistory: () => void;
  onSelectRun: (run: Job) => void;
}

/** Render context inspection and task history modals for the unified writing assistant. */
export function AssistantModals({
  context,
  showHistory,
  runs,
  onCloseContext,
  onCloseHistory,
  onSelectRun,
}: ModalsProps) {
  return (
    <>
      {context && (
        <Modal title={t("Request context")} onClose={onCloseContext} wide>
          <div className="modal-content">
            {context.warning && (
              <p role="status" className="muted">
                {t(context.warning)}
              </p>
            )}
            {context.budget && (
              <div className="chat-context-budget">
                <p>
                  {t("Input tokens")} · {context.budget.estimated ? "~" : ""}
                  {context.budget.input_tokens_estimate.toLocaleString()}
                </p>
                <p>
                  {t("Context window")} ·{" "}
                  {context.budget.context_window_tokens
                    ? context.budget.context_window_tokens.toLocaleString()
                    : t("Unknown")}
                </p>
                <p>
                  {t("Output budget")} ·{" "}
                  {context.budget.output_tokens.toLocaleString()}
                </p>
                {context.budget.needs_compaction && (
                  <p>
                    {t("Earlier exchanges will be compacted before sending.")}
                  </p>
                )}
                {context.budget.warning && (
                  <p role="status">{t(context.budget.warning)}</p>
                )}
              </div>
            )}
            {!!context.carried_records?.length && (
              <details className="context-item">
                <summary>{t("What this turn carries")}</summary>
                {context.carried_records.map((record) => (
                  <p key={record.id}>
                    {record.label}
                    <small> · {record.id}</small>
                  </p>
                ))}
              </details>
            )}
            {!!context.skills?.length && (
              <details className="context-item">
                <summary>{t("Skills")}</summary>
                {context.skills.map((skill) => (
                  <p key={skill.id}>
                    {skill.name}
                    <small>
                      {" "}
                      · {skill.source} · {skill.version.slice(0, 12)}
                    </small>
                  </p>
                ))}
              </details>
            )}
            {context.checkpoint && (
              <details className="context-item">
                <summary>{t("Conversation summary")}</summary>
                <p className="preserve">{context.checkpoint.summary}</p>
              </details>
            )}
            <p className="muted">
              {t("Total")} {context.chars.toLocaleString()}{" "}
              {t(
                "characters, including the story bible, current chapter, and retrieved evidence below.",
              )}
            </p>
            {context.context.map((item) => (
              <details className="context-item" key={item.id}>
                <summary>
                  {t(item.title)}
                  {item.required && <Badge>{t("Required evidence")}</Badge>}
                  <small>{item.id}</small>
                </summary>
                <p className="muted">
                  {item.reason && <span> {t(item.reason)}</span>}
                  {item.source_hash && (
                    <small>
                      {" "}
                      · {t("Source version")} {item.source_hash.slice(0, 12)}
                    </small>
                  )}
                </p>
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
        <Modal title={t("AI task history")} onClose={onCloseHistory}>
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
                  onSelectRun(run);
                  onCloseHistory();
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
    </>
  );
}
