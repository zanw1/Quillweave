import { useEffect, useState } from "react";
import { IconTrash } from "@tabler/icons-react";
import { Button, ErrorBox, Modal } from "../../components/ui";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import type { ChangeSet } from "../../domain/studio";
import type { Chapter, Project } from "../../domain/types";
import { forgetChapterDraft } from "./drafts";

interface Receipt {
  project: Project;
  changeset: ChangeSet;
}

interface Props {
  project: Project;
  chapter?: Chapter;
  beforeDelete: () => Promise<Project | null>;
  onProject: (project: Project) => void;
  onChapter: (id: string) => void;
  onPendingChange: (pending: boolean) => void;
}

/** Preview a recoverable chapter deletion through the same reviewed changesets as other edits. */
export function ChapterDeletion({
  project,
  chapter,
  beforeDelete,
  onProject,
  onChapter,
  onPendingChange,
}: Props) {
  const [preview, setPreview] = useState<Receipt | null>(null);
  const [deleted, setDeleted] = useState<ChangeSet | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    onPendingChange(busy || !!preview);
  }, [busy, preview, onPendingChange]);

  /** Save current input before staging the selected chapter and its server-calculated impacts. */
  async function request() {
    if (!chapter || busy) return;
    const target = chapter;
    setBusy(true);
    setError("");
    let staged: ChangeSet | undefined;
    try {
      const saved = await beforeDelete();
      if (!saved) {
        setError(t("Save or resolve the chapter draft before deleting it."));
        return;
      }
      const savedTarget = saved.chapters.find((item) => item.id === target.id);
      if (!savedTarget) throw new Error("Chapter not found");
      staged = await api<ChangeSet>("stage_changeset", {
        project_id: saved.id,
        changeset: {
          source: "author",
          commit_policy: "review",
          summary: t("Delete chapter {0}", savedTarget.title),
          operations: [{
            target_kind: "chapter",
            target_id: target.id,
            op_type: "delete",
            title: savedTarget.title,
            summary: t("Delete this chapter and retain a recovery record"),
            after_value: null,
          }],
        },
      });
      setPreview(await api<Receipt>("preview_changeset", {
        project_id: saved.id,
        changeset_id: staged.id,
      }));
    } catch (cause) {
      setError(message(cause));
      if (staged) {
        try {
          await api("reject_changeset", { project_id: project.id, changeset_id: staged.id });
        } catch (cleanup) {
          setError(`${message(cause)} ${message(cleanup)}`);
        }
      }
    } finally {
      setBusy(false);
    }
  }

  /** Re-read a conflicting deletion preview without applying it or discarding local prose. */
  async function refreshPreview() {
    if (!preview || busy) return;
    setBusy(true);
    setError("");
    try {
      const latest = await api<Project>("get_project", { project_id: preview.project.id });
      onProject(latest);
      setPreview(await api<Receipt>("preview_changeset", {
        project_id: latest.id,
        changeset_id: preview.changeset.id,
      }));
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy(false);
    }
  }

  /** Reject an abandoned preview so it cannot remain as an unnoticed pending deletion. */
  async function cancel() {
    if (!preview || busy) return;
    setBusy(true);
    try {
      await api("reject_changeset", { project_id: preview.project.id, changeset_id: preview.changeset.id });
      setPreview(null);
      setError("");
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy(false);
    }
  }

  /** Apply only the displayed revision and select a neighboring surviving chapter. */
  async function confirm() {
    if (!preview || busy) return;
    setBusy(true);
    setError("");
    try {
      const saved = await beforeDelete();
      if (!saved) {
        setError(t("Save or resolve the chapter draft before deleting it."));
        return;
      }
      const changeset = preview.changeset;
      if (saved.revision !== changeset.preview_revision) {
        setPreview(await api<Receipt>("preview_changeset", {
          project_id: saved.id,
          changeset_id: changeset.id,
        }));
        setError(t("The chapter changed. Review the updated deletion preview."));
        return;
      }
      const id = changeset.operations[0].target_id;
      const index = preview.project.chapters.findIndex((item) => item.id === id);
      const result = await api<Receipt>("apply_changeset", {
        project_id: preview.project.id,
        changeset_id: changeset.id,
        revision: changeset.preview_revision,
        review_token: changeset.review_token,
      });
      forgetChapterDraft(`quillweave-draft-${result.project.id}-${id}`);
      onProject(result.project);
      onChapter(result.project.chapters[Math.min(Math.max(index, 0), result.project.chapters.length - 1)]?.id || "");
      setDeleted(result.changeset);
      setPreview(null);
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy(false);
    }
  }

  /** Restore the deletion's archived chapter and dependencies using normal undo conflict checks. */
  async function undo() {
    if (!deleted || busy) return;
    setBusy(true);
    setError("");
    try {
      const result = await api<Receipt>("undo_changeset", {
        project_id: project.id,
        changeset_id: deleted.id,
        revision: project.revision,
      });
      onProject(result.project);
      onChapter(deleted.operations[0].target_id);
      setDeleted(null);
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="chapter-delete-actions">
      <button
        data-testid="delete-chapter"
        className="text-button"
        disabled={!chapter || busy}
        onClick={() => void request()}
      >
        <IconTrash size={17} />
        {t("Delete chapter")}
      </button>
      {deleted && (
        <button
          data-testid="undo-delete-chapter"
          className="text-button"
          disabled={busy}
          onClick={() => void undo()}
        >
          {t("Undo chapter deletion")}
        </button>
      )}
      {error && !preview && <ErrorBox>{error}</ErrorBox>}
      {preview && (
        <Modal title={t("Delete chapter")} onClose={() => void cancel()}>
          <div className="modal-content">
            <p>{t("Delete chapter {0}", preview.changeset.operations[0].title)}</p>
            <p>{t("Chapter numbers and outlines are retained. The deletion can be undone from its change receipt.")}</p>
            {!!preview.changeset.impacts?.length && (
              <ul className="chapter-delete-impacts">
                {preview.changeset.impacts.map((impact, index) => (
                  <li key={`${impact.kind}-${impact.id}-${index}`}>{impact.title}</li>
                ))}
              </ul>
            )}
            {error && <ErrorBox>{error}</ErrorBox>}
            <div className="modal-actions">
              <Button disabled={busy} onClick={() => void cancel()}>{t("Cancel")}</Button>
              <Button disabled={busy} onClick={() => void refreshPreview()}>{t("Refresh deletion preview")}</Button>
              <Button data-testid="confirm-delete-chapter" disabled={busy} onClick={() => void confirm()}>
                {t("Confirm chapter deletion")}
              </Button>
            </div>
          </div>
        </Modal>
      )}
    </div>
  );
}
