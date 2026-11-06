import { t } from "../lib/i18n";
import { IconDownload, IconFeather } from "@tabler/icons-react";
import { Button, ErrorBox, Modal } from "../components/ui";
import { useState } from "react";
import { api, download, message } from "../lib/api";
import { downloadArchive } from "../lib/archive";
import type { Project } from "../domain/types";

/** Keep the workspace brand and home navigation together across layout changes. */
export function WorkspaceBrand({ onHome }: { onHome: () => void }) {
  return (
    <button className="brand" onClick={onHome}>
      <span className="brand-mark">
        <IconFeather size={27} stroke={1.7} />
      </span>
      <span>
        Quillweave<small>{t("LOCAL WRITING")}</small>
      </span>
    </button>
  );
}

/** Start an undecided story without requiring a title, genre or completed creative brief. */
export function NewStoryDialog({
  title,
  error,
  busy,
  onTitle,
  onCreate,
  onCancel,
  onClose,
}: {
  title: string;
  error: string;
  busy: boolean;
  onTitle: (title: string) => void;
  onCreate: () => Promise<void>;
  onCancel: () => void;
  onClose: () => void;
}) {
  return (
    <Modal title={t("Start a new story")} onClose={onClose}>
      <form
        className="modal-content"
        onSubmit={(event) => {
          event.preventDefault();
          void onCreate();
        }}
      >
        <p className="field-hint">
          {t(
            "You don't need a story idea yet. Start a workspace, talk with AI, and decide the genre, length, and setting later.",
          )}
        </p>
        <label>
          {t("Working title (optional)")}
          <input
            autoFocus
            value={title}
            onChange={(event) => onTitle(event.target.value)}
            placeholder={t("Leave blank for an untitled story")}
            maxLength={120}
          />
        </label>
        {error && <ErrorBox>{error}</ErrorBox>}
        <div className="modal-actions">
          <Button onClick={onCancel}>{t("Cancel")}</Button>
          <Button primary type="submit" disabled={busy}>
            {t("Start exploring")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}

/** Export only the selected work's saved content through its existing reviewed formats. */
export function WorkspaceExport({
  project,
  onExported,
  onError,
}: {
  project: Project;
  onExported: () => void;
  onError: (error: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  /** Export saved content while keeping full backup bytes out of the command envelope. */
  async function exportWork(format: string) {
    setBusy(true);
    try {
      if (format === "archive") {
        if (!(await downloadArchive(project.id, project.title))) return;
      } else {
        const result = await api<{ filename: string; content: string }>(
          "export",
          { project_id: project.id, format },
        );
        await download(result.filename, result.content);
      }
      onExported();
    } catch (error) {
      onError(message(error));
    } finally {
      setBusy(false);
    }
  }
  return (
    <details className="export-menu">
      <summary>
        <IconDownload size={17} />
        <span>{t("Export project")}</span>
      </summary>
      <div>
        <button disabled={busy} onClick={() => void exportWork("manuscript")}>
          {t("Manuscript Markdown")}
        </button>
        <button disabled={busy} onClick={() => void exportWork("wiki")}>
          {t("Character Wiki Markdown")}
        </button>
        <button disabled={busy} onClick={() => void exportWork("archive")}>
          {t("Full project backup")}
        </button>
        <small>{t("Export the latest saved version")}</small>
      </div>
    </details>
  );
}
