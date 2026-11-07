import { useEffect, useState } from "react";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import {
  Badge,
  Button,
  Empty,
  ErrorBox,
  MarkdownResponse,
  Modal,
} from "../../components/ui";
import { Select } from "../../components/Select";
import type { Project } from "../../domain/types";
import type { StoryMemory } from "../../domain/studio";
import { memorySuccessors, memoryVisibleAt } from "../../domain/memory";

export const memoryKinds: Record<string, string> = {
  rule: "Rule",
  setting: "Setting",
  fact: "Fact",
  inference: "Inference",
  plan: "Planned",
  character: "Character state",
  relationship: "Relationship",
  knowledge: "Character knowledge",
  clue: "Clue",
  promise: "Promise",
  summary: "Summary",
};
const memoryStates: Record<string, string> = {
  active: "Active",
  planned: "Planned",
  planted: "Planted",
  reinforced: "Reinforced",
  resolved: "Resolved",
  fulfilled: "Fulfilled",
  abandoned: "Abandoned",
  alive: "Alive",
  dead: "Dead",
};

/** Initialize optional narrative metadata explicitly for manual or evidence-linked memories. */
function blankMemory(kind = "setting"): StoryMemory {
  return {
    id: "",
    kind,
    entity: "",
    title: "",
    content: "",
    state: "active",
    status: "confirmed",
    chapter: 0,
    through_chapter: 0,
    due_chapter: 0,
    story_time: "",
    known_by: [],
    source_id: "",
    source_hash: "",
    quote: "",
    session_id: "",
    supersedes: "",
    updated_at: "",
  };
}

/** Manage author-visible canon, changing state and open narrative promises with retained provenance. */
export function Memories({
  project,
  onProject,
  onWiki,
  focusId,
}: {
  project: Project;
  onProject: (project: Project) => void;
  onWiki: () => void;
  focusId?: string;
}) {
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState("all");
  const [snapshotChapter, setSnapshotChapter] = useState("");
  const [editing, setEditing] = useState<StoryMemory | null>(null);
  const [base, setBase] = useState(project.revision);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const focused = project.studio.memories.find((item) => item.id === focusId);
    if (focused) {
      setQuery(focused.title);
      setKind("all");
    }
  }, [focusId]);

  /** Bind an editing session to the revision the author inspected. */
  function open(memory: StoryMemory) {
    setEditing(memory);
    setBase(project.revision);
    setError("");
  }

  /** Persist a reviewed memory and retain previous versions through the backend's supersede operation. */
  async function save(memory: StoryMemory, revision = base) {
    setBusy(true);
    setError("");
    try {
      onProject(
        await api<Project>("save_memory", {
          project_id: project.id,
          revision,
          memory,
        }),
      );
      setEditing(null);
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  const atChapter =
    snapshotChapter === ""
      ? null
      : Math.max(1, Math.floor(Number(snapshotChapter) || 1));
  const successors = memorySuccessors(project.studio.memories);
  const filtered = project.studio.memories.filter(
    (memory) =>
      (kind === "all" || memory.kind === kind) &&
      (atChapter === null || memoryVisibleAt(memory, atChapter, successors)) &&
      `${memory.title} ${memory.entity} ${memory.content}`
        .toLocaleLowerCase()
        .includes(query.toLocaleLowerCase()),
  );
  return (
    <section className="studio-section">
      <div className="studio-section-heading">
        <div>
          <h2>{t("Story memory")}</h2>
          <p className="muted">
            {t("The author decides whether a hint is a real continuity issue.")}
          </p>
        </div>
        <div className="studio-actions">
          <Button onClick={onWiki}>{t("Open Character Wiki")}</Button>
          <Button primary onClick={() => open(blankMemory())}>
            {t("Add memory")}
          </Button>
        </div>
      </div>
      <div className="studio-actions">
        <input
          aria-label={t("Search memory")}
          placeholder={t("Search memory")}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <Select
          aria-label={t("Memory kind")}
          value={kind}
          onChange={(event) => setKind(event.target.value)}
        >
          <option value="all">{t("All kinds")}</option>
          {Object.entries(memoryKinds).map(([id, title]) => (
            <option key={id} value={id}>
              {t(title)}
            </option>
          ))}
        </Select>
        <label>
          {t("View memory at chapter")}
          <input
            type="number"
            min={1}
            step={1}
            value={snapshotChapter}
            placeholder={t("All versions")}
            onChange={(event) => setSnapshotChapter(event.target.value)}
          />
        </label>
        <Button onClick={() => open(blankMemory("rule"))}>
          {t("World and rules")}
        </Button>
        <Button onClick={() => open(blankMemory("clue"))}>
          {t("Clues and payoffs")}
        </Button>
      </div>
      {!filtered.length && (
        <Empty
          title={t("No memory yet")}
          detail={t("No changes are applied until you accept them.")}
        />
      )}
      {error && !editing && <ErrorBox>{error}</ErrorBox>}
      <div className="memory-grid">
        {filtered.map((memory) => (
          <article className={`memory-card ${memory.status}`} key={memory.id}>
            <header>
              <Badge>{t(memoryKinds[memory.kind])}</Badge>
              <Badge tone={memory.status === "confirmed" ? "green" : "gray"}>
                {t(
                  memory.status === "confirmed"
                    ? "Confirmed"
                    : memory.status === "superseded"
                      ? "Superseded"
                      : memory.status === "rejected"
                        ? "Rejected"
                        : "Proposed",
                )}
              </Badge>
            </header>
            <h3>{memory.title || memory.entity}</h3>
            <MarkdownResponse text={memory.content} />
            <p className="muted">
              {memory.entity} · {t(memoryStates[memory.state] || memory.state)}{" "}
              · {t("Chapter")} {memory.chapter}
              {memory.through_chapter > 0 && `–${memory.through_chapter}`}
              {memory.due_chapter > 0 &&
                ` · ${t("Due chapter")}: ${memory.due_chapter}`}
            </p>
            {memory.story_time && (
              <p className="muted">
                {t("Story time")}: {memory.story_time}
              </p>
            )}
            {!!memory.known_by?.length && (
              <p>
                {t("Known by")}: {memory.known_by.join("、")}
              </p>
            )}
            {memory.quote && <blockquote>{memory.quote}</blockquote>}
            {memory.source_id && (
              <small>
                {t("Source")}:{" "}
                {project.chapters.find(
                  (chapter) => chapter.id === memory.source_id,
                )?.title ||
                  project.sources.find(
                    (source) => source.id === memory.source_id,
                  )?.title ||
                  memory.source_id}
              </small>
            )}
            <div className="studio-actions">
              <Button
                disabled={!["confirmed", "proposed"].includes(memory.status)}
                title={
                  !["confirmed", "proposed"].includes(memory.status)
                    ? t("Archived memory versions are read-only")
                    : undefined
                }
                onClick={() => open(memory)}
              >
                {t("Edit")}
              </Button>
              {memory.status === "proposed" && (
                <Button
                  disabled={busy}
                  onClick={() =>
                    void save(
                      { ...memory, status: "confirmed" },
                      project.revision,
                    )
                  }
                >
                  {t("Accept")}
                </Button>
              )}
              {memory.status !== "rejected" && (
                <Button
                  disabled={busy}
                  onClick={() =>
                    void save(
                      { ...memory, status: "rejected" },
                      project.revision,
                    )
                  }
                >
                  {t("Reject")}
                </Button>
              )}
            </div>
          </article>
        ))}
      </div>
      {editing && (
        <Modal
          title={t(editing.id ? "Edit memory" : "Add memory")}
          onClose={() => setEditing(null)}
          wide
        >
          <form
            className="modal-content studio-form"
            onSubmit={(event) => {
              event.preventDefault();
              void save(editing);
            }}
          >
            <div className="form-grid">
              <label>
                {t("Memory kind")}
                <Select
                  value={editing.kind}
                  onChange={(event) =>
                    setEditing({ ...editing, kind: event.target.value })
                  }
                >
                  {Object.entries(memoryKinds).map(([id, title]) => (
                    <option value={id} key={id}>
                      {t(title)}
                    </option>
                  ))}
                </Select>
              </label>
              <label>
                {t("State")}
                <Select
                  value={editing.state}
                  onChange={(event) =>
                    setEditing({ ...editing, state: event.target.value })
                  }
                >
                  {Object.entries(memoryStates).map(([id, title]) => (
                    <option value={id} key={id}>
                      {t(title)}
                    </option>
                  ))}
                </Select>
              </label>
            </div>
            <div className="form-grid">
              <label>
                {t("Title")}
                <input
                  value={editing.title}
                  onChange={(event) =>
                    setEditing({ ...editing, title: event.target.value })
                  }
                  maxLength={120}
                  required
                />
              </label>
              <label>
                {t("Entity")}
                <input
                  value={editing.entity}
                  onChange={(event) =>
                    setEditing({ ...editing, entity: event.target.value })
                  }
                />
              </label>
            </div>
            <label>
              {t("Content")}
              <textarea
                value={editing.content}
                onChange={(event) =>
                  setEditing({ ...editing, content: event.target.value })
                }
                required
                maxLength={12000}
              />
            </label>
            <div className="form-grid">
              {(
                [
                  ["chapter", "Chapter"],
                  ["through_chapter", "Effective through chapter"],
                  ["due_chapter", "Due chapter"],
                ] as const
              ).map(([key, title]) => (
                <label key={key}>
                  {t(title)}
                  <input
                    type="number"
                    min={0}
                    value={editing[key]}
                    onChange={(event) =>
                      setEditing({
                        ...editing,
                        [key]: Number(event.target.value),
                      })
                    }
                  />
                </label>
              ))}
            </div>
            <div className="form-grid">
              <label>
                {t("Story time")}
                <input
                  value={editing.story_time}
                  onChange={(event) =>
                    setEditing({ ...editing, story_time: event.target.value })
                  }
                />
              </label>
              <label>
                {t("Known by")}
                <textarea
                  value={editing.known_by?.join("\n") || ""}
                  onChange={(event) =>
                    setEditing({
                      ...editing,
                      known_by: event.target.value.split("\n").filter(Boolean),
                    })
                  }
                  placeholder={t("Only these characters know; one per line")}
                />
              </label>
            </div>
            <label>
              {t("Source")}
              <Select
                value={editing.source_id}
                onChange={(event) =>
                  setEditing({
                    ...editing,
                    source_id: event.target.value,
                    source_hash: "",
                    quote: "",
                  })
                }
              >
                <option value="">{t("Author setting")}</option>
                {project.chapters.map((chapter) => (
                  <option value={chapter.id} key={chapter.id}>
                    {chapter.number} · {chapter.title}
                  </option>
                ))}
                {project.sources.map((source) => (
                  <option value={source.id} key={source.id}>
                    {source.title}
                  </option>
                ))}
              </Select>
            </label>
            <label>
              {t("Source quote")}
              <textarea
                value={editing.quote}
                onChange={(event) =>
                  setEditing({ ...editing, quote: event.target.value })
                }
              />
            </label>
            {error && <ErrorBox>{error}</ErrorBox>}
            <div className="modal-actions">
              <Button onClick={() => setEditing(null)}>{t("Cancel")}</Button>
              <Button primary type="submit" disabled={busy}>
                {t("Save")}
              </Button>
            </div>
          </form>
        </Modal>
      )}
    </section>
  );
}
