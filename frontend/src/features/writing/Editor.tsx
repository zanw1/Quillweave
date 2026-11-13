import { Select } from "../../components/Select";
import { t } from "../../lib/i18n";
import { useCallback, useEffect, useRef, useState } from "react";
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
import { Badge, Button, Empty, ErrorBox, Modal } from "../../components/ui";
import type { Chapter, Job, Project } from "../../domain/types";
import { ChapterDeletion } from "./ChapterDeletion";
import "./writing.css";
import {
  cacheChapterDraft,
  forgetChapterDraft,
  rememberChapterDraft,
  retainedChapterDraft,
  type ChapterDraft,
} from "./drafts";

interface Props {
  project: Project;
  chapter?: Chapter;
  onProject: (p: Project) => void;
  onChapter: (id: string) => void;
  onSettings: () => void;
  onWiki: () => void;
  seed: string;
  onSeedConsumed?: () => void;
  embedded?: boolean;
  onRegisterSave?: (save: (() => Promise<Project | null>) | null) => void;
  onSelection?: (text: string) => void;
}
type Draft = ChapterDraft;

/** Identify a saved chapter revision using its durable version history. */
function chapterVersion(chapter: Chapter): string {
  return `${chapter.id}:${chapter.versions.at(-1)?.id || "initial"}`;
}

/** Compare the editable chapter fields without draft-cache metadata. */
function sameDraft(left: Draft, right: Draft): boolean {
  return (
    left.title === right.title &&
    left.body === right.body &&
    left.synopsis === right.synopsis &&
    left.status === right.status &&
    left.target_words === right.target_words
  );
}

/** Build a clean editor draft from the currently saved chapter. */
function savedDraft(chapter: Chapter): Draft {
  return {
    title: chapter.title,
    body: chapter.body,
    synopsis: chapter.synopsis,
    status: chapter.status,
    target_words: chapter.target_words,
    base_version: chapterVersion(chapter),
  };
}

/** Recover the common saved text from the mounted baseline or its next archived version. */
function commonSavedVersion(chapter: Chapter, draft: Draft, previous: Chapter) {
  if (draft.base_version === chapterVersion(previous)) return previous;
  const prefix = `${chapter.id}:`;
  if (!draft.base_version?.startsWith(prefix)) return undefined;
  const base = draft.base_version.slice(prefix.length);
  const index = base === "initial" ? -1 : chapter.versions.findIndex((item) => item.id === base);
  if (base !== "initial" && index < 0) return undefined;
  return chapter.versions[index + 1];
}

/** Restore uncommitted local drafts across navigation or reload. */
function readDraft(key: string, chapter: Chapter): Draft {
  const retained = retainedChapterDraft(key);
  if (retained) return retained;
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
        base_version: value.base_version || chapterVersion(chapter),
        target_words:
          value.target_words === undefined
            ? chapter.target_words
            : value.target_words,
      };
  } catch {
    /* Retain database text when a local draft is corrupted. */
  }
  return savedDraft(chapter);
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
  onSeedConsumed,
  embedded,
  onRegisterSave,
  onSelection,
}: Props) {
  const [adding, setAdding] = useState(false);
  const [title, setTitle] = useState("");
  const [targetWords, setTargetWords] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const editorSave = useRef<(() => Promise<Project | null>) | null>(null);
  /** Share one current editor save callback with deletion and the workspace assistant. */
  const registerSave = useCallback((save: (() => Promise<Project | null>) | null) => {
    editorSave.current = save;
    onRegisterSave?.(save);
  }, [onRegisterSave]);
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
            disabled={deleting}
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
        <ChapterDeletion
          project={project}
          chapter={chapter}
          beforeDelete={() => editorSave.current ? editorSave.current() : Promise.resolve(project)}
          onProject={onProject}
          onChapter={onChapter}
          onPendingChange={setDeleting}
        />
        {project.chapters.map((c) => (
          <button
            key={c.id}
            className={`chapter-item ${chapter?.id === c.id ? "active" : ""}`}
            disabled={deleting}
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
        <button className="new-chapter" disabled={deleting} onClick={() => setAdding(true)}>
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
            onSeedConsumed,
            embedded,
            onSelection,
          }}
          onRegisterSave={registerSave}
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
  onSeedConsumed,
  embedded,
  onRegisterSave,
  onSelection,
}: Props & { chapter: Chapter }) {
  const draftKey = `quillweave-draft-${project.id}-${chapter.id}`;
  const [draft, setDraftState] = useState<Draft>(() =>
    readDraft(draftKey, chapter),
  );
  const draftRef = useRef(draft);
  const previousChapter = useRef(chapter);
  const projectRef = useRef(project);
  if (project.revision >= projectRef.current.revision) projectRef.current = project;
  const saveRef = useRef<() => Promise<Project | null>>(() =>
    Promise.resolve(project),
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [history, setHistory] = useState(false);
  const [comparison, setComparison] = useState(false);
  const [cacheUnavailable, setCacheUnavailable] = useState(false);
  const [showAI, setShowAI] = useState(true);
  const [showPlan, setShowPlan] = useState(false);
  const pendingSave = useRef<Promise<Project | null> | null>(null);
  const [selectedVersion, setSelectedVersion] = useState<string>("");
  const dirty =
    draft.title !== chapter.title ||
    draft.body !== chapter.body ||
    draft.synopsis !== chapter.synopsis ||
    draft.status !== chapter.status ||
    draft.target_words !== chapter.target_words;
  const conflicted = dirty && draft.base_version !== chapterVersion(chapter);

  useEffect(() => {
    const local = draftRef.current;
    if (
      sameDraft(local, previousChapter.current) ||
      sameDraft(local, chapter)
    ) {
      setDraft(savedDraft(chapter));
      previousChapter.current = chapter;
    } else if (!pendingSave.current && local.base_version !== chapterVersion(chapter)) {
      setError(
        "This chapter was updated elsewhere. Your local draft is retained. Review the saved text before replacing it.",
      );
    }
  }, [chapter, project.revision]);

  useEffect(() => {
    onRegisterSave?.(() => saveRef.current());
    return () => onRegisterSave?.(null);
  }, [chapter.id, onRegisterSave]);

  useEffect(() => {
    if (!dirty) {
      forgetChapterDraft(draftKey);
      setCacheUnavailable(false);
      return;
    }
    setCacheUnavailable(!cacheChapterDraft(draftKey, draft));
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
    rememberChapterDraft(draftKey, value);
    setDraftState(value);
  }

  /** Drain pending saves and recheck input so a task receives the latest saved draft. */
  async function save(): Promise<Project | null> {
    for (;;) {
      if (pendingSave.current) {
        if (!(await pendingSave.current)) return null;
        continue;
      }
      const currentProject = projectRef.current;
      const saved = currentProject.chapters.find((item) => item.id === chapter.id);
      if (!saved) {
        setError("Chapter not found");
        return null;
      }
      const submitted = draftRef.current;
      if (sameDraft(submitted, saved)) return currentProject;
      if (submitted.base_version !== chapterVersion(saved)) {
        setError(
          "This chapter was updated elsewhere. Your local draft is retained. Review the saved text before replacing it.",
        );
        return null;
      }
      setSaving(true);
      setError("");
      const request = api<Project>("save_chapter", {
        project_id: currentProject.id,
        revision: currentProject.revision,
        chapter_id: saved.id,
        ...submitted,
      })
        .then((next) => {
          if (next.revision >= projectRef.current.revision) projectRef.current = next;
          const persisted = next.chapters.find((item) => item.id === saved.id);
          if (persisted) {
            previousChapter.current = persisted;
            const unchanged = sameDraft(draftRef.current, submitted);
            setDraft((current) =>
              sameDraft(current, submitted)
                ? savedDraft(persisted)
                : { ...current, base_version: chapterVersion(persisted) },
            );
            if (unchanged) forgetChapterDraft(draftKey);
          }
          onProject(next);
          return next;
        })
        .catch((cause) => {
          setError(message(cause));
          return null;
        })
        .finally(() => {
          pendingSave.current = null;
          setSaving(false);
        });
      pendingSave.current = request;
      if (!(await request)) return null;
    }
  }
  saveRef.current = save;

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
      projectRef.current = next;
      setDraft(savedDraft(c));
      setHistory(false);
    } catch (e) {
      setError(message(e));
    }
  }
  const version = chapter.versions.find((v) => v.id === selectedVersion);
  const commonVersion = commonSavedVersion(chapter, draft, previousChapter.current);
  return (
    <div
      className={`editor-and-assistant ${showAI && !embedded ? "" : "focus-mode"}`}
    >
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
            {!embedded && (
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
            )}
            <Button primary disabled={!dirty || saving} onClick={() => save()}>
              <IconDeviceFloppy size={16} />
              {saving ? t("Saving") : t("Save")}
            </Button>
          </div>
        </header>
        {cacheUnavailable && (
          <div className="editor-error">
            <ErrorBox>
              {t("Browser storage is unavailable. This draft is retained for this session. Save before closing the app.")}
            </ErrorBox>
          </div>
        )}
        {error && (
          <div className="editor-error">
            <ErrorBox>{error}</ErrorBox>
            <button
              className="text-button"
              onClick={() => refresh().catch((e) => setError(message(e)))}
            >
              {t("Refresh project status (keep draft)")}
            </button>
            {conflicted && (
              <button data-testid="resolve-chapter-conflict" className="text-button" onClick={() => setComparison(true)}>
                {t("Compare and resolve chapter conflict")}
              </button>
            )}
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
              <Select
                aria-label={t("Chapter status")}
                value={draft.status}
                onChange={(e) => setDraft({ ...draft, status: e.target.value })}
              >
                <option value="draft">{t("Draft")}</option>
                <option value="final">{t("Final")}</option>
              </Select>
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
                {showPlan ? t("Collapse chapter notes") : t("Chapter notes")}
              </button>
            </div>
            {showPlan && (
              <label className="scene-plan">
                {t("Additional author requirements for this chapter")}
                <textarea
                  aria-label={t("Chapter notes")}
                  value={draft.synopsis}
                  onChange={(e) =>
                    setDraft({ ...draft, synopsis: e.target.value })
                  }
                  placeholder={t(
                    "Extra instructions or details to preserve…",
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
              onSelect={(event) => {
                const field = event.currentTarget;
                onSelection?.(
                  field.value.slice(field.selectionStart, field.selectionEnd),
                );
              }}
            />
            <div className="chapter-end">{t("— The story continues —")}</div>
          </div>
        </div>
        <footer className="editor-footer">
          <span>
            {dirty
              ? cacheUnavailable
                ? t("Draft retained for this session · Save before closing")
                : t("Draft stored on this device · Save to add it to the project")
              : t("Chapter text and versions are stored locally")}
          </span>
          <button className="text-button" onClick={onWiki}>
            <IconBook2 size={15} />
            {t("Character Wiki")}
          </button>
        </footer>
      </section>
      {showAI && !embedded && (
        <Assistant
          project={project}
          chapter={chapter}
          canHumanize={!!draft.body.trim()}
          seed={seed}
          onSeedConsumed={onSeedConsumed}
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
                  <Select
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
                  </Select>
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
      {comparison && (
        <Modal
          title={t("Resolve chapter conflict")}
          wide
          onClose={() => setComparison(false)}
        >
          <div className="modal-content">
            <p>{t("Compare both versions before choosing which draft to save. Saving will retain the current saved text in version history.")}</p>
            <div className="version-comparison">
              <div>
                <h4>{t("Saved chapter")}</h4>
                <strong>{chapter.title}</strong>
                <pre>{chapter.body || t("(Empty chapter)")}</pre>
              </div>
              <div>
                <h4>{t("Current draft")}</h4>
                <strong>{draft.title}</strong>
                <pre>{draft.body || t("(Empty chapter)")}</pre>
              </div>
            </div>
            <details className="chapter-conflict-base">
              <summary>{t("Common saved version")}</summary>
              {commonVersion ? <>
                <strong>{commonVersion.title}</strong>
                <pre>{commonVersion.body || t("(Empty chapter)")}</pre>
              </> : <p>{t("The common saved version is no longer available in history.")}</p>}
            </details>
            <div className="modal-actions">
              <Button onClick={() => setComparison(false)}>{t("Cancel")}</Button>
              <Button data-testid="use-saved-chapter" onClick={() => {
                previousChapter.current = chapter;
                setDraft(savedDraft(chapter));
                setError("");
                setComparison(false);
              }}>{t("Use saved chapter")}</Button>
              <Button data-testid="keep-local-chapter" primary onClick={() => {
                previousChapter.current = chapter;
                setDraft({ ...draftRef.current, base_version: chapterVersion(chapter) });
                setError("");
                setComparison(false);
              }}>{t("Keep local draft and save against this version")}</Button>
            </div>
          </div>
        </Modal>
      )}
    </div>
  );
}
