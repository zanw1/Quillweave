import { t } from "../../lib/i18n";
import { useRef, useState } from "react";
import {
  IconPlus,
  IconSearch,
  IconFileText,
  IconExternalLink,
  IconSparkles,
  IconArrowUpRight,
} from "@tabler/icons-react";
import { api, date, message, wordCount } from "../../lib/api";
import {
  Badge,
  Button,
  Drawer,
  Empty,
  ErrorBox,
  Modal,
} from "../../components/ui";
import { Assistant } from "../assistant/Assistant";
import type { ContextItem, Project, Source } from "../../domain/types";

/** Import and retrieve source material with attribution and a separate legacy archive. */
export function Library({
  project,
  onProject,
  onSettings,
}: {
  project: Project;
  onProject: (p: Project) => void;
  onSettings: () => void;
}) {
  const [adding, setAdding] = useState(false);
  const [draft, setDraft] = useState({
    title: "",
    content: "",
    category: "research",
    url: "",
  });
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<ContextItem[] | null>(null);
  const [selected, setSelected] = useState<Source | null>(null);
  const [analyze, setAnalyze] = useState<Source | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const file = useRef<HTMLInputElement>(null);
  const kinds: Record<string, string> = {
    research: "Reference",
    craft: "Writing methods",
    world: "Worldbuilding",
    legacy: "Legacy archive",
  };
  /** Read a selected UTF-8 file into the local import form. */
  async function readFile(selectedFile?: File) {
    if (!selectedFile) return;
    if (selectedFile.size > 5_000_000) {
      setError("The file exceeds 5 MB. Split it before importing.");
      return;
    }
    try {
      const content = new TextDecoder("utf-8", { fatal: true }).decode(
        await selectedFile.arrayBuffer(),
      );
      setDraft((previous) => ({
        ...previous,
        title: selectedFile.name,
        content,
      }));
    } catch {
      setError(
        "Import UTF-8 TXT or Markdown files. Convert the encoding if needed.",
      );
    }
  }
  /** Save sources for later author-triggered Wiki compilation. */
  async function add() {
    setBusy(true);
    setError("");
    try {
      onProject(
        await api<Project>("add_source", {
          project_id: project.id,
          revision: project.revision,
          ...draft,
        }),
      );
      setAdding(false);
      setDraft({ title: "", content: "", category: "research", url: "" });
      setResults(null);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }
  /** Retrieve actual Go source excerpts with their provenance. */
  async function search() {
    setError("");
    setBusy(true);
    try {
      setResults(
        query.trim()
          ? await api<ContextItem[]>("search", {
              project_id: project.id,
              query,
            })
          : null,
      );
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className={`library-layout ${analyze ? "with-analysis" : ""}`}>
      <section className="workspace-page library-page">
        <div className="section-title">
          <div>
            <p className="eyebrow">{t("A PLACE FOR EVERYTHING")}</p>
            <h1>{t("Library")}</h1>
            <p>{t("Keep the sources behind your ideas.")}</p>
          </div>
          <Button primary onClick={() => setAdding(true)}>
            <IconPlus size={18} />
            {t("Add source")}
          </Button>
        </div>
        <form
          className="library-search"
          onSubmit={(e) => {
            e.preventDefault();
            void search();
          }}
        >
          <label className="search">
            <IconSearch size={19} />
            <input
              aria-label={t("Search sources")}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("Search keywords across the project, character facts, and sources…")}
            />
          </label>
          <Button primary disabled={busy} type="submit">
            {t("Search")}
          </Button>
          {results && (
            <Button
              onClick={() => {
                setResults(null);
                setQuery("");
              }}
            >
              {t("All sources")}
            </Button>
          )}
        </form>
        <p className="search-caption">
          {t("Local keyword search · Multilingual text · Linked sources")}
        </p>
        {error && <ErrorBox>{error}</ErrorBox>}
        {results !== null ? (
          <div className="search-results">
            {results.length ? (
              results.map((item) => (
                <article className="search-result" key={item.id}>
                  <div>
                    <IconFileText size={18} />
                    <h3>{item.title}</h3>
                    <Badge>{t(kinds[item.kind] || item.kind)}</Badge>
                  </div>
                  <p className="preserve">{item.text}</p>
                  <small>{item.id}</small>
                </article>
              ))
            ) : (
              <Empty
                title={t("No matching excerpts")}
                detail={t("Try a character, location, or a more specific keyword.")}
              />
            )}
          </div>
        ) : project.sources.length ? (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>{t("Source title")}</th>
                  <th>{t("Category")}</th>
                  <th>{t("Length")}</th>
                  <th>{t("Added")}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {project.sources.map((source) => (
                  <tr key={source.id}>
                    <td>
                      <button
                        className="entity-link"
                        onClick={() => setSelected(source)}
                      >
                        <span className="entity-icon">
                          <IconFileText size={20} />
                        </span>
                        <strong>{source.title}</strong>
                      </button>
                    </td>
                    <td>
                      <Badge>{t(kinds[source.category])}</Badge>
                    </td>
                    <td>
                      {wordCount(source.content).toLocaleString()}{" "}{t("characters")}
                    </td>
                    <td className="muted">{date(source.created_at)}</td>
                    <td>
                      <button
                        className="icon-button"
                        aria-label={t("View source {0}", source.title)}
                        onClick={() => setSelected(source)}
                      >
                        <IconArrowUpRight size={17} />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <Empty
            title={t("Build a library for your story")}
            detail={t("Import TXT or Markdown, or paste source text and its citation. Sources are stored locally and sent to your model when you analyze them.")}
          >
            <Button onClick={() => setAdding(true)}>
              <IconPlus size={17} />
              {t("Add your first source")}
            </Button>
          </Empty>
        )}
        {adding && (
          <Modal title={t("Add source")} wide onClose={() => setAdding(false)}>
            <form
              className="modal-content"
              onSubmit={(e) => {
                e.preventDefault();
                void add();
              }}
            >
              <div className="form-grid">
                <label>
                  {t("Source title")}
                  <input
                    autoFocus
                    value={draft.title}
                    onChange={(e) =>
                      setDraft({ ...draft, title: e.target.value })
                    }
                    required
                  />
                </label>
                <label>
                  {t("Category")}
                  <select
                    value={draft.category}
                    onChange={(e) =>
                      setDraft({ ...draft, category: e.target.value })
                    }
                  >
                    <option value="research">{t("Reference")}</option>
                    <option value="craft">{t("Writing methods")}</option>
                    <option value="world">{t("Worldbuilding")}</option>
                  </select>
                </label>
              </div>
              <label>
                {t("Source URL (optional)")}
                <input
                  type="url"
                  value={draft.url}
                  onChange={(e) => setDraft({ ...draft, url: e.target.value })}
                  placeholder="https://…"
                />
              </label>
              <div className="label-row">
                <span>{t("Source content")}</span>
                <Button onClick={() => file.current?.click()}>
                  <IconFileText size={15} />
                  {t("Read TXT / Markdown")}
                </Button>
                <input
                  ref={file}
                  type="file"
                  accept=".txt,.md,.markdown"
                  hidden
                  onChange={(e) => void readFile(e.target.files?.[0])}
                />
              </div>
              <textarea
                className="source-input"
                aria-label={t("Source content")}
                value={draft.content}
                onChange={(e) =>
                  setDraft({ ...draft, content: e.target.value })
                }
                placeholder={t("Paste material you can use, including its author, publication date, and original URL…")}
                required
              />
              {error && <ErrorBox>{error}</ErrorBox>}
              <div className="modal-actions">
                <Button onClick={() => setAdding(false)}>{t("Cancel")}</Button>
                <Button
                  primary
                  type="submit"
                  disabled={
                    busy || !draft.title.trim() || !draft.content.trim()
                  }
                >
                  {t("Save source")}
                </Button>
              </div>
            </form>
          </Modal>
        )}
        {selected && (
          <Drawer
            title={selected.title}
            subtitle={t("Library / Source details")}
            onClose={() => setSelected(null)}
          >
            <div className="drawer-subtitle">
              <Badge>{t(kinds[selected.category])}</Badge>
              <span>{wordCount(selected.content)}{" "}{t("characters")}</span>
            </div>
            {/^https?:\/\//.test(selected.url) && (
              <a
                className="source-url"
                href={selected.url}
                target="_blank"
                rel="noreferrer"
              >
                {t("Open original source")}{" "}<IconExternalLink size={16} />
              </a>
            )}
            <pre className="source-text">{selected.content}</pre>
            <div className="drawer-section">
              <Button
                primary
                onClick={() => {
                  setAnalyze(selected);
                  setSelected(null);
                }}
              >
                <IconSparkles size={17} />
                {t("Compile this source with AI")}
              </Button>
              <p className="muted">
                {t("The complete source is analyzed in chunks. Proposed facts enter the Wiki review queue.")}
              </p>
            </div>
          </Drawer>
        )}
      </section>
      {analyze && (
        <div className="library-assistant">
          <div className="analysis-close">
            <button className="text-button" onClick={() => setAnalyze(null)}>
              {t("Collapse source analysis")}
            </button>
          </div>
          <Assistant
            key={analyze.id}
            project={project}
            source={analyze}
            initialAction="wiki"
            onSettings={onSettings}
            onRefresh={async () =>
              onProject(await api("get_project", { project_id: project.id }))
            }
          />
        </div>
      )}
    </div>
  );
}
