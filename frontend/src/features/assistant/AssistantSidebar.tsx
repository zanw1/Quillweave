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
