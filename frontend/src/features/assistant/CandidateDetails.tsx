import type { ChangeOperation, ChangeSet, ChangeEffect } from "../../domain/studio";
import { Select } from "../../components/Select";
import { t } from "../../lib/i18n";

/** Show external authorship and source-located review results beside the candidate being confirmed. */
export function CandidateProvenance({ candidate }: { candidate: ChangeSet }) {
  const provenance = candidate.provenance;
  if (!provenance) return null;
  return (
    <details className="change-review-impact">
      <summary>{t(provenance.transport === "desktop" ? "Prepared by" : "External author")} · {provenance.client}</summary>
      {provenance.writing_session_id && <p>{t("Writing session")} · {provenance.writing_session_id}</p>}
      {provenance.wiki_session_id && <p>{t("Wiki session")} · {provenance.wiki_session_id}</p>}
      {provenance.review && (
        <>
          <p>{t("External review")} · {provenance.review.summary}</p>
          <p>{t("Revision rounds")} · {provenance.revisions ?? 0}</p>
          {provenance.review.issues.map((issue, index) => (
            <blockquote key={`${issue.scope}-${index}`}>
              <p>{issue.quote}</p>
              <p>{issue.explanation}</p>
              {issue.suggestion && <p>{issue.suggestion}</p>}
            </blockquote>
          ))}
        </>
      )}
    </details>
  );
}

/** Expand every Wiki record comparison and edit summary prose against server-validated evidence references. */
export function WikiFields({ operation, disabled, onEdit }: {
  operation: ChangeOperation;
  disabled: boolean;
  onEdit: (value: Record<string, unknown>) => void;
}) {
  const effects = Array.isArray(operation.before_value) ? operation.before_value as ChangeEffect[] : [];
  const patch = operation.after_value as Record<string, unknown>;
  const reviews = (patch.reviews || []) as { fact_id: string; decision: string; reason: string }[];
  const compilation = patch.compilation as { sections: { summary: string; evidence_ids: string[]; questions: string[] }[] } | undefined;
  return (
    <div className="change-review-fields">
      {effects.map((effect) => {
        const record = (effect.after ?? effect.before) as Record<string, unknown> | null;
        const review = reviews.find((item) => item.fact_id === effect.id);
        return (
          <section className="change-review-field" key={`${effect.kind}-${effect.id}`}>
            <strong>{t(effect.kind === "fact" ? "Source fact" : effect.kind === "compilation" ? "Wiki summary" : "Character / World entry")} · {String(record?.entity || record?.name || effect.id)}</strong>
            <div className="change-review-comparison">
              <div><small>{t("Before")}</small><pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere", maxHeight: "18rem", overflow: "auto" }}>{effectText(effect.before)}</pre></div>
              <div><small>{t("After")}</small><pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere", maxHeight: "18rem", overflow: "auto" }}>{effectText(effect.after)}</pre></div>
            </div>
            {review && <blockquote><strong>{t(review.decision === "accept" ? "Accepted evidence" : "Rejected evidence")}</strong><p>{review.reason}</p></blockquote>}
          </section>
        );
      })}
      {compilation?.sections.map((section, index) => (
        <fieldset className="change-review-field" key={index} disabled={disabled}>
          <legend>{t("Edit Wiki summary")}</legend>
          <textarea
            aria-label={t("Edit Wiki summary {0}", index + 1)}
            value={section.summary}
            onChange={(event) => onEdit({ ...patch, compilation: { ...compilation, sections: compilation.sections.map((item, position) => position === index ? { ...item, summary: event.target.value } : item) } })}
          />
          <small>{t("Supporting evidence")} · {section.evidence_ids.join(", ")}</small>
        </fieldset>
      ))}
    </div>
  );
}

/** Format body text and structured record values for the three-way recovery comparison. */
function effectText(value: unknown): string {
  if (value == null) return t("Record absent");
  if (typeof value === "string") return value;
  if (typeof value === "object" && !Array.isArray(value)) {
    const record = value as Record<string, unknown>;
    if (typeof record.body === "string") return record.body;
    if (typeof record.content === "string") return record.content;
  }
  return JSON.stringify(value, null, 2);
}

/** Require an explicit decision for later edits while preserving each server-issued recovery effect. */
export function RecoveryFields({ operation, candidate, disabled, onEdit }: {
  operation: ChangeOperation;
  candidate: ChangeSet;
  disabled: boolean;
  onEdit: (value: unknown) => void;
}) {
  const effects = Array.isArray(operation.after_value) ? operation.after_value as ChangeEffect[] : [];
  return (
    <div className="change-review-fields">
      {effects.map((effect, index) => {
        const conflict = candidate.revert_conflicts?.find((item) => item.kind === effect.kind && item.id === effect.id);
        const record = (effect.before ?? effect.after) as Record<string, unknown> | null;
        const title = record && typeof record === "object" ? String(record.title || record.name || effect.id) : effect.id;
        return (
          <section key={`${effect.kind}-${effect.id}`}>
            <strong>{t("Recovery target")} · {title}</strong>
            <dl>
              {[
                [t("Current value"), conflict ? conflict.current : effect.after],
                [t("Value after original change"), effect.after],
                [t("Value to restore"), effect.before],
              ].map(([label, value]) => (
                <div key={String(label)}>
                  <dt>{String(label)}</dt>
                  <dd><pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere", maxHeight: "16rem", overflow: "auto" }}>{effectText(value)}</pre></dd>
                </div>
              ))}
            </dl>
            <Select
                aria-label={`${t("Recovery decision")} ${effect.id}`}
                disabled={disabled}
                value={effect.resolution || (conflict ? "" : "restore")}
                onChange={(event) => onEdit(effects.map((item, itemIndex) => itemIndex === index ? { ...item, resolution: event.target.value } : item))}
              >
                {conflict && <option value="">{t("Choose a recovery decision")}</option>}
                <option value="keep">{t("Keep current value")}</option>
                <option value="restore">{t("Restore previous value")}</option>
            </Select>
          </section>
        );
      })}
    </div>
  );
}
