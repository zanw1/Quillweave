import { t, useLanguage, setLanguage } from "../lib/i18n";
import { useEffect, useRef, useState } from "react";
import {
  IconFeather,
  IconHome,
  IconBook2,
  IconPencil,
  IconFolder,
  IconBulb,
  IconSettings,
  IconChevronDown,
  IconPlus,
  IconSearch,
  IconLayoutGrid,
  IconList,
  IconArrowUpRight,
  IconDownload,
  IconUpload,
  IconArrowRight,
  IconCircleCheck,
  IconLayoutSidebarLeftCollapse,
  IconLayoutSidebarLeftExpand,
} from "@tabler/icons-react";
import { api, date, download, message, pickDirectory } from "../lib/api";
import { Badge, Button, Empty, ErrorBox, Modal } from "../components/ui";
import { Writing, Planning } from "../features/writing/Editor";
import { WritingParameters } from "../features/writing/WritingParameters";
import { Wiki } from "../features/wiki/Wiki";
import { Library } from "../features/library/Library";
import { Methods } from "../features/methods/Methods";
import { Settings } from "../features/settings/Settings";
import type { Page, Project, ProjectCard } from "../domain/types";
import "../styles/app.css";

const navigation = [
  { page: "write" as Page, label: "Chapters", icon: IconPencil },
  { page: "plan" as Page, label: "Story planning", icon: IconBook2 },
  { page: "wiki" as Page, label: "Character Wiki", icon: IconBook2 },
  { page: "knowledge" as Page, label: "Library", icon: IconFolder },
];
const pageNames: Record<Page, string> = {
  home: "My projects",
  write: "Chapters",
  plan: "Story planning",
  wiki: "Character Wiki",
  knowledge: "Library",
  methods: "Writing methods",
  settings: "Model settings",
};

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
  const [form, setForm] = useState({
    title: "",
    genre: "",
    premise: "",
    writing_style: "custom",
    default_chapter_words: 3000,
    narrative_pov: "Third-person limited",
  });
  const [query, setQuery] = useState("");
  const [layout, setLayout] = useState("list");
  const [seed, setSeed] = useState("");
  const [sidebar, setSidebar] = useState(() => window.innerWidth > 640);
  const archiveFile = useRef<HTMLInputElement>(null);

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
    setLoading(true);
    setError("");
    try {
      const list = await api<ProjectCard[]>("list_projects");
      setProjects(list);
      const current = localStorage.getItem("quillweave-project");
      if (current && list.some((p) => p.id === current)) {
        const p = await api<Project>("get_project", { project_id: current });
        setProject(p);
        setChapterId(p.chapters[0]?.id || "");
      }
    } catch (e) {
      setError(message(e));
    } finally {
      setLoading(false);
    }
  }

  /** Update project data and its home summary while retaining the chapter selection. */
  function update(next: Project) {
    setProject((current) => (current?.id === next.id ? next : current));
    api<ProjectCard[]>("list_projects")
      .then(setProjects)
      .catch((e) => setError(message(e)));
  }

  /** Open an existing chapter or direct an empty project to Story planning. */
  async function openProject(id: string) {
    setError("");
    try {
      const p = await api<Project>("get_project", { project_id: id });
      enter(p);
    } catch (e) {
      setError(message(e));
    }
  }

  /** Enter the shared editing flow for newly created, imported, or sample projects. */
  function enter(p: Project) {
    setProject(p);
    localStorage.setItem("quillweave-project", p.id);
    update(p);
    setChapterId(p.chapters[0]?.id || "");
    setPage(p.chapters.length ? "write" : "plan");
  }

  /** Create an empty offline project. */
  async function newProject() {
    setError("");
    setBusy(true);
    try {
      enter(await api<Project>("create_project", form));
      setCreate(false);
      setForm({
        title: "",
        genre: "",
        premise: "",
        writing_style: "custom",
        default_chapter_words: 3000,
        narrative_pov: "Third-person limited",
      });
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
      if (file.size > 40_000_000)
        throw new Error("The backup exceeds the 40 MB import limit");
      enter(
        await api<Project>("import_archive", { content: await file.text() }),
      );
      setImporting(false);
      setToast("Backup restored as a separate project");
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  /** Export saved prose, story setup, evidence, and versions through a credential-free backup. */
  async function exportProject(format: string) {
    if (!project) return;
    try {
      const result = await api<{ filename: string; content: string }>(
        "export",
        { project_id: project.id, format },
      );
      await download(result.filename, result.content);
      setToast("Exported the saved version");
    } catch (e) {
      setError(message(e));
    }
  }

  /** Bring a writing-method question into the composer for explicit submission. */
  function useMethod(prompt: string) {
    if (!project?.chapters.length) {
      setToast("Open a project and create a chapter to use a writing method");
      return;
    }
    setSeed(prompt);
    setPage("write");
  }

  const filtered = projects.filter((p) =>
    `${p.title} ${p.genre}`.includes(query),
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
          <button className="brand" onClick={() => setPage("home")}>
            <span className="brand-mark">
              <IconFeather size={27} stroke={1.7} />
            </span>
            <span>
              Quillweave<small>{t("LOCAL WRITING")}</small>
            </span>
          </button>
          <button className="workspace-picker" onClick={() => setPage("home")}>
            <span className="workspace-monogram">
              <IconBook2 size={20} />
            </span>
            <span>
              {t("My workspace")}<small>{t("Local workspace")}</small>
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
            {project ? project.title : t("Start a new project")}
          </div>
          {navigation.map((item) => (
            <button
              key={item.page}
              className={`nav-item ${page === item.page ? "active" : ""}`}
              disabled={!project}
              onClick={() => setPage(item.page)}
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
              <select
                aria-label={t("Interface language")}
                value={language}
                onChange={(event) =>
                  setLanguage(event.target.value === "en" ? "en" : "zh-CN")
                }
              >
                <option value="zh-CN">中文</option>
                <option value="en">English</option>
              </select>
            </label>
            {project && selectedPage && (
              <details className="export-menu">
                <summary>
                  <IconDownload size={17} />
                  <span>{t("Export project")}</span>
                </summary>
                <div>
                  <button onClick={() => exportProject("manuscript")}>
                    {t("Manuscript Markdown")}
                  </button>
                  <button onClick={() => exportProject("wiki")}>
                    {t("Character Wiki Markdown")}
                  </button>
                  <button onClick={() => exportProject("archive")}>
                    {t("Full project backup")}
                  </button>
                  <small>{t("Export the latest saved version")}</small>
                </div>
              </details>
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
                  {t("From an idea to a whole world.")}{" "}<br />{t("A home for your prose, characters, and ideas.")}
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
                    {t("Explore the sample")}{" "}<IconArrowRight size={17} />
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
                  {t("My projects")}{" "}<span>{projects.length}</span>
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
                                {p.word_count.toLocaleString()}{" "}{t("characters")}
                              </strong>
                              <small className="cell-sub">
                                {p.chapter_count}{" "}
                                {p.chapter_count === 1 ? t("chapter") : t("chapters")}
                              </small>
                            </td>
                            <td className="muted">{date(p.updated_at)}</td>
                            <td>
                              <button
                                className="icon-button"
                                aria-label={t("Open {0}", p.title)}
                                onClick={() => openProject(p.id)}
                              >
                                <IconArrowUpRight size={18} />
                              </button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <div className="table-footer">
                      {projects.length}{" "}
                      {projects.length === 1 ? t("project") : t("projects")}{" "}{t("· Every story deserves an ending")}
                    </div>
                  </div>
                ) : (
                  <div className="project-grid">
                    {filtered.map((p) => (
                      <button
                        className="project-card"
                        key={p.id}
                        onClick={() => openProject(p.id)}
                      >
                        <span className="project-icon">
                          <IconBook2 size={27} />
                        </span>
                        <Badge>{p.genre || t("New project")}</Badge>
                        <h3>{p.title}</h3>
                        <p>{p.premise || t("A new story is taking shape.")}</p>
                        <footer>
                          <span>
                            {p.chapter_count}{" "}
                            {p.chapter_count === 1 ? t("chapter") : t("chapters")} ·{" "}
                            {p.word_count.toLocaleString()}{" "}{t("characters")}
                          </span>
                          <IconArrowUpRight size={18} />
                        </footer>
                      </button>
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
                      : t("Create a project, import an older one, or explore the sample.")
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
                  {t("Write offline and use AI when you choose. Projects and versions are stored locally.")}
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
            />
          )}
          {page === "plan" && project && (
            <Planning
              key={project.id}
              project={project}
              onProject={update}
              onSettings={() => setPage("settings")}
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
      {create && (
        <Modal
          title={t("Start a new story")}
          onClose={() => {
            setCreate(false);
            setError("");
          }}
        >
          <form
            className="modal-content"
            onSubmit={(e) => {
              e.preventDefault();
              void newProject();
            }}
          >
            <label>
              {t("Project title")}
              <input
                autoFocus
                value={form.title}
                onChange={(e) => setForm({ ...form, title: e.target.value })}
                placeholder={t("Give your story a title")}
                maxLength={120}
                required
              />
            </label>
            <label>
              {t("Genre")}
              <input
                value={form.genre}
                onChange={(e) => setForm({ ...form, genre: e.target.value })}
                placeholder={t("For example, mystery, fantasy, contemporary, literary fiction")}
              />
            </label>
            <WritingParameters
              value={form}
              onChange={(patch) => setForm({ ...form, ...patch })}
            />
            <label>
              {t("Initial idea")}
              <textarea
                className="large-input"
                value={form.premise}
                onChange={(e) => setForm({ ...form, premise: e.target.value })}
                placeholder={t("Who wants what, and what stands in the way? You can fill this in later.")}
              />
            </label>
            {error && <ErrorBox>{error}</ErrorBox>}
            <div className="modal-actions">
              <Button onClick={() => setCreate(false)}>{t("Cancel")}</Button>
              <Button
                primary
                type="submit"
                disabled={busy || !form.title.trim()}
              >
                {t("Create project")}
              </Button>
            </div>
          </form>
        </Modal>
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
              {t("Select the project folder containing chapters/ or Novel_architecture.txt.")}
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
