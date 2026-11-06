import { navigation, pageNames } from "./navigation";
import {
  NewStoryDialog,
  WorkspaceExport,
  WorkspaceBrand,
} from "./WorkspaceControls";
import { Select } from "../components/Select";
import { clearProjectStorage } from "./storage";
import { t, useLanguage, setLanguage } from "../lib/i18n";
import { useEffect, useRef, useState } from "react";
import {
  IconFeather,
  IconHome,
  IconBook2,
  IconBulb,
  IconSettings,
  IconChevronDown,
  IconPlus,
  IconSearch,
  IconLayoutGrid,
  IconList,
  IconArrowUpRight,
  IconUpload,
  IconTrash,
  IconArrowRight,
  IconCircleCheck,
  IconLayoutSidebarLeftCollapse,
  IconLayoutSidebarLeftExpand,
} from "@tabler/icons-react";
import { api, date, message, pickDirectory } from "../lib/api";
import { restoreArchiveFile } from "../lib/archive";
import { Badge, Button, Empty, ErrorBox, Modal } from "../components/ui";
import { Writing } from "../features/writing/Editor";
import { Studio as Planning } from "../features/studio/Studio";
import { Wiki } from "../features/wiki/Wiki";
import { Library } from "../features/library/Library";
import { Methods } from "../features/methods/Methods";
import { Settings } from "../features/settings/Settings";
import type { Page, Project, ProjectCard } from "../domain/types";
import "../styles/app.css";

/** Manage project navigation through the shared Go service. */
export function App() {
  const language = useLanguage();
  const [page, setPage] = useState<Page>("home");
  const [projects, setProjects] = useState<ProjectCard[]>([]);
  const [project, setProject] = useState<Project | null>(null);
  const [chapterId, setChapterId] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [toast, setToast] = useState("");
  const [create, setCreate] = useState(false);
  const [importing, setImporting] = useState(false);
  const [importPath, setImportPath] = useState("");
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState({ title: "" });
  const [query, setQuery] = useState("");
  const [layout, setLayout] = useState("list");
  const [seed, setSeed] = useState("");
  const [sidebar, setSidebar] = useState(() => window.innerWidth > 640);
  const [projectPicker, setProjectPicker] = useState<Page | "auto" | null>(
    null,
  );
  const [pickerQuery, setPickerQuery] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<Project | null>(null);
  const [deleteError, setDeleteError] = useState("");
  const [deleting, setDeleting] = useState(false);
  const deletingRef = useRef(false);
  const projectRequest = useRef(0);
  const archiveFile = useRef<HTMLInputElement>(null);
  const pendingMethod = useRef("");

  useEffect(() => {
    void load();
  }, []);
  useEffect(() => {
    if (window.innerWidth <= 640) setSidebar(false);
  }, [page]);
  useEffect(() => {
    if (!toast) return;
    const timer = setTimeout(() => setToast(""), 4000);
    return () => clearTimeout(timer);
  }, [toast]);
  /** Load project summaries and restore the selected project on the home screen. */
  async function load() {
    const request = ++projectRequest.current;
    setLoading(true);
    setError("");
    try {
      const list = await api<ProjectCard[]>("list_projects");
      if (request !== projectRequest.current) return;
      setProjects(list);
      let current: string | null = null;
      try {
        current = localStorage.getItem("quillweave-project");
      } catch {
        /* Project tools remain available without a remembered selection. */
      }
      if (current && list.some((p) => p.id === current)) {
        const p = await api<Project>("get_project", { project_id: current });
        if (request === projectRequest.current) {
          setProject(p);
          setChapterId(p.chapters[0]?.id || "");
        }
      }
    } catch (e) {
      if (request === projectRequest.current) setError(message(e));
    } finally {
      setLoading(false);
    }
  }

  /** Update project data and its home summary while retaining the chapter selection. */
  function update(next: Project) {
    setProject((current) => (current?.id === next.id ? next : current));
    const request = projectRequest.current;
    api<ProjectCard[]>("list_projects")
      .then((list) => {
        if (request === projectRequest.current) setProjects(list);
      })
      .catch((e) => setError(message(e)));
  }

  /** Open an existing chapter or direct an empty project to Story planning. */
  async function openProject(id: string, destination?: Page) {
    const request = ++projectRequest.current;
    setError("");
    setBusy(true);
    try {
      const p = await api<Project>("get_project", { project_id: id });
      if (request === projectRequest.current) enter(p, destination);
    } catch (e) {
      if (request === projectRequest.current) setError(message(e));
    } finally {
      if (request === projectRequest.current) setBusy(false);
    }
  }

  /** Enter the shared editing flow for newly created, imported, or sample projects. */
  function enter(p: Project, destination?: Page) {
    projectRequest.current++;
    setProject(p);
    try {
      localStorage.setItem("quillweave-project", p.id);
    } catch {
      /* Keep project switching usable when browser storage is blocked. */
    }
    update(p);
    const method = pendingMethod.current;
    pendingMethod.current = "";
    setSeed(method);
    setBusy(false);
    setProjectPicker(null);
    setChapterId(p.chapters[0]?.id || "");
    setPage(
      method
        ? p.chapters.length
          ? "write"
          : "plan"
        : destination || (p.chapters.length ? "write" : "plan"),
    );
  }

  /** Resolve global project tools explicitly while keeping in-project navigation scoped. */
  function navigateProject(destination: Page) {
    setError("");
    if (selectedPage && project) setPage(destination);
    else if (!projects.length) setCreate(true);
    else if (projects.length === 1)
      void openProject(projects[0].id, destination);
    else {
      setPickerQuery("");
      setProjectPicker(destination);
    }
  }

  /** Offer an explicit story switch while preserving the currently open project tool. */
  function switchProject() {
    setError("");
    if (!projects.length) setCreate(true);
    else {
      setPickerQuery("");
      setProjectPicker(selectedPage ? page : "auto");
    }
  }

  /** Fetch the current revision and title before asking the author to confirm deletion. */
  async function requestDelete(id: string) {
    setBusy(true);
    setError("");
    setDeleteError("");
    try {
      setDeleteTarget(await api<Project>("get_project", { project_id: id }));
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  /** Commit a confirmed deletion before clearing its UI state or browser caches. */
  async function deleteProject() {
    if (!deleteTarget || deletingRef.current) return;
    deletingRef.current = true;
    setDeleting(true);
    setDeleteError("");
    try {
      await api("delete_project", {
        project_id: deleteTarget.id,
        revision: deleteTarget.revision,
      });
      projectRequest.current++;
      setProjects((current) => current.filter((p) => p.id !== deleteTarget.id));
      if (project?.id === deleteTarget.id) {
        setProject(null);
        setChapterId("");
        setSeed("");
        setPage("home");
      }
      clearProjectStorage(deleteTarget.id);
      setToast(t("Deleted {0}", deleteTarget.title));
      setDeleteTarget(null);
    } catch (e) {
      setDeleteError(message(e));
    } finally {
      deletingRef.current = false;
      setDeleting(false);
    }
  }

  /** Create an unplanned workspace with a temporary name when the author has none yet. */
  async function newProject() {
    setError("");
    setBusy(true);
    try {
      let title = form.title.trim();
      if (!title) {
        const base = t("Untitled story");
        const titles = new Set(projects.map((item) => item.title));
        title = base;
        for (let number = 2; titles.has(title); number++)
          title = `${base} ${number}`;
      }
      enter(
        await api<Project>("create_project", { title, genre: "", premise: "" }),
      );
      setCreate(false);
      setForm({ title: "" });
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  /** Create the original sample with clearly identified demonstration analysis. */
  async function example() {
    setError("");
    setBusy(true);
    try {
      const existing = projects.find((p) => p.title === "Letters at Low Tide");
      if (existing) await openProject(existing.id);
      else enter(await api<Project>("create_example"));
      setToast(
        "Opened the original sample project with demonstration analysis",
      );
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  /** Import a separate project while retaining the form after errors. */
  async function importLegacy() {
    setBusy(true);
    setError("");
    try {
      enter(await api<Project>("import_legacy", { path: importPath.trim() }));
      setImporting(false);
      setToast("Import complete. Previous analysis is archived in the library");
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  /** Restore a complete backup into a separate project. */
  async function importArchive(file?: File) {
    if (!file) return;
    setBusy(true);
    setError("");
    try {
      enter(await restoreArchiveFile(file));
      setImporting(false);
      setToast("Backup restored as a separate project");
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  /** Bring a writing-method question into the composer for explicit submission. */
  function useMethod(prompt: string) {
    pendingMethod.current = prompt;
    navigateProject("plan");
  }

  const filtered = projects.filter((p) =>
    `${p.title} ${p.genre}`.includes(query),
  );
  const pickerProjects = projects.filter((p) =>
    `${p.title} ${p.genre}`
      .toLocaleLowerCase()
      .includes(pickerQuery.trim().toLocaleLowerCase()),
  );
  const chapter =
    project?.chapters.find((c) => c.id === chapterId) || project?.chapters[0];
  const selectedPage = ["write", "plan", "wiki", "knowledge"].includes(page);
  return (
    <div className={`app ${sidebar ? "" : "sidebar-collapsed"}`}>
      {sidebar && (
        <button
          className="sidebar-shade"
          aria-label={t("Close navigation")}
          onClick={() => setSidebar(false)}
        />
      )}
      {sidebar && (
        <aside className="sidebar">
          <WorkspaceBrand onHome={() => setPage("home")} />
          <button
            className="workspace-picker"
            onClick={switchProject}
            disabled={loading || busy}
            aria-label={t("Switch project")}
          >
            <span className="workspace-monogram">
              <IconBook2 size={20} />
            </span>
            <span>
              {project?.title || t("My workspace")}
              <small>{t("Switch project")}</small>
            </span>
            <IconChevronDown size={15} />
          </button>
          <div className="sidebar-section-label">{t("Workspace")}</div>
          <button
            className={`nav-item ${page === "home" ? "active" : ""}`}
            onClick={() => setPage("home")}
          >
            <IconHome size={20} />
            {t("My projects")}
          </button>
          <div className="sidebar-section-label project-label">
            {selectedPage && project ? project.title : t("Project tools")}
          </div>
          {navigation.map((item) => (
            <button
              key={item.page}
              className={`nav-item ${page === item.page ? "active" : ""}`}
              disabled={loading || busy}
              onClick={() => navigateProject(item.page)}
            >
              <item.icon size={20} />
              {t(item.label)}
              {item.page === "wiki" &&
                !!project?.facts.filter((f) => f.status === "pending")
                  .length && (
                  <span className="nav-count">
                    {project.facts.filter((f) => f.status === "pending").length}
                  </span>
                )}
            </button>
          ))}
          <div className="sidebar-section-label">{t("Writing tools")}</div>
          <button
            className={`nav-item ${page === "methods" ? "active" : ""}`}
            onClick={() => setPage("methods")}
          >
            <IconBulb size={20} />
            {t("Writing methods")}
          </button>
          <div className="sidebar-bottom">
            <button
              className={`nav-item ${page === "settings" ? "active" : ""}`}
              onClick={() => setPage("settings")}
            >
              <IconSettings size={20} />
              {t("Model settings")}
            </button>
            <div className="local-status">
              <span className="connection connected" />
              <div>
                {t("Your story, your decisions")}
                <small>{t("Saved locally · Export anytime")}</small>
              </div>
            </div>
          </div>
        </aside>
      )}
      <div className="app-main">
        <header className="topbar">
          <div className="breadcrumbs">
            <button
              className="icon-button"
              aria-label={sidebar ? t("Collapse sidebar") : t("Expand sidebar")}
              onClick={() => setSidebar(!sidebar)}
            >
              {sidebar ? (
                <IconLayoutSidebarLeftCollapse size={20} />
              ) : (
                <IconLayoutSidebarLeftExpand size={20} />
              )}
            </button>
            <button onClick={() => setPage("home")}>{t("Workspace")}</button>
            <span>/</span>
            <strong>
              {selectedPage && project ? project.title : t(pageNames[page])}
            </strong>
          </div>
          {project && selectedPage && (
            <div className="top-tabs">
              <button
                className={["write", "plan"].includes(page) ? "active" : ""}
                onClick={() => setPage("write")}
              >
                {t("Write")}
              </button>
              <button
                className={page === "wiki" ? "active" : ""}
                onClick={() => setPage("wiki")}
              >
                {t("Wiki")}
              </button>
              <button
                className={page === "knowledge" ? "active" : ""}
                onClick={() => setPage("knowledge")}
              >
                {t("Sources")}
              </button>
            </div>
          )}
          <div className="topbar-right">
            <label className="language-switcher">
              <span className="sr-only">{t("Interface language")}</span>
              <Select
                aria-label={t("Interface language")}
                value={language}
                onChange={(event) =>
                  setLanguage(event.target.value === "en" ? "en" : "zh-CN")
                }
              >
                <option value="zh-CN">中文</option>
                <option value="en">English</option>
              </Select>
            </label>
            {project && selectedPage && (
              <WorkspaceExport
                project={project}
                onExported={() => setToast("Exported the saved version")}
                onError={setError}
              />
            )}
            <span className="workspace-avatar">
              <IconFeather size={20} />
            </span>
          </div>
        </header>
        <main className={`page-content page-${page}`}>
          {error && !create && !importing && (
            <div className="global-error">
              <ErrorBox>{error}</ErrorBox>
              <button
                className="text-button"
                onClick={() => {
                  setError("");
                  if (page === "home") void load();
                }}
              >
                {t("Retry / Close")}
              </button>
            </div>
          )}
          {page === "home" && (
            <section className="home-page">
              <div className="welcome-banner">
                <p className="eyebrow">
                  <span className="connection connected" />
                  {t("QUILLWEAVE WORKSPACE")}
                </p>
                <h1>{t("Give your story room to grow.")}</h1>
                <p className="welcome-description">
                  {t("From an idea to a whole world.")} <br />
                  {t("A home for your prose, characters, and ideas.")}
                </p>
                <div className="welcome-actions">
                  <Button
                    primary
                    onClick={() => {
                      setError("");
                      setCreate(true);
                    }}
                  >
                    <IconPlus size={19} />
                    {t("Start a new story")}
                  </Button>
                  <button
                    className="text-button"
                    onClick={example}
                    disabled={busy}
                  >
                    {t("Explore the sample")} <IconArrowRight size={17} />
                  </button>
                </div>
                <div className="welcome-side">
                  <IconFeather size={44} stroke={1.1} />
                  <p>{t("Plan · Write · Connect")}</p>
                  <span>{t("A quiet place for your ideas")}</span>
                </div>
              </div>
              <div className="projects-title">
                <h2>
                  {t("My projects")} <span>{projects.length}</span>
                </h2>
                <div className="projects-tools">
                  <label className="search">
                    <IconSearch size={18} />
                    <input
                      aria-label={t("Search projects")}
                      placeholder={t("Search projects…")}
                      value={query}
                      onChange={(e) => setQuery(e.target.value)}
                    />
                  </label>
                  <div className="segmented">
                    <button
                      className={layout === "grid" ? "active" : ""}
                      onClick={() => setLayout("grid")}
                      aria-label={t("Grid view")}
                    >
                      <IconLayoutGrid size={18} />
                    </button>
                    <button
                      className={layout === "list" ? "active" : ""}
                      onClick={() => setLayout("list")}
                      aria-label={t("List view")}
                    >
                      <IconList size={20} />
                    </button>
                  </div>
                  <Button
                    onClick={() => {
                      setError("");
                      setImporting(true);
                    }}
                  >
                    <IconUpload size={16} />
                    {t("Import")}
                  </Button>
                </div>
              </div>
              {loading ? (
                <div className="loading">{t("Opening your workspace…")}</div>
              ) : filtered.length ? (
                layout === "list" ? (
                  <div className="table-wrap projects-table">
                    <table>
                      <thead>
                        <tr>
                          <th>{t("Project")}</th>
                          <th>{t("Premise")}</th>
                          <th>{t("Length")}</th>
                          <th>{t("Last updated")}</th>
                          <th />
                        </tr>
                      </thead>
                      <tbody>
                        {filtered.map((p) => (
                          <tr key={p.id}>
                            <td>
                              <button
                                className="project-name"
                                onClick={() => openProject(p.id)}
                              >
                                <span className="project-icon">
                                  <IconBook2 size={25} stroke={1.5} />
                                </span>
                                <span>
                                  <strong>{p.title}</strong>
                                  <small>{p.genre || t("No genre set")}</small>
                                </span>
                              </button>
                            </td>
                            <td>
                              <p className="table-description">
                                {p.premise || t("A new story is taking shape.")}
                              </p>
                            </td>
                            <td>
                              <strong className="word-total">
                                {p.word_count.toLocaleString()}{" "}
                                {t("characters")}
                              </strong>
                              <small className="cell-sub">
                                {p.chapter_count}{" "}
                                {p.chapter_count === 1
                                  ? t("chapter")
                                  : t("chapters")}
                              </small>
                            </td>
                            <td className="muted">{date(p.updated_at)}</td>
                            <td>
                              <div className="project-actions">
                                <button
                                  className="icon-button"
                                  aria-label={t("Open {0}", p.title)}
                                  disabled={busy}
                                  onClick={() => openProject(p.id)}
                                >
                                  <IconArrowUpRight size={18} />
                                </button>
                                <button
                                  className="icon-button danger"
                                  aria-label={t("Delete {0}", p.title)}
                                  title={t("Delete project")}
                                  disabled={busy}
                                  onClick={() => requestDelete(p.id)}
                                >
                                  <IconTrash size={18} />
                                </button>
                              </div>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <div className="table-footer">
                      {projects.length}{" "}
                      {projects.length === 1 ? t("project") : t("projects")}{" "}
                      {t("· Every story deserves an ending")}
                    </div>
                  </div>
                ) : (
                  <div className="project-grid">
                    {filtered.map((p) => (
                      <article className="project-card" key={p.id}>
                        <button
                          className="project-card-main"
                          aria-label={t("Open {0}", p.title)}
                          disabled={busy}
                          onClick={() => openProject(p.id)}
                        >
                          <span className="project-icon">
                            <IconBook2 size={27} />
                          </span>
                          <Badge>{p.genre || t("New project")}</Badge>
                          <h3>{p.title}</h3>
                          <p>
                            {p.premise || t("A new story is taking shape.")}
                          </p>
                          <span className="project-card-meta">
                            <span>
                              {p.chapter_count}{" "}
                              {p.chapter_count === 1
                                ? t("chapter")
                                : t("chapters")}{" "}
                              · {p.word_count.toLocaleString()}{" "}
                              {t("characters")}
                            </span>
                            <IconArrowUpRight size={18} />
                          </span>
                        </button>
                        <button
                          className="icon-button danger project-card-delete"
                          aria-label={t("Delete {0}", p.title)}
                          title={t("Delete project")}
                          disabled={busy}
                          onClick={() => requestDelete(p.id)}
                        >
                          <IconTrash size={18} />
                        </button>
                      </article>
                    ))}
                  </div>
                )
              ) : (
                <Empty
                  title={
                    query
                      ? t("No matching project")
                      : t("Your first story starts here")
                  }
                  detail={
                    query
                      ? t("Try a project title or genre keyword.")
                      : t(
                          "Create a project, import an older one, or explore the sample.",
                        )
                  }
                >
                  <Button onClick={() => setCreate(true)}>
                    <IconPlus size={17} />
                    {t("New project")}
                  </Button>
                </Empty>
              )}
              <div className="home-footnote">
                <IconCircleCheck size={17} />
                <span>
                  {t(
                    "Write offline and use AI when you choose. Projects and versions are stored locally.",
                  )}
                </span>
              </div>
            </section>
          )}
          {page === "write" && project && (
            <Writing
              key={project.id}
              project={project}
              chapter={chapter}
              onProject={update}
              onChapter={setChapterId}
              onSettings={() => setPage("settings")}
              onWiki={() => setPage("wiki")}
              seed={seed}
              onSeedConsumed={() => setSeed("")}
            />
          )}
          {page === "plan" && project && (
            <Planning
              key={project.id}
              project={project}
              onProject={update}
              onSettings={() => setPage("settings")}
              onWiki={() => setPage("wiki")}
              onWrite={() => setPage("write")}
              initialSeed={seed}
              onSeedConsumed={() => setSeed("")}
            />
          )}
          {page === "wiki" && project && (
            <Wiki
              key={project.id}
              project={project}
              onProject={update}
              onChapter={(id) => {
                setChapterId(id);
                setPage("write");
              }}
            />
          )}
          {page === "knowledge" && project && (
            <Library
              key={project.id}
              project={project}
              onProject={update}
              onSettings={() => setPage("settings")}
            />
          )}
          {page === "methods" && <Methods onUse={useMethod} />}
          {page === "settings" && <Settings />}
        </main>
      </div>
      {toast && (
        <div className="toast" role="status">
          <IconCircleCheck size={18} />
          {t(toast)}
        </div>
      )}
      {projectPicker && (
        <Modal
          title={
            projectPicker === "auto"
              ? t("Switch project")
              : t("Choose a project for {0}", t(pageNames[projectPicker]))
          }
          onClose={() => {
            if (!busy) {
              setProjectPicker(null);
              pendingMethod.current = "";
            }
          }}
        >
          <div className="modal-content">
            <label>
              {t("Search projects")}
              <input
                autoFocus
                value={pickerQuery}
                onChange={(e) => setPickerQuery(e.target.value)}
                placeholder={t("Search projects…")}
              />
            </label>
            <div className="project-picker-list">
              {pickerProjects.map((p) => (
                <button
                  key={p.id}
                  className="project-picker-option"
                  disabled={busy}
                  onClick={() =>
                    openProject(
                      p.id,
                      projectPicker === "auto" ? undefined : projectPicker,
                    )
                  }
                >
                  <span className="project-icon">
                    <IconBook2 size={22} />
                  </span>
                  <span>
                    <strong>{p.title}</strong>
                    <small>
                      {p.genre || t("No genre set")} · {p.chapter_count}{" "}
                      {t("chapters")}
                    </small>
                  </span>
                  {project?.id === p.id && <Badge>{t("Last opened")}</Badge>}
                </button>
              ))}
              {!pickerProjects.length && (
                <p className="muted">{t("No matching project")}</p>
              )}
            </div>
            {error && <ErrorBox>{error}</ErrorBox>}
            <div className="modal-actions">
              <Button
                disabled={busy}
                onClick={() => {
                  setProjectPicker(null);
                  pendingMethod.current = "";
                }}
              >
                {t("Cancel")}
              </Button>
              <Button
                disabled={busy}
                onClick={() => {
                  setProjectPicker(null);
                  setCreate(true);
                }}
              >
                <IconPlus size={17} />
                {t("Start a new story")}
              </Button>
            </div>
          </div>
        </Modal>
      )}
      {deleteTarget && (
        <Modal
          title={t("Delete project")}
          onClose={() => {
            if (!deletingRef.current) setDeleteTarget(null);
          }}
        >
          <div className="modal-content">
            <h3 className="delete-project-name">
              {t('Delete "{0}"?', deleteTarget.title)}
            </h3>
            <p>
              {t(
                "This permanently deletes the story, chapters, saved versions, story knowledge, and AI history. This cannot be undone.",
              )}
            </p>
            <p className="muted">
              {t("Export a project backup first if you want to keep a copy.")}
            </p>
            {deleteError && <ErrorBox>{deleteError}</ErrorBox>}
            <div className="modal-actions">
              <Button
                autoFocus
                disabled={deleting}
                onClick={() => setDeleteTarget(null)}
              >
                {t("Cancel")}
              </Button>
              <Button
                className="danger"
                disabled={deleting}
                onClick={deleteProject}
              >
                <IconTrash size={17} />
                {deleting ? t("Deleting…") : t("Delete permanently")}
              </Button>
            </div>
          </div>
        </Modal>
      )}
      {create && (
        <NewStoryDialog
          title={form.title}
          error={error}
          busy={busy}
          onTitle={(title) => setForm({ ...form, title })}
          onCreate={newProject}
          onCancel={() => {
            setCreate(false);
            pendingMethod.current = "";
          }}
          onClose={() => {
            setCreate(false);
            setError("");
            pendingMethod.current = "";
          }}
        />
      )}
      {importing && (
        <Modal
          title={t("Import your project")}
          onClose={() => {
            setImporting(false);
            setError("");
          }}
        >
          <div className="modal-content">
            <h3>{t("Import from an older Quillweave version")}</h3>
            <p className="muted">
              {t(
                "Select the project folder containing chapters/ or Novel_architecture.txt.",
              )}
            </p>
            <label>
              {t("Full path to the legacy project")}
              <div className="path-input">
                <input
                  value={importPath}
                  onChange={(e) => setImportPath(e.target.value)}
                  placeholder="/Users/…/my-novel"
                />
                <Button
                  onClick={async () => {
                    try {
                      const path = await pickDirectory();
                      if (path) setImportPath(path);
                      else
                        setToast(
                          "Enter a local path in the browser preview, or use the desktop folder picker",
                        );
                    } catch (e) {
                      setError(message(e));
                    }
                  }}
                >
                  {t("Choose folder")}
                </Button>
              </div>
            </label>
            <Button
              primary
              disabled={busy || !importPath.trim()}
              onClick={importLegacy}
            >
              {busy ? t("Importing…") : t("Import legacy project")}
            </Button>
            <div className="settings-divider" />
            <h3>{t("Restore project backup")}</h3>
            <p className="muted">
              {t("Import a .quillweave.json backup as a separate project.")}
            </p>
            <Button
              disabled={busy}
              onClick={() => archiveFile.current?.click()}
            >
              <IconUpload size={17} />
              {t("Choose backup file")}
            </Button>
            <input
              ref={archiveFile}
              type="file"
              accept=".json"
              hidden
              onChange={(e) => void importArchive(e.target.files?.[0])}
            />
            {error && <ErrorBox>{error}</ErrorBox>}
          </div>
        </Modal>
      )}
    </div>
  );
}
