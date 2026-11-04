import { t } from "../../lib/i18n";
import { useEffect, useMemo, useState, lazy, Suspense } from "react";
import {
  IconPlus,
  IconSearch,
  IconBook2,
  IconCheck,
  IconX,
  IconArrowUpRight,
  IconUser,
  IconGitBranch,
  IconClock,
  IconMapPin,
  IconFileText,
} from "@tabler/icons-react";
import { api, contentHash, message } from "../../lib/api";
import {
  Badge,
  Button,
  Drawer,
  Empty,
  ErrorBox,
  Modal,
} from "../../components/ui";
import { StoryMemory } from "./StoryMemory";
const KnowledgeGraph = lazy(() =>
  import("./KnowledgeGraph").then((module) => ({
    default: module.KnowledgeGraph,
  })),
);
import {
  entityKinds,
  factKinds,
  type Entity,
  type Fact,
  type Project,
} from "../../domain/types";

interface Props {
  project: Project;
  onProject: (p: Project) => void;
  onChapter: (id: string) => void;
}

/** Organize entities, evidence, and chapter trajectories with explicit inference labels. */
export function Wiki({ project, onProject, onChapter }: Props) {
  const [tab, setTab] = useState("graph");
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState("all");
  const [selected, setSelected] = useState<string | null>(null);
  const [editing, setEditing] = useState<Entity | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [hashes, setHashes] = useState<Record<string, string>>({});
  const [source, setSource] = useState<Fact | null>(null);
  const [trajectory, setTrajectory] = useState("");
  useEffect(() => {
    let active = true;
    Promise.all(
      [
        ...project.chapters.map((c) => ({ id: c.id, text: c.body })),
        ...project.sources.map((s) => ({ id: s.id, text: s.content })),
      ].map(async (s) => [s.id, await contentHash(s.text)] as const),
    )
      .then((result) => {
        if (active) setHashes(Object.fromEntries(result));
      })
      .catch((e) => setError(message(e)));
    return () => {
      active = false;
    };
  }, [project]);
  const entity = project.entities.find((e) => e.id === selected);
  const entries = project.entities.filter(
    (e) =>
      (kind === "all" || e.kind === kind) &&
      `${e.name} ${e.aliases.join(" ")} ${e.description}`.includes(query),
  );
  const pending = project.facts.filter((f) => f.status === "pending");
  const characters = project.entities.filter((e) => e.kind === "character");
  const character =
    characters.find((e) => e.name === trajectory) || characters[0];
  const trajectoryFacts = useMemo(
    () =>
      project.facts
        .filter((f) => related(f, character?.name) && f.status !== "rejected")
        .sort((a, b) => a.chapter - b.chapter),
    [project, character],
  );

  /** Show evidence freshness against the current source revision. */
  function state(fact: Fact) {
    const text =
      project.chapters.find((c) => c.id === fact.source_id)?.body ??
      project.sources.find((s) => s.id === fact.source_id)?.content;
    return text === undefined
      ? "stale"
      : !hashes[fact.source_id]
        ? "checking"
        : hashes[fact.source_id] !== fact.source_hash ||
            !text.includes(fact.quote)
          ? "stale"
          : fact.status;
  }

  /** Submit each author review decision for backend source validation. */
  async function review(fact: Fact, status: string) {
    setError("");
    setBusy(true);
    try {
      onProject(
        await api<Project>("review_fact", {
          project_id: project.id,
          revision: project.revision,
          fact_id: fact.id,
          status,
        }),
      );
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  /** Save character and world entries using stable IDs through renames. */
  async function saveEntity() {
    if (!editing) return;
    setBusy(true);
    setError("");
    try {
      const next = await api<Project>("save_entity", {
        project_id: project.id,
        revision: project.revision,
        entity: editing,
      });
      onProject(next);
      setSelected(
        next.entities.find((e) => e.name === editing.name.trim())?.id || null,
      );
      setEditing(null);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  /** Display source, inference type, and author review state on an evidence card. */
  function factCard(fact: Fact, showEntity = false) {
    const status = state(fact);
    return (
      <article className="fact-card" key={fact.id}>
        <div className="fact-meta">
          {showEntity && <strong>{fact.entity}</strong>}
          <Badge tone="blue">{t(factKinds[fact.kind])}</Badge>
          <span>
            {fact.chapter ? t("Chapter {0}", fact.chapter) : t("Reference source")}
          </span>
          <Badge
            tone={
              status === "confirmed"
                ? "green"
                : status === "stale"
                  ? "red"
                  : "amber"
            }
          >
            {t(
              (
                {
                  confirmed: "Confirmed",
                  pending: "Pending review",
                  rejected: "Ignored",
                  stale: "Source changed",
                  checking: "Checking source",
                } as Record<string, string>
              )[status],
            )}
          </Badge>
        </div>
        <h4>{fact.claim}</h4>
        <div className="fact-context">
          {fact.story_time && (
            <span>
              <IconClock size={14} />
              {fact.story_time}
            </span>
          )}
          {fact.location && (
            <span>
              <IconMapPin size={14} />
              {fact.location}
            </span>
          )}
          {fact.related_entity && (
            <span>
              <IconGitBranch size={14} />
              {[fact.entity, fact.related_entity].map((name) => {
                const target = project.entities.find(
                  (entry) => entry.name === name,
                );
                return target ? (
                  <button
                    key={name}
                    className="text-button"
                    onClick={() => setSelected(target.id)}
                  >
                    {name}
                  </button>
                ) : (
                  <span key={name}>{name}</span>
                );
              })}
            </span>
          )}
        </div>
        <blockquote>{fact.quote}</blockquote>
        <div className="fact-footer">
          <button className="text-button" onClick={() => setSource(fact)}>
            {t("View source")}{" "}<IconArrowUpRight size={14} />
          </button>
          <span>
            {fact.epistemic === "inferred" ? t("Inferred") : t("Explicit")} ·{" "}
            {fact.model}
          </span>
        </div>
        {status === "pending" && (
          <div className="review-actions">
            <Button disabled={busy} onClick={() => review(fact, "rejected")}>
              <IconX size={15} />
              {t("Ignore")}
            </Button>
            <Button
              primary
              disabled={busy}
              onClick={() => review(fact, "confirmed")}
            >
              <IconCheck size={15} />
              {t("Confirm this analysis")}
            </Button>
          </div>
        )}
        {status === "confirmed" && (
          <button
            className="text-button review-revoke"
            disabled={busy}
            onClick={() => review(fact, "pending")}
          >
            {t("Revoke confirmation")}
          </button>
        )}
      </article>
    );
  }

  return (
    <section
      className={`workspace-page wiki-page ${tab === "graph" ? "wiki-graph-page" : ""}`}
    >
      <div className="section-title">
        <div>
          <p className="eyebrow">{t("YOUR LIVING STORY")}</p>
          <h1>{t("Character Wiki")}</h1>
          <p>{t("Follow a character through their choices.")}</p>
        </div>
        <Button
          primary
          onClick={() =>
            setEditing({
              id: "",
              name: "",
              kind: "character",
              aliases: [],
              description: "",
            })
          }
        >
          <IconPlus size={18} />
          {t("New entry")}
        </Button>
      </div>
      <div className="tabs">
        <button
          className={tab === "graph" ? "active" : ""}
          onClick={() => setTab("graph")}
        >
          <IconGitBranch size={17} />
          {t("Knowledge graph")}
        </button>
        <button
          className={tab === "entries" ? "active" : ""}
          onClick={() => setTab("entries")}
        >
          <IconBook2 size={17} />
          {t("All entries")}{" "}<span>{project.entities.length}</span>
        </button>
        <button
          className={tab === "trajectory" ? "active" : ""}
          onClick={() => setTab("trajectory")}
        >
          <IconGitBranch size={17} />
          {t("Character trajectories")}
        </button>
        <button
          className={tab === "review" ? "active" : ""}
          onClick={() => setTab("review")}
        >
          <IconCheck size={17} />
          {t("Pending review")}{" "}<span>{pending.length}</span>
        </button>
        <button
          className={tab === "memory" ? "active" : ""}
          onClick={() => setTab("memory")}
        >
          <IconBook2 size={17} />
          {t("Story memory")}
        </button>
        <button
          className={tab === "health" ? "active" : ""}
          onClick={() => setTab("health")}
        >
          <IconCheck size={17} />
          {t("Knowledge checks")}
        </button>
      </div>
      {error && <ErrorBox>{error}</ErrorBox>}
      {tab === "graph" && (
        <Suspense fallback={<div className="loading">{t("Loading graph…")}</div>}>
          <KnowledgeGraph
            project={project}
            onEntity={setSelected}
            onChapter={onChapter}
          />
        </Suspense>
      )}
      {(tab === "memory" || tab === "health") && (
        <StoryMemory
          project={project}
          view={tab}
          onProject={onProject}
          onChapter={onChapter}
          onReview={() => setTab("review")}
        />
      )}
      {tab === "entries" && (
        <>
          <div className="list-toolbar">
            <label className="search">
              <IconSearch size={18} />
              <input
                aria-label={t("Search Wiki")}
                placeholder={t("Search characters, locations, or worldbuilding…")}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </label>
            <select
              aria-label={t("Entry type")}
              value={kind}
              onChange={(e) => setKind(e.target.value)}
            >
              <option value="all">{t("All types")}</option>
              {Object.entries(entityKinds).map(([key, label]) => (
                <option key={key} value={key}>
                  {t(label)}
                </option>
              ))}
            </select>
            <span className="muted">{entries.length}{" "}{t("entries")}</span>
          </div>
          {entries.length ? (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>{t("Name")}</th>
                    <th>{t("Type")}</th>
                    <th>{t("Author setup")}</th>
                    <th>{t("Linked evidence")}</th>
                    <th>{t("Pending review")}</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {entries.map((e) => (
                    <tr key={e.id}>
                      <td>
                        <button
                          className="entity-link"
                          onClick={() => setSelected(e.id)}
                        >
                          <span className="entity-icon">
                            {e.kind === "character" ? (
                              <IconUser size={20} />
                            ) : (
                              <IconBook2 size={20} />
                            )}
                          </span>
                          <span>
                            <strong>{e.name}</strong>
                            <small>{e.aliases.join(" / ")}</small>
                          </span>
                        </button>
                      </td>
                      <td>
                        <Badge>{t(entityKinds[e.kind])}</Badge>
                      </td>
                      <td>
                        <p className="table-description">
                          {e.description || t("Add details when you are ready")}
                        </p>
                      </td>
                      <td>
                        {
                          project.facts.filter(
                            (f) =>
                              related(f, e.name) && f.status !== "rejected",
                          ).length
                        }{" "}
                        {t("records")}
                      </td>
                      <td>
                        {pending.filter((f) => related(f, e.name)).length ||
                          "—"}
                      </td>
                      <td>
                        <button
                          className="icon-button"
                          aria-label={t("View {0}", e.name)}
                          onClick={() => setSelected(e.id)}
                        >
                          <IconArrowUpRight size={18} />
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <div className="table-footer">
                {t("Total")}{" "}{entries.length}{" "}{t("entries · Author setup and chapter evidence are maintained separately")}
              </div>
            </div>
          ) : (
            <Empty
              title={
                query
                  ? t("No matching entries")
                  : t("Bring your characters and world together")
              }
              detail={t("Create a character or use Compile Wiki in the writing workspace to analyze a chapter.")}
            />
          )}
        </>
      )}
      {tab === "review" && (
        <div className="review-list">
          <div className="info-strip">
            <IconBook2 size={18} />
            {t("Review the quote and its interpretation before confirming. Current, confirmed evidence enters the AI Wiki context.")}
          </div>
          {pending.length ? (
            pending.map((f) => factCard(f, true))
          ) : (
            <Empty
              title={t("The review queue is clear")}
              detail={t("New evidence will appear here after a chapter analysis completes.")}
            />
          )}
        </div>
      )}
      {tab === "trajectory" &&
        (character ? (
          <div className="trajectory-layout">
            <div className="trajectory-people">
              {characters.map((c) => (
                <button
                  key={c.id}
                  className={c.name === character.name ? "active" : ""}
                  onClick={() => setTrajectory(c.name)}
                >
                  <IconUser size={20} />
                  <span>{c.name}</span>
                  <small>
                    {
                      project.facts.filter(
                        (f) => related(f, c.name) && f.status !== "rejected",
                      ).length
                    }
                  </small>
                </button>
              ))}
            </div>
            <div className="trajectory-main">
              <div className="trajectory-title">
                <div>
                  <h2>{character.name}{t("trajectory")}</h2>
                  <p>{character.description}</p>
                </div>
                <Badge>{t("In chapter order")}</Badge>
              </div>
              <p className="muted">
                {t("Actions, relationships, and emotional changes link to their source. Chapter order may differ from in-story chronology.")}
              </p>
              <div className="timeline">
                {trajectoryFacts.length ? (
                  trajectoryFacts.map((f) => (
                    <div className="timeline-entry" key={f.id}>
                      <div className="timeline-marker">
                        <IconFileText size={16} />
                      </div>
                      {factCard(f)}
                    </div>
                  ))
                ) : (
                  <Empty
                    title={t("No character trajectory yet")}
                    detail={t("Analyze a chapter that includes this character in the writing workspace.")}
                  />
                )}
              </div>
            </div>
          </div>
        ) : (
          <Empty
            title={t("Start with a character")}
            detail={t("Create a character entry, or extract characters from a chapter to view their trajectories.")}
          />
        ))}
      {entity && (
        <Drawer
          title={entity.name}
          subtitle={t("Character Wiki / Entry details")}
          onClose={() => setSelected(null)}
        >
          <div className="drawer-subtitle">
            <Badge tone="blue">{t(entityKinds[entity.kind])}</Badge>
            <span>
              {entity.aliases.length
                ? t("Aliases: {0}", entity.aliases.join("、"))
                : t("No aliases")}
            </span>
            <Button
              onClick={() =>
                setEditing({ ...entity, aliases: [...entity.aliases] })
              }
            >
              {t("Edit setup")}
            </Button>
          </div>
          <div className="drawer-section">
            <h3>{t("Author setup")}</h3>
            <p className="preserve">
              {entity.description || t("No author setup yet.")}
            </p>
          </div>
          <div className="drawer-section">
            <h3>{t("Trajectory and evidence")}</h3>
            {project.facts
              .filter((f) => related(f, entity.name) && f.status !== "rejected")
              .sort((a, b) => a.chapter - b.chapter)
              .map((f) => factCard(f))}
            {!project.facts.some(
              (f) => related(f, entity.name) && f.status !== "rejected",
            ) && (
              <p className="muted">
                {t("Analyze a chapter to see this entry's evidence and changes.")}
              </p>
            )}
          </div>
        </Drawer>
      )}
      {editing && (
        <Modal
          title={editing.id ? t("Edit Wiki entry") : t("New Wiki entry")}
          onClose={() => setEditing(null)}
        >
          <form
            className="modal-content"
            onSubmit={(e) => {
              e.preventDefault();
              void saveEntity();
            }}
          >
            <div className="form-grid">
              <label>
                {t("Name")}
                <input
                  autoFocus
                  value={editing.name}
                  onChange={(e) =>
                    setEditing({ ...editing, name: e.target.value })
                  }
                  maxLength={120}
                  required
                />
              </label>
              <label>
                {t("Type")}
                <select
                  value={editing.kind}
                  onChange={(e) =>
                    setEditing({ ...editing, kind: e.target.value })
                  }
                >
                  {Object.entries(entityKinds).map(([key, label]) => (
                    <option key={key} value={key}>
                      {t(label)}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <label>
              {t("Aliases (comma-separated)")}
              <input
                value={editing.aliases.join("，")}
                onChange={(e) =>
                  setEditing({
                    ...editing,
                    aliases: e.target.value
                      .split(/[,，]/)
                      .map((a) => a.trim())
                      .filter(Boolean),
                  })
                }
              />
            </label>
            <label>
              {t("Author setup")}
              <textarea
                className="large-input"
                value={editing.description}
                onChange={(e) =>
                  setEditing({ ...editing, description: e.target.value })
                }
                placeholder={t("Identity, wants, beliefs, relationships, and creative boundaries…")}
              />
            </label>
            {error && <ErrorBox>{error}</ErrorBox>}
            <div className="modal-actions">
              <Button onClick={() => setEditing(null)}>{t("Cancel")}</Button>
              <Button
                primary
                type="submit"
                disabled={busy || !editing.name.trim()}
              >
                {t("Save entry")}
              </Button>
            </div>
          </form>
        </Modal>
      )}
      {source && (
        <Modal title={t("Back to source")} wide onClose={() => setSource(null)}>
          <div className="modal-content">
            <Badge tone={state(source) === "stale" ? "red" : "blue"}>
              {state(source) === "stale"
                ? t("Quote from an earlier version")
                : t("Current-version evidence")}
            </Badge>
            <blockquote className="source-quote">{source.quote}</blockquote>
            <h4>{t("Current source text")}</h4>
            <pre className="source-text">
              {project.chapters.find((c) => c.id === source.source_id)?.body ||
                project.sources.find((s) => s.id === source.source_id)
                  ?.content ||
                t("Source not found")}
            </pre>
            {project.chapters.some((c) => c.id === source.source_id) && (
              <div className="modal-actions">
                <Button
                  primary
                  onClick={() => {
                    setSource(null);
                    setSelected(null);
                    onChapter(source.source_id);
                  }}
                >
                  {t("Open this chapter")}
                </Button>
              </div>
            )}
          </div>
        </Modal>
      )}
    </section>
  );
}

/** Show relationship evidence at both ends of the same connection. */
function related(fact: Fact, name?: string) {
  return fact.entity === name || fact.related_entity === name;
}
