import { useEffect, useState } from "react";
import { Badge, Button, ErrorBox } from "../../components/ui";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import type { ContextItem, Project } from "../../domain/types";
import { memoryKinds } from "../studio/Memories";

interface MaintenanceEntry {
  record_kind: string;
  id: string;
  title: string;
  reason: string;
  safe: boolean;
}
interface KnowledgeView {
  items: ContextItem[];
  maintenance: {
    revision: number;
    digest: string;
    pending_count: number;
    archive_count: number;
    review_count: number;
    entries: MaintenanceEntry[];
  };
  maintenance_receipts: {
    id: string;
    created_at: string;
    undone_at?: string;
    entries: MaintenanceEntry[];
  }[] | null;
}
interface Props {
  project: Project;
  onProject: (project: Project) => void;
  onChapter: (id: string) => void;
  onOpenTarget?: (kind: string, id: string) => void;
}
const reasons: Record<string, string> = {
  missing_source: "Source unavailable",
  stale_source: "Source changed",
  quote_missing: "Quote unavailable",
  unbound_source: "Source revision missing",
  exact_duplicate: "Exact duplicate",
  possible_conflict: "State needs checking",
};

/** Translate projection labels without changing the quoted story text or narrative claims. */
function readableKnowledgeText(text: string) {
  return text.split("\n").map((line) => {
    if (line.startsWith("State:")) {
      return line.replace("State:", t("State") + ":")
        .replace("Effective chapters:", t("Effective chapters") + ":")
        .replace("Story time:", t("Story time") + ":")
        .replace("Known by:", t("Known by") + ":")
        .replace("Due chapter:", t("Due chapter") + ":")
        .replace("not specified; do not assume public knowledge", t("Unspecified; do not assume everyone knows"));
    }
    if (line.startsWith("Quote:")) return line.replace("Quote:", t("Source quote") + ":");
    if (line.startsWith("Evidence type:")) return line.replace("Evidence type:", t("Evidence type") + ":");
    return line;
  }).join("\n");
}

/** Show the same effective knowledge used by writing, with grouped maintenance and reversible receipts. */
export function StoryKnowledgeOverview({ project, onProject, onChapter, onOpenTarget }: Props) {
  const lastChapter = Math.max(0, ...project.chapters.map((chapter) => chapter.number));
  const [through, setThrough] = useState(lastChapter);
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");
  const [view, setView] = useState<KnowledgeView | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => { setThrough(lastChapter); setQuery(""); setInput(""); }, [project.id]);
  useEffect(() => {
    let active = true;
    setView(null);
    setError("");
    api<KnowledgeView>("story_knowledge", {
      project_id: project.id, through_chapter: through, query, include_candidates: true,
    }).then((value) => { if (active) setView(value); })
      .catch((cause) => { if (active) setError(message(cause)); });
    return () => { active = false; };
  }, [project.id, project.revision, through, query]);

  /** Apply only the displayed digest or restore its unchanged records through revision-protected commands. */
  async function maintain(receiptId?: string) {
    if (!view || busy) return;
    setBusy(true);
    setError("");
    try {
      onProject(await api<Project>(receiptId ? "undo_memory_maintenance" : "apply_memory_maintenance", {
        project_id: project.id,
        revision: project.revision,
        ...(receiptId ? { receipt_id: receiptId } : { digest: view.maintenance.digest }),
      }));
    } catch (cause) { setError(message(cause)); }
    finally { setBusy(false); }
  }

  /** Navigate to source prose or the corresponding canonical setting while retaining the current search. */
  function open(item: ContextItem) {
    const chapter = project.chapters.find((value) => value.id === item.source_id);
    if (chapter) { onChapter(chapter.id); return; }
    const [prefix, id] = item.id.split(":");
    if (id && onOpenTarget) onOpenTarget(
      ({ E: "entity", N: "memory", L: "location", R: "route", P: "plan" } as Record<string, string>)[prefix] || "source", id,
    );
  }

  const groups = new Map<string, number>();
  for (const entry of view?.maintenance.entries || [])
    groups.set(entry.reason, (groups.get(entry.reason) || 0) + 1);
  return (
    <section className="knowledge-overview" aria-label={t("Effective story knowledge")}>
      <p className="muted">{t("Writing recalls effective rules, character states, relationships, clues and map constraints together. Candidate quotations remain source material, not confirmed facts.")}</p>
      <form className="knowledge-query" onSubmit={(event) => { event.preventDefault(); setQuery(input.trim()); }}>
        <label>{t("Search story knowledge")}<input value={input} onChange={(event) => setInput(event.target.value)} /></label>
        <label>{t("Through chapter")}<input type="number" min={0} max={lastChapter} value={through} onChange={(event) => setThrough(Math.max(0, Math.min(lastChapter, Number(event.target.value) || 0)))} /></label>
        <Button type="submit">{t("Search")}</Button>
      </form>
      {error && <ErrorBox>{error}</ErrorBox>}
      {view ? <>
        <div className="knowledge-summary">
          <span>{t("Retrieved records")} <Badge>{view.items.length}</Badge></span>
          <span>{t("Candidate records")} <Badge>{view.maintenance.pending_count}</Badge></span>
          <span>{t("Obsolete or duplicate")} <Badge>{view.maintenance.archive_count}</Badge></span>
        </div>
        <details className="knowledge-maintenance">
          <summary>{t("Maintain source records")}</summary>
          <p className="muted">{t("Archive only changed sources, missing quotations and exact duplicates. Interpretations stay separate; archiving can be undone.")}</p>
          <ul>{[...groups].map(([reason, count]) => <li key={reason}>{t(reasons[reason] || reason)} · {count}</li>)}</ul>
          <Button disabled={busy || !view.maintenance.archive_count} onClick={() => void maintain()}>{t("Archive obsolete candidates")}</Button>
          {(view.maintenance_receipts || []).slice(-10).reverse().map((receipt) => (
            <div className="knowledge-receipt" key={receipt.id}>
              <span>{receipt.created_at} · {receipt.entries.length}</span>
              {receipt.undone_at ? <Badge>{t("Undone")}</Badge> : <Button disabled={busy} onClick={() => void maintain(receipt.id)}>{t("Undo")}</Button>}
            </div>
          ))}
        </details>
        <div className="knowledge-records">
          {view.items.map((item) => (
            <article key={item.id}>
              <div className="knowledge-record-heading"><strong>{item.title}</strong><Badge>{t(memoryKinds[item.kind] || ({ entity: "Setting records", location: "Location", route: "Route", wiki: "Source evidence", candidate_source: "candidate_source" } as Record<string, string>)[item.kind] || item.kind)}</Badge></div>
              <p className="preserve">{readableKnowledgeText(item.text)}</p>
              <p className="muted">{t(item.reason || "")}</p>
              <Button onClick={() => open(item)}>{t("Open source or setting")}</Button>
            </article>
          ))}
          {!view.items.length && <p className="muted">{t("No relevant effective records. Search a character or place name.")}</p>}
        </div>
      </> : <p className="muted">{t("Loading…")}</p>}
    </section>
  );
}
