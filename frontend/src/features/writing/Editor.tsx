import { t } from "../../lib/i18n";
import { useEffect, useRef, useState } from "react";
import {
  IconHistory,
  IconDeviceFloppy,
  IconPlus,
  IconBook2,
  IconLayoutSidebarRightCollapse,
  IconLayoutSidebarRightExpand,
} from "@tabler/icons-react";
import { api, contentHash, date, message, wordCount } from "../../lib/api";
import { Assistant } from "../assistant/Assistant";
import {
  Badge,
  Button,
  Empty,
  ErrorBox,
  MarkdownResponse,
  Modal,
} from "../../components/ui";
import { WritingParameters } from "./WritingParameters";
import { ProjectModels } from "../settings/models";
import type { Chapter, Job, Project } from "../../domain/types";

interface Props {
  project: Project;
  chapter?: Chapter;
  onProject: (p: Project) => void;
  onChapter: (id: string) => void;
  onSettings: () => void;
  onWiki: () => void;
  seed: string;
}
type Draft = Pick<
  Chapter,
  "title" | "body" | "synopsis" | "status" | "target_words"
>;

/** Restore uncommitted local drafts across navigation or reload. */
function readDraft(key: string, chapter: Chapter): Draft {
  try {
    const value = JSON.parse(localStorage.getItem(key) || "null");
    if (
      value &&
      ["title", "body", "synopsis", "status"].every(
        (k) => typeof value[k] === "string",
      )
    )
      return {
        ...value,
        target_words:
          value.target_words === undefined
            ? chapter.target_words
            : value.target_words,
      };
  } catch {
    /* Retain database text when a local draft is corrupted. */
  }
  return {
    title: chapter.title,
    body: chapter.body,
    synopsis: chapter.synopsis,
    status: chapter.status,
    target_words: chapter.target_words,
  };
}

/** Provide chapter navigation, an editor, and a collapsible AI panel. */
export function Writing({
  project,
  chapter,
  onProject,
  onChapter,
  onSettings,
  onWiki,
  seed,
}: Props) {
  const [adding, setAdding] = useState(false);
  const [title, setTitle] = useState("");
  const [targetWords, setTargetWords] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  /** Create chapter numbers through the backend to avoid conflicting allocation. */
  async function add() {
    setBusy(true);
    setError("");
    try {
      const next = await api<Project>("new_chapter", {
        project_id: project.id,
        revision: project.revision,
        title,
        target_words: targetWords ? Number(targetWords) : null,
      });
      onProject(next);
      onChapter(next.chapters.at(-1)!.id);
      setAdding(false);
      setTitle("");
      setTargetWords("");
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="writing-layout">
      <nav className="chapter-nav" aria-label={t("Chapter list")}>
        <div className="chapter-nav-title">
          <span>{t("Chapter list")}</span>
          <button
            className="icon-button"
            onClick={() => setAdding(true)}
            aria-label={t("New chapter")}
          >
            <IconPlus size={18} />
          </button>
        </div>
        <div className="volume-label">
          {t("Whole book")}{" "}
          <span>
            {project.chapters.length}{" "}
            {project.chapters.length === 1 ? t("chapter") : t("chapters")}
          </span>
        </div>
        {project.chapters.map((c) => (
          <button
            key={c.id}
            className={`chapter-item ${chapter?.id === c.id ? "active" : ""}`}
            onClick={() => onChapter(c.id)}
          >
            <span className="chapter-number">
              {String(c.number).padStart(2, "0")}
            </span>
            <div>
              <strong>{c.title}</strong>
              <small>
                {wordCount(c.body).toLocaleString()} {t("characters ·")}{" "}
                {c.status === "final" ? t("Final") : t("Draft")}
              </small>
            </div>
          </button>
        ))}
        <button className="new-chapter" onClick={() => setAdding(true)}>
          <IconPlus size={16} />
          {t("Add chapter")}
        </button>
        <div className="chapter-nav-footer">
          <IconBook2 size={17} />
          <span>{t("Give every detail a place")}</span>
        </div>
      </nav>
      {chapter ? (
        <Editor
          key={`${project.id}-${chapter.id}`}
          {...{
            project,
            chapter,
            onProject,
            onChapter,
            onSettings,
            onWiki,
            seed,
          }}
        />
      ) : (
        <div className="fill">
          <Empty
            title={t("Your story begins with chapter one")}
            detail={t(
              "Write a few sentences, or explore your ideas in Story planning.",
            )}
          >
            <Button primary onClick={() => setAdding(true)}>
              <IconPlus size={17} />
              {t("Create the first chapter")}
            </Button>
          </Empty>
        </div>
      )}
      {adding && (
        <Modal title={t("New chapter")} onClose={() => setAdding(false)}>
          <form
            className="modal-content"
            onSubmit={(e) => {
              e.preventDefault();
              void add();
            }}
          >
            <label>
              {t("Chapter title")}
              <input
                autoFocus
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                placeholder={t("For example, After the Tide")}
                maxLength={120}
              />
            </label>
            <label>
              {t("Chapter target characters (optional)")}
              <input
                type="number"
                min={100}
                max={50000}
                step={100}
                value={targetWords}
                placeholder={t(
                  "Inherit {0} characters",
                  project.default_chapter_words,
                )}
                onChange={(event) => setTargetWords(event.target.value)}
              />
            </label>
            {error && <ErrorBox>{error}</ErrorBox>}
            <div className="modal-actions">
              <Button onClick={() => setAdding(false)}>{t("Cancel")}</Button>
              <Button primary type="submit" disabled={busy || !title.trim()}>
                {t("Create chapter")}
              </Button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  );
}

/** Retain unsaved drafts and verify the source revision before applying AI output. */
function Editor({
  project,
  chapter,
  onProject,
  onSettings,
  onWiki,
  seed,
}: Props & { chapter: Chapter }) {
  const draftKey = `quillweave-draft-${project.id}-${chapter.id}`;
  const [draft, setDraftState] = useState<Draft>(() =>
    readDraft(draftKey, chapter),
  );
  const draftRef = useRef(draft);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [history, setHistory] = useState(false);
  const [showAI, setShowAI] = useState(true);
  const [showPlan, setShowPlan] = useState(false);
  const [selectedVersion, setSelectedVersion] = useState<string>("");
  const dirty =
    draft.title !== chapter.title ||
    draft.body !== chapter.body ||
    draft.synopsis !== chapter.synopsis ||
    draft.status !== chapter.status ||
    draft.target_words !== chapter.target_words;

  useEffect(() => {
    if (!dirty) {
      localStorage.removeItem(draftKey);
      return;
    }
    try {
      localStorage.setItem(draftKey, JSON.stringify(draft));
    } catch {
      setError("Local draft storage is full. Save the chapter soon.");
    }
  }, [draft, dirty, draftKey]);
  useEffect(() => {
    /** Prompt before closing a browser with unsaved edits. */
    function preventLoss(event: BeforeUnloadEvent) {
      if (dirty) {
        event.preventDefault();
        event.returnValue = "";
      }
    }
    window.addEventListener("beforeunload", preventLoss);
    return () => window.removeEventListener("beforeunload", preventLoss);
  }, [dirty]);

  /** Keep the latest draft available for checks after asynchronous work. */
  function setDraft(next: Draft | ((previous: Draft) => Draft)) {
    const value = typeof next === "function" ? next(draftRef.current) : next;
    draftRef.current = value;
    setDraftState(value);
  }

  /** Save a revision while letting current dirty state own draft-cache cleanup. */
  async function save(): Promise<Project | null> {
    if (!dirty) return project;
    if (saving) return null;
    setSaving(true);
    setError("");
    try {
      const next = await api<Project>("save_chapter", {
        project_id: project.id,
        revision: project.revision,
        chapter_id: chapter.id,
        ...draft,
      });
      onProject(next);
      return next;
    } catch (e) {
      setError(message(e));
      return null;
    } finally {
      setSaving(false);
    }
  }

  /** Refresh derived project data while retaining ongoing local edits. */
  async function refresh() {
    onProject(await api<Project>("get_project", { project_id: project.id }));
  }

  /** Reject outdated AI output after the author edits the source chapter. */
  async function apply(job: Job) {
    const sourceBody = draftRef.current.body;
    const sourceHash = await contentHash(sourceBody);
    if (
      sourceHash !== job.prompt.source_hash ||
      draftRef.current.body !== sourceBody
    )
      throw new Error(
        "The chapter changed after generation. Copy the parts you need, or generate from the current draft.",
      );
    setDraft((previous) => ({
      ...previous,
      body:
        job.action === "continue"
          ? `${previous.body}${previous.body ? "\n\n" : ""}${job.text}`
          : job.text,
      status: "draft",
    }));
  }

  /** Save the current draft before restoring an earlier version. */
  async function restore() {
    const saved = await save();
    if (!saved) return;
    try {
      const next = await api<Project>("restore_version", {
        project_id: saved.id,
        revision: saved.revision,
        chapter_id: chapter.id,
        version_id: selectedVersion,
      });
      onProject(next);
      const c = next.chapters.find((c) => c.id === chapter.id)!;
      setDraft({
        title: c.title,
        body: c.body,
        synopsis: c.synopsis,
        status: c.status,
        target_words: c.target_words,
      });
      setHistory(false);
    } catch (e) {
      setError(message(e));
    }
  }
  const version = chapter.versions.find((v) => v.id === selectedVersion);
  return (
    <div className={`editor-and-assistant ${showAI ? "" : "focus-mode"}`}>
      <section className="editor-panel">
        <header className="editor-toolbar">
          <div>
            <span>
              {t("Chapter")} {chapter.number}
            </span>
            <Badge tone={dirty ? "amber" : "green"}>
              {dirty ? t("Unsaved") : t("Saved locally")}
            </Badge>
          </div>
          <div className="toolbar-actions">
            <button
              className="icon-button"
              title={t("Version history")}
              aria-label={t("Version history")}
              onClick={() => {
                setSelectedVersion(chapter.versions.at(-1)?.id || "");
                setHistory(true);
              }}
            >
              <IconHistory size={18} />
            </button>
            <button
              className="icon-button"
              aria-label={
                showAI
                  ? t("Collapse writing assistant")
                  : t("Expand writing assistant")
              }
              onClick={() => setShowAI(!showAI)}
            >
              {showAI ? (
                <IconLayoutSidebarRightCollapse size={19} />
              ) : (
                <IconLayoutSidebarRightExpand size={19} />
              )}
            </button>
            <Button primary disabled={!dirty || saving} onClick={() => save()}>
              <IconDeviceFloppy size={16} />
              {saving ? t("Saving") : t("Save")}
            </Button>
          </div>
        </header>
        {error && (
          <div className="editor-error">
            <ErrorBox>{error}</ErrorBox>
            <button
              className="text-button"
              onClick={() => refresh().catch((e) => setError(message(e)))}
            >
              {t("Refresh project status (keep draft)")}
            </button>
          </div>
        )}
        <div className="editor-scroll">
          <div className="manuscript">
            <div className="manuscript-eyebrow">
              {t("CHAPTER")} {String(chapter.number).padStart(2, "0")}
            </div>
            <input
              className="chapter-title-input"
              aria-label={t("Chapter title")}
              value={draft.title}
              onChange={(e) => setDraft({ ...draft, title: e.target.value })}
              maxLength={120}
            />
            <div className="manuscript-meta">
              <select
                aria-label={t("Chapter status")}
                value={draft.status}
                onChange={(e) => setDraft({ ...draft, status: e.target.value })}
              >
                <option value="draft">{t("Draft")}</option>
                <option value="final">{t("Final")}</option>
              </select>
              <span>
                {wordCount(draft.body).toLocaleString()} /{" "}
                {(
                  draft.target_words ?? project.default_chapter_words
                ).toLocaleString()}{" "}
                {t("characters")}
              </span>
              <label className="chapter-word-target">
                {t("Chapter target")}
                <input
                  aria-label={t("Chapter target characters")}
                  type="number"
                  min={100}
                  max={50000}
                  step={100}
                  value={draft.target_words ?? ""}
                  placeholder={t("Inherit {0}", project.default_chapter_words)}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      target_words:
                        e.target.value === "" ? null : Number(e.target.value),
                    })
                  }
                />
              </label>
              <button
                className="text-button"
                onClick={() => setShowPlan(!showPlan)}
              >
                {showPlan ? t("Collapse scene plan") : t("Scene plan")}
              </button>
            </div>
            {showPlan && (
              <label className="scene-plan">
                {t("What changes in this chapter?")}
                <textarea
                  aria-label={t("Scene plan")}
                  value={draft.synopsis}
                  onChange={(e) =>
                    setDraft({ ...draft, synopsis: e.target.value })
                  }
                  placeholder={t(
                    "Character goals, obstacles, choices, consequences…",
                  )}
                />
              </label>
            )}
            <textarea
              className="manuscript-body"
              aria-label={t("Chapter text")}
              placeholder={t("Write your story here…")}
              spellCheck={false}
              value={draft.body}
              onChange={(e) => setDraft({ ...draft, body: e.target.value })}
            />
            <div className="chapter-end">{t("— The story continues —")}</div>
          </div>
        </div>
        <footer className="editor-footer">
          <span>
            {dirty
              ? t("Draft stored on this device · Save to add it to the project")
              : t("Chapter text and versions are stored locally")}
          </span>
          <button className="text-button" onClick={onWiki}>
            <IconBook2 size={15} />
            {t("Character Wiki")}
          </button>
        </footer>
      </section>
      {showAI && (
        <Assistant
          project={project}
          chapter={chapter}
          seed={seed}
          onSettings={onSettings}
          beforeStart={save}
          onRefresh={refresh}
          onApply={apply}
        />
      )}
      {history && (
        <Modal
          title={t("Chapter version history")}
          wide
          onClose={() => setHistory(false)}
        >
          <div className="modal-content">
            {!chapter.versions.length ? (
              <Empty
                title={t("No earlier versions yet")}
                detail={t(
                  "Editing and saving a chapter retains its previous version.",
                )}
              />
            ) : (
              <>
                <label>
                  {t("Select version")}
                  <select
                    value={selectedVersion}
                    onChange={(e) => setSelectedVersion(e.target.value)}
                  >
                    {[...chapter.versions].reverse().map((v, index) => (
                      <option key={v.id} value={v.id}>
                        {t("Version")} {chapter.versions.length - index} ·{" "}
                        {date(v.created_at)} · {wordCount(v.body)}{" "}
                        {t("characters")}
                      </option>
                    ))}
                  </select>
                </label>
                <div className="version-comparison">
                  <div>
                    <h4>{t("Earlier version")}</h4>
                    <pre>{version?.body || t("(Empty chapter)")}</pre>
                  </div>
                  <div>
                    <h4>{t("Current draft")}</h4>
                    <pre>{draft.body || t("(Empty chapter)")}</pre>
                  </div>
                </div>
                <div className="modal-actions">
                  <Button onClick={() => setHistory(false)}>
                    {t("Close")}
                  </Button>
                  <Button primary onClick={restore}>
                    {t("Restore this version and retain the current version")}
                  </Button>
                </div>
              </>
            )}
          </div>
        </Modal>
      )}
    </div>
  );
}

/** Share story setup, outline drafts, and writing parameters with planning discussion. */
export function Planning({
  project,
  onProject,
  onSettings,
}: {
  project: Project;
  onProject: (p: Project) => void;
  onSettings: () => void;
}) {
  const exploring =
    !project.chapters.length &&
    !project.premise.trim() &&
    !project.outline.trim();
  const [detailsOpen, setDetailsOpen] = useState(false);
  const showDetails = !exploring || detailsOpen;
  const draftKey = `quillweave-plan-${project.id}`;
  const [draft, setDraft] = useState(() => {
    const fallback = {
      title: project.title,
      genre: project.genre,
      premise: project.premise,
      style: project.style,
      outline: project.outline,
      writing_style: project.writing_style,
      default_chapter_words: project.default_chapter_words,
      narrative_pov: project.narrative_pov,
    };
    try {
      const value = JSON.parse(localStorage.getItem(draftKey) || "null");
      return value &&
        ["title", "genre", "premise", "style", "outline"].every(
          (key) => typeof value[key] === "string",
        )
        ? ({ ...fallback, ...value } as typeof fallback)
        : fallback;
    } catch {
      return fallback;
    }
  });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const draftRef = useRef(draft);
  draftRef.current = draft;
  const [outlineReview, setOutlineReview] = useState<{
    text: string;
    base: string;
    error: string;
  } | null>(null);
  const dirty = Object.entries(draft).some(
    ([key, value]) => project[key as keyof typeof draft] !== value,
  );
  useEffect(() => {
    try {
      if (dirty) localStorage.setItem(draftKey, JSON.stringify(draft));
      else localStorage.removeItem(draftKey);
    } catch {
      setError("Draft storage is full. Save the story bible.");
    }
  }, [draft, draftKey, dirty]);
  /** Save author plans while retaining edits made during the request. */
  async function save(): Promise<Project | null> {
    if (!dirty) return project;
    if (busy) return null;
    setBusy(true);
    setError("");
    try {
      const next = await api<Project>("update_project", {
        project_id: project.id,
        revision: project.revision,
        ...draft,
      });
      onProject(next);
      return next;
    } catch (e) {
      setError(message(e));
      return null;
    } finally {
      setBusy(false);
    }
  }
  /** Open outline proposals for review or apply setup proposals to the draft. */
  async function apply(job: Job) {
    if (!["architecture", "outline"].includes(job.action)) return;
    if (job.action === "outline") {
      setOutlineReview({
        text: job.text,
        base: draftRef.current.outline,
        error: "",
      });
      return;
    }
    const key = "premise";
    if (
      draft[key] &&
      !window.confirm(t("Replace the current setup draft with this proposal?"))
    )
      return;
    setDraft((previous) => ({ ...previous, [key]: job.text }));
    setDetailsOpen(true);
  }
  /** Apply an edited proposal to the local outline draft after explicit review. */
  function acceptOutline() {
    if (!outlineReview) return;
    if (!outlineReview.text.trim()) {
      setOutlineReview({
        ...outlineReview,
        error: "Enter an outline before applying it.",
      });
      return;
    }
    if (draftRef.current.outline !== outlineReview.base) {
      setOutlineReview({
        ...outlineReview,
        error:
          "The outline draft changed during review. Close this review and open it again.",
      });
      return;
    }
    setDraft((previous) => ({ ...previous, outline: outlineReview.text }));
    setDetailsOpen(true);
    setOutlineReview(null);
  }
  return (
    <div className={`planning-layout${exploring ? " planning-exploring" : ""}`}>
      <section className="planning-content">
        <div className="section-title">
          <div>
            <p className="eyebrow">
              {t(exploring ? "A PLACE TO BEGIN" : "STORY BIBLE")}
            </p>
            <h1>
              {t(
                exploring
                  ? "Find your story, one conversation at a time"
                  : "Plan your story",
              )}
            </h1>
            <p>
              {t(
                exploring
                  ? "No genre, plot, or length to decide now. Start with what interests you; the details can grow from the conversation."
                  : "Discuss the story, generate an outline, then review and apply it to your draft.",
              )}
            </p>
          </div>
          {exploring && (
            <button
              type="button"
              className="button"
              aria-expanded={detailsOpen}
              aria-controls={`story-details-${project.id}`}
              onClick={() => setDetailsOpen((open) => !open)}
            >
              {t(
                detailsOpen
                  ? "Hide story details"
                  : "Story details · edit later",
              )}
            </button>
          )}
          {showDetails && (
            <Button primary disabled={busy} onClick={save}>
              <IconDeviceFloppy size={17} />
              {t("Save story bible")}
            </Button>
          )}
        </div>
        {error && <ErrorBox>{error}</ErrorBox>}
        <div
          className="planning-details"
          id={`story-details-${project.id}`}
          hidden={!showDetails}
        >
          {exploring && (
            <p className="field-hint">
              {t(
                "These settings are optional while exploring. Chapter length and viewpoint are editable defaults, not commitments.",
              )}
            </p>
          )}
          <div className="form-grid">
            <label>
              {t("Project title")}
              <input
                value={draft.title}
                onChange={(e) => setDraft({ ...draft, title: e.target.value })}
              />
            </label>
            <label>
              {t("Genre")}
              <input
                value={draft.genre}
                onChange={(e) => setDraft({ ...draft, genre: e.target.value })}
              />
            </label>
          </div>
          <WritingParameters
            value={draft}
            onChange={(patch) => setDraft({ ...draft, ...patch })}
          />
          <label>
            {t("Core premise")}
            <textarea
              className="large-input"
              value={draft.premise}
              onChange={(e) => setDraft({ ...draft, premise: e.target.value })}
              placeholder={t("Who wants what, and what stands in the way?")}
            />
          </label>
          <label>
            {t("Narrative style and boundaries")}
            <textarea
              value={draft.style}
              onChange={(e) => setDraft({ ...draft, style: e.target.value })}
              placeholder={t(
                "Viewpoint, language, and the style you want to preserve…",
              )}
            />
          </label>
          <label>
            {t("Story outline")}
            <textarea
              className="outline-input"
              aria-label={t("Story outline")}
              value={draft.outline}
              onChange={(e) => setDraft({ ...draft, outline: e.target.value })}
              placeholder={t("Expand a paragraph into a causal chapter plan…")}
            />
          </label>
          <ProjectModels project={project} onProject={onProject} />
        </div>
      </section>
      <Assistant
        project={project}
        initialAction="ask"
        exploring={exploring}
        onSettings={onSettings}
        onRefresh={async () =>
          onProject(await api("get_project", { project_id: project.id }))
        }
        beforeStart={save}
        onApply={apply}
      />
      {outlineReview && (
        <Modal
          title={t("Review generated outline")}
          wide
          onClose={() => setOutlineReview(null)}
        >
          <div className="modal-content outline-review">
            <p>
              {t(
                "Edit the proposal below, then apply it to the story outline draft. Save the story bible when you are ready.",
              )}
            </p>
            <details>
              <summary>{t("Current outline")}</summary>
              <MarkdownResponse
                text={outlineReview.base || t("No outline yet.")}
              />
            </details>
            <label>
              {t("Proposed outline")}
              <textarea
                className="outline-input"
                aria-label={t("Proposed outline")}
                value={outlineReview.text}
                onChange={(event) =>
                  setOutlineReview({
                    ...outlineReview,
                    text: event.target.value,
                    error: "",
                  })
                }
              />
            </label>
            {outlineReview.error && <ErrorBox>{outlineReview.error}</ErrorBox>}
            <div className="modal-actions">
              <Button onClick={() => setOutlineReview(null)}>
                {t("Cancel")}
              </Button>
              <Button primary onClick={acceptOutline}>
                {t("Use as outline draft")}
              </Button>
            </div>
          </div>
        </Modal>
      )}
    </div>
  );
}
