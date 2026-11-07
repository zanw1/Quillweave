import { useEffect, useRef, useState } from "react";
import {
  IconArrowBackUp,
  IconArrowUpRight,
  IconCheck,
} from "@tabler/icons-react";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import { Badge, Button, ErrorBox } from "../../components/ui";
import type { ChangeOperation, ChangeSet } from "../../domain/studio";
import type { Project } from "../../domain/types";

interface Props {
  project: Project;
  changeset: ChangeSet;
  onRefresh: () => Promise<void>;
  onChangeSetUpdated?: (changeset: ChangeSet) => void;
  onOpenTarget?: (kind: string, id: string) => void;
  beforeConfirm?: () => Promise<Project | null>;
}

const kindLabels: Record<string, string> = {
  project_meta: "Story Setup",
  studio_goal: "Story goals",
  decision: "Author decision",
  entity: "Character / World entry",
  memory: "Story memory",
  plan: "Story plan",
  chapter: "Chapter prose",
  location: "Location",
  route: "Travel route",
};
const fieldLabels: Record<string, string> = {
  title: "Title",
  name: "Name",
  genre: "Genre",
  premise: "Premise",
  style: "Style notes",
  outline: "Story outline",
  writing_style: "Writing style",
  default_chapter_words: "Chapter word target",
  narrative_pov: "Narrative viewpoint",
  mode: "Story form",
  stage: "Stage",
  content: "Content",
  description: "Description",
  kind: "Type",
  aliases: "Aliases",
  body: "Chapter prose",
  synopsis: "Synopsis",
  target_characters: "Target characters",
  target_chapters: "Target chapters",
  target_volumes: "Target volumes",
  pacing: "Pacing",
  narrative_focus: "Narrative emphasis",
  audience: "Audience",
  naming_style: "Naming preference",
  forbidden_names: "Forbidden names",
  preferences: "Author preferences",
  goal: "Goal",
  conflict: "Conflict",
  choice: "Choice",
  consequence: "Consequence",
  theme: "Theme",
  pov: "Viewpoint",
  characters: "Characters",
  clues: "Clues",
  level: "Level",
  start_chapter: "Start chapter",
  end_chapter: "End chapter",
  entity: "Related character",
  state: "State",
  chapter: "Chapter",
  through_chapter: "Valid through chapter",
  due_chapter: "Due chapter",
  story_time: "Story time",
  known_by: "Known by",
  quote: "Source quote",
  region_id: "Region",
  from_id: "From location",
  to_id: "To location",
  travel_hours: "Travel hours",
  transport: "Transport",
  bidirectional: "Two-way route",
  entity_ids: "Linked world entries",
  plan_ids: "Linked plans",
  chapter_ids: "Linked chapters",
  x: "Horizontal position",
  y: "Vertical position",
};
const internalFields = new Set([
  "id",
  "status",
  "created_at",
  "updated_at",
  "session_id",
  "source_hash",
  "source_id",
  "parent_id",
  "supersedes",
]);

/** Convert record values into readable field text while keeping model JSON outside the conversation. */
function valueText(value: unknown): string {
  if (value == null) return "";
  if (Array.isArray(value)) return value.map(valueText).join("\n");
  if (typeof value === "object")
    return Object.entries(value)
      .map(
        ([key, item]) => `${t(fieldLabels[key] || key)} — ${valueText(item)}`,
      )
      .join("\n");
  return String(value);
}

/** Narrow structured values to plain records for the field editor. */
function fields(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

/** Display the server-frozen comparison and edit one candidate without changing the saved story. */
function OperationFields({
  operation,
  references,
  disabled,
  onEdit,
}: {
  operation: ChangeOperation;
  references: Map<string, string>;
  disabled: boolean;
  onEdit: (value: Record<string, unknown>) => void;
}) {
  const proposed = fields(operation.after_value);
  const before = fields(operation.before_value);
  const after = fields(operation.preview_value ?? operation.after_value);
  /** Resolve record references to the same titles shown in their destination pages. */
  function displayValue(key: string, value: unknown): string {
    if (
      ["entity_ids", "plan_ids", "chapter_ids"].includes(key) &&
      Array.isArray(value)
    )
      return value
        .map((id) => references.get(String(id)) || String(id))
        .join("\n");
    if (
      ["region_id", "from_id", "to_id"].includes(key) &&
      typeof value === "string"
    )
      return references.get(value) || value;
    return valueText(value);
  }
  const keys = Object.keys(
    operation.op_type === "delete" ? before : proposed,
  ).filter((key) => !internalFields.has(key));
  return (
    <div className="change-review-fields">
      {keys.map((key) => {
        const value = proposed[key];
        const label = t(fieldLabels[key] || key);
        const editable =
          operation.op_type !== "delete" &&
          (value == null || typeof value !== "object" || Array.isArray(value));
        return (
          <fieldset
            className="change-review-field"
            key={key}
            disabled={disabled}
          >
            <legend>{label}</legend>
            <div className="change-review-comparison">
              <div>
                <small>{t("Before")}</small>
                <p>{displayValue(key, before[key]) || t("Empty")}</p>
              </div>
              <div>
                <small>{t("After")}</small>
                <p>
                  {operation.op_type === "delete"
                    ? t("Delete")
                    : displayValue(key, after[key]) || t("Empty")}
                </p>
              </div>
            </div>
            {editable && (
              <details className="change-review-edit">
                <summary>{t("Edit proposed value")}</summary>
                <label>
                  <span className="sr-only">{t("Edit {0}", label)}</span>
                  {typeof value === "boolean" ? (
                    <input
                      type="checkbox"
                      aria-label={t("Edit {0}", label)}
                      checked={value}
                      onChange={(event) =>
                        onEdit({ ...proposed, [key]: event.target.checked })
                      }
                    />
                  ) : typeof value === "number" ? (
                    <input
                      type="number"
                      aria-label={t("Edit {0}", label)}
                      value={value}
                      onChange={(event) =>
                        onEdit({
                          ...proposed,
                          [key]: Number(event.target.value),
                        })
                      }
                    />
                  ) : (
                    <textarea
                      aria-label={t("Edit {0}", label)}
                      rows={3}
                      value={valueText(value)}
                      onChange={(event) =>
                        onEdit({
                          ...proposed,
                          [key]: Array.isArray(value)
                            ? event.target.value
                                .split("\n")
                                .map((item) => item.trim())
                                .filter(Boolean)
                            : event.target.value,
                        })
                      }
                    />
                  )}
                </label>
              </details>
            )}
          </fieldset>
        );
      })}
      {typeof proposed.source_id === "string" && proposed.source_id && (
        <small>
          {t("Source")} · {proposed.source_id}
        </small>
      )}
    </div>
  );
}

/** Review, edit and confirm an exact persisted candidate, with independently recoverable error states. */
export function ChangeSetEditor({
  project,
  changeset,
  onRefresh,
  onChangeSetUpdated,
  onOpenTarget,
  beforeConfirm,
}: Props) {
  const [current, setCurrent] = useState(changeset);
  const [operations, setOperations] = useState(changeset.operations);
  const [selected, setSelected] = useState(
    new Set(changeset.operations.map((op) => op.id)),
  );
  const [dirty, setDirty] = useState(false);
  const currentRef = useRef(changeset);
  const dirtyRef = useRef(false);
  const [remote, setRemote] = useState<ChangeSet | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pending =
    current.status === "staged" || current.status === "conflicted";
  const remotePending =
    !remote || remote.status === "staged" || remote.status === "conflicted";
  const references = new Map<string, string>([
    ...project.entities.map((item): [string, string] => [item.id, item.name]),
    ...project.chapters.map((item): [string, string] => [item.id, item.title]),
    ...project.studio.plans.map((item): [string, string] => [
      item.id,
      item.title,
    ]),
    ...(project.studio.locations || []).map((item): [string, string] => [
      item.id,
      item.name,
    ]),
    ...operations.map((op): [string, string] => {
      const value = fields(op.preview_value ?? op.after_value);
      return [op.target_id, String(value.name || value.title || op.title)];
    }),
  ]);

  /** Replace local review data with a newly persisted server revision. */
  function adopt(next: ChangeSet) {
    currentRef.current = next;
    dirtyRef.current = false;
    setCurrent(next);
    setOperations(next.operations);
    setSelected(new Set(next.operations.map((op) => op.id)));
    setDirty(false);
    setRemote(null);
  }

  // Resolve incoming server revisions separately from the author's current edits.
  useEffect(() => {
    const previous = currentRef.current;
    if (
      previous.id === changeset.id &&
      previous.status === changeset.status &&
      previous.review_token === changeset.review_token
    ) {
      setRemote(null);
    } else if (dirtyRef.current && previous.id === changeset.id) {
      setRemote(changeset);
    } else {
      adopt(changeset);
    }
  }, [changeset.id, changeset.status, changeset.review_token]);

  /** Prepare a preview or execute an explicit author action using its frozen confirmation token. */
  async function perform(
    command:
      | "preview_changeset"
      | "apply_changeset"
      | "undo_changeset"
      | "reject_changeset",
  ) {
    setBusy(true);
    setError("");
    try {
      if (
        command === "apply_changeset" &&
        beforeConfirm &&
        !(await beforeConfirm())
      )
        return;
      const result = await api<{ project: Project; changeset: ChangeSet }>(
        command,
        {
          project_id: project.id,
          changeset_id: current.id,
          revision:
            command === "apply_changeset"
              ? current.preview_revision
              : project.revision,
          review_token: current.review_token,
          ...(command === "preview_changeset" && dirty
            ? { operations: operations.filter((op) => selected.has(op.id)) }
            : {}),
        },
      );
      adopt(result.changeset);
      onChangeSetUpdated?.(result.changeset);
      await onRefresh();
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    if (changeset.status === "staged" && !changeset.review_token)
      void perform("preview_changeset");
  }, [changeset.id]);

  /** Keep editable candidate values separate until the author requests an updated preview. */
  function editOperation(id: string, value: Record<string, unknown>) {
    dirtyRef.current = true;
    setOperations((previous) =>
      previous.map((op) => (op.id === id ? { ...op, after_value: value } : op)),
    );
    setDirty(true);
  }

  const statusLabel =
    current.status === "applied"
      ? "Committed to story bible"
      : current.status === "undone"
        ? "Reverted"
        : current.status === "rejected"
          ? "Rejected"
          : "Review changes";
  return (
    <article className="change-review" data-changeset-id={current.id}>
      <header>
        <strong>{current.summary || t("Structured setting update")}</strong>
        <Badge tone={current.status === "applied" ? "green" : "blue"}>
          {t(statusLabel)}
        </Badge>
      </header>
      {error && <ErrorBox>{error}</ErrorBox>}
      {remote && (
        <ErrorBox>
          {remotePending
            ? "This preview changed elsewhere. Your edits are kept. Update the preview before confirming."
            : "This change was reviewed elsewhere. Your local edits are kept here for copying."}
        </ErrorBox>
      )}
      {pending && (
        <p className="muted">
          {t("Review each change, then confirm to save it to your story.")}
        </p>
      )}
      {operations.map((op) => (
        <section className="change-review-operation" key={op.id}>
          <div className="change-review-target">
            {pending ? (
              <label className="change-review-selection">
                <input
                  type="checkbox"
                  checked={selected.has(op.id)}
                  disabled={busy}
                  onChange={(event) => {
                    dirtyRef.current = true;
                    const next = new Set(selected);
                    if (event.target.checked) next.add(op.id);
                    else next.delete(op.id);
                    setSelected(next);
                    setDirty(true);
                  }}
                />
                <span>{op.title || t(kindLabels[op.target_kind])}</span>
              </label>
            ) : (
              <strong>{op.title || t(kindLabels[op.target_kind])}</strong>
            )}
            <small>{t(kindLabels[op.target_kind] || op.target_kind)}</small>
          </div>
          {pending && (
            <OperationFields
              operation={op}
              references={references}
              disabled={busy || !selected.has(op.id)}
              onEdit={(value) => editOperation(op.id, value)}
            />
          )}
          {current.status === "applied" && !current.archived && (
            <div className="change-review-saved">
              <IconCheck size={16} />
              <span>{t("Saved")}</span>
              {onOpenTarget && (
                <Button
                  onClick={() => onOpenTarget(op.target_kind, op.target_id)}
                >
                  <IconArrowUpRight size={15} />
                  {t("Open saved item")}
                </Button>
              )}
            </div>
          )}
        </section>
      ))}
      {!!current.skipped_operations?.length && (
        <small>
          {t("Skipped changes")} · {current.skipped_operations.length}
        </small>
      )}
      {!!current.impacts?.length && (
        <details className="change-review-impact">
          <summary>
            {t("Related plans and chapters")} · {current.impacts.length}
          </summary>
          {current.impacts.map((item) =>
            current.status === "applied" && onOpenTarget ? (
              <Button
                key={`${item.kind}-${item.id}`}
                onClick={() => onOpenTarget(item.kind, item.id)}
              >
                {item.title}
              </Button>
            ) : (
              <p key={`${item.kind}-${item.id}`}>{item.title}</p>
            ),
          )}
        </details>
      )}
      {dirty && (
        <p role="status" className="muted">
          {t("Update the preview to review your edits before confirming.")}
        </p>
      )}
      {current.applied_revision != null && (
        <small>
          {t("Saved revision")} · {current.applied_revision}
        </small>
      )}
      <footer>
        {pending && (
          <>
            <Button
              disabled={busy || selected.size === 0 || !remotePending}
              onClick={() => void perform("preview_changeset")}
            >
              {t("Update preview")}
            </Button>
            <Button
              primary
              disabled={
                busy || dirty || !!remote || !current.review_token || selected.size === 0
              }
              onClick={() => void perform("apply_changeset")}
            >
              {t("Confirm changes")}
            </Button>
            <Button
              disabled={busy || !remotePending}
              onClick={() => void perform("reject_changeset")}
            >
              {t("Reject")}
            </Button>
          </>
        )}
        {remote && (
          <Button disabled={busy} onClick={() => adopt(remote)}>
            {t("Discard local edits and load the saved review")}
          </Button>
        )}
        {current.status === "applied" && (
          <Button
            disabled={busy}
            onClick={() => void perform("undo_changeset")}
          >
            <IconArrowBackUp size={15} />
            {t("Undo commit")}
          </Button>
        )}
        {busy && <span role="status">{t("Saving…")}</span>}
      </footer>
    </article>
  );
}
