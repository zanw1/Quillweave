import { pageNames } from "./navigation";
import { NewStoryDialog, WorkspaceExport } from "./WorkspaceControls";
import { Select } from "../components/Select";
import { clearProjectStorage } from "./storage";
import { clearProjectDrafts } from "../features/writing/drafts";
import { t, useLanguage, setLanguage } from "../lib/i18n";
import { useEffect, useRef, useState } from "react";
import {
  IconBook2,
  IconPlus,
  IconSearch,
  IconSparkles,
  IconUpload,
  IconTrash,
  IconCircleCheck,
  IconLayoutSidebarLeftCollapse,
  IconLayoutSidebarLeftExpand,
} from "@tabler/icons-react";
import { api, message, pickDirectory } from "../lib/api";
import { restoreArchiveFile } from "../lib/archive";
import { Badge, Button, ErrorBox, Modal } from "../components/ui";
import { FlowSidebar } from "../features/workspace/FlowSidebar";
import { ProjectHome } from "../features/workspace/ProjectHome";
import { Workspace } from "../features/workspace/Workspace";
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
  const [sidebar, setSidebar] = useState(() => window.innerWidth > 640);
  const [projectPicker, setProjectPicker] = useState<Page | "auto" | null>(
    null,
  );
  const [pickerQuery, setPickerQuery] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<Project | null>(null);
  const [deleteError, setDeleteError] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [sourcesOpen, setSourcesOpen] = useState(false);
  const [assistantOpen, setAssistantOpen] = useState(
    () => window.innerWidth >= 1280,
  );
  const deletingRef = useRef(false);
  const projectRequest = useRef(0);
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
    setProject((current) => {
      if (current?.id !== next.id || current.revision > next.revision)
        return current;
      const candidates = new Map(
        (next.studio.changesets || []).map((item) => [item.id, item]),
      );
      for (const item of current.studio.changesets || []) {
        const incoming = candidates.get(item.id);
        if (!incoming || incoming.updated_at < item.updated_at)
          candidates.set(item.id, item);
      }
      return {
        ...next,
        studio: { ...next.studio, changesets: [...candidates.values()] },
      };
    });
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
    setBusy(false);
    setProjectPicker(null);
    setChapterId(p.chapters[0]?.id || "");
    setPage(destination || "plan");
    setSourcesOpen(destination === "knowledge");
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
        setPage("home");
      }
      clearProjectStorage(deleteTarget.id);
      clearProjectDrafts(deleteTarget.id);
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
      const restored = await restoreArchiveFile(file);
      enter(restored);
      setImporting(false);
      setToast(restored.import_warnings?.length ? "Backup restored; retired experimental data was skipped" : "Backup restored as a separate project");
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }

  /** Route the four primary destinations while research opens beside the active workspace. */
  function selectFlowStep(stepKey: string) {
    if (stepKey === "research") {
      setSourcesOpen(true);
      return;
    }
    const destination: Record<string, Page> = {
      setup: "plan",
      world: "wiki",
      plan: "outline",
      write: "write",
    };
    navigateProject(destination[stepKey] || "plan");
  }

  const pickerProjects = projects.filter((p) =>
    `${p.title} ${p.genre}`
      .toLocaleLowerCase()
      .includes(pickerQuery.trim().toLocaleLowerCase()),
  );
  const chapter =
    project?.chapters.find((c) => c.id === chapterId) || project?.chapters[0];
  const selectedPage = [
    "write",
    "plan",
    "outline",
    "wiki",
    "knowledge",
  ].includes(page);
  const activeFlowStep =
    page === "write"
      ? "write"
      : page === "wiki"
        ? "world"
        : page === "outline"
          ? "plan"
          : "setup";
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
        <FlowSidebar
          page={page}
          project={project}
          loading={loading}
          busy={busy}
          selectedPage={selectedPage}
          activeFlowStep={activeFlowStep}
          onHome={() => setPage("home")}
          onSwitchProject={switchProject}
          onSelectFlowStep={selectFlowStep}
          onPage={setPage}
        />
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
          <div className="topbar-right">
            {project && selectedPage && (
              <>
                <Button onClick={() => setSourcesOpen(true)}>
                  <IconSearch size={17} />
                  {t("Sources & research")}
                </Button>
                <Button
                  aria-expanded={assistantOpen}
                  onClick={() => setAssistantOpen(!assistantOpen)}
                >
                  <IconSparkles size={17} />
                  {t("Creative partner")}
                </Button>
              </>
            )}
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
            <ProjectHome
              projects={projects}
              loading={loading}
              busy={busy}
              onOpen={openProject}
              onDelete={requestDelete}
              onCreate={() => {
                setError("");
                setCreate(true);
              }}
              onImport={() => {
                setError("");
                setImporting(true);
              }}
              onExample={example}
            />
          )}
          {project && selectedPage && (
            <Workspace
              key={project.id}
              project={project}
              chapter={chapter}
              page={page}
              activeStep={activeFlowStep}
              assistantOpen={assistantOpen}
              sourcesOpen={sourcesOpen}
              onAssistantOpen={setAssistantOpen}
              onSourcesClose={() => setSourcesOpen(false)}
              onProject={update}
              onChapter={setChapterId}
              onStep={selectFlowStep}
              onSettings={() => setPage("settings")}
            />
          )}
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
          }}
          onClose={() => {
            setCreate(false);
            setError("");
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
