import { useState } from "react";
import {
  IconFeather,
  IconBook2,
  IconPlus,
  IconSearch,
  IconLayoutGrid,
  IconList,
  IconArrowUpRight,
  IconUpload,
  IconTrash,
  IconArrowRight,
  IconCircleCheck,
} from "@tabler/icons-react";
import { t } from "../../lib/i18n";
import { date } from "../../lib/api";
import { Badge, Button, Empty } from "../../components/ui";
import type { ProjectCard } from "../../domain/types";

interface Props {
  projects: ProjectCard[];
  loading: boolean;
  busy: boolean;
  onOpen: (id: string) => void;
  onDelete: (id: string) => void;
  onCreate: () => void;
  onImport: () => void;
  onExample: () => void;
}

/** Own project browsing controls while project lifecycle remains in the application shell. */
export function ProjectHome({
  projects,
  loading,
  busy,
  onOpen,
  onDelete,
  onCreate,
  onImport,
  onExample,
}: Props) {
  const [query, setQuery] = useState("");
  const [layout, setLayout] = useState("list");
  const filtered = projects.filter((project) =>
    `${project.title} ${project.genre}`
      .toLocaleLowerCase()
      .includes(query.toLocaleLowerCase()),
  );
  return (
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
          <Button primary onClick={onCreate}>
            <IconPlus size={19} />
            {t("Start a new story")}
          </Button>
          <button className="text-button" onClick={onExample} disabled={busy}>
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
          <Button onClick={onImport}>
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
                        onClick={() => onOpen(p.id)}
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
                        {p.word_count.toLocaleString()} {t("characters")}
                      </strong>
                      <small className="cell-sub">
                        {p.chapter_count}{" "}
                        {p.chapter_count === 1 ? t("chapter") : t("chapters")}
                      </small>
                    </td>
                    <td className="muted">{date(p.updated_at)}</td>
                    <td>
                      <div className="project-actions">
                        <button
                          className="icon-button"
                          aria-label={t("Open {0}", p.title)}
                          disabled={busy}
                          onClick={() => onOpen(p.id)}
                        >
                          <IconArrowUpRight size={18} />
                        </button>
                        <button
                          className="icon-button danger"
                          aria-label={t("Delete {0}", p.title)}
                          title={t("Delete project")}
                          disabled={busy}
                          onClick={() => onDelete(p.id)}
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
                  onClick={() => onOpen(p.id)}
                >
                  <span className="project-icon">
                    <IconBook2 size={27} />
                  </span>
                  <Badge>{p.genre || t("New project")}</Badge>
                  <h3>{p.title}</h3>
                  <p>{p.premise || t("A new story is taking shape.")}</p>
                  <span className="project-card-meta">
                    <span>
                      {p.chapter_count}{" "}
                      {p.chapter_count === 1 ? t("chapter") : t("chapters")} ·{" "}
                      {p.word_count.toLocaleString()} {t("characters")}
                    </span>
                    <IconArrowUpRight size={18} />
                  </span>
                </button>
                <button
                  className="icon-button danger project-card-delete"
                  aria-label={t("Delete {0}", p.title)}
                  title={t("Delete project")}
                  disabled={busy}
                  onClick={() => onDelete(p.id)}
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
            query ? t("No matching project") : t("Your first story starts here")
          }
          detail={
            query
              ? t("Try a project title or genre keyword.")
              : t(
                  "Create a project, import an older one, or explore the sample.",
                )
          }
        >
          <Button onClick={onCreate}>
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
  );
}
