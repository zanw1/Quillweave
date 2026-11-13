import { Select } from "../../components/Select";
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
  RequiredLabel,
} from "../../components/ui";
import { Assistant } from "../assistant/Assistant";
import type { ContextItem, Project, Source } from "../../domain/types";
import {
  decodeReferenceText,
  referenceChapters,
  type ReferenceChapter,
} from "../../lib/referenceText";

/** Import and retrieve source material with attribution and a separate legacy archive. */
export function Library({
  project,
  onProject,
  onSettings,
  onAskSource,
}: {
  project: Project;
  onProject: (p: Project) => void;
  onSettings: () => void;
  onAskSource?: (source: Source) => void;
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
  const [reference, setReference] = useState<{
    text: string;
    name: string;
    encoding: string;
    chapters: ReferenceChapter[];
  } | null>(null);
  const [chapterRange, setChapterRange] = useState({ start: 1, end: 1 });
  const file = useRef<HTMLInputElement>(null);
  const kinds: Record<string, string> = {
    research: "Reference",
    craft: "Writing methods",
    world: "Worldbuilding",
    legacy: "Legacy archive",
  };
  /** Select an attributable chapter range without sending an entire long novel to the model. */
  function selectReferenceRange(
    document: NonNullable<typeof reference>,
    start: number,
    end: number,
  ) {
    const first = document.chapters[Math.max(0, start - 1)];
    const last =
      document.chapters[Math.min(document.chapters.length - 1, end - 1)];
    if (!first || !last || start > end) return;
    setChapterRange({ start, end });
    setDraft((previous) => ({
      ...previous,
      title: `${document.name.replace(/\.[^.]+$/, "").slice(0, 75)} · ${start}–${end}`,
      category: "craft",
      content: `[Writing reference]\nFile ${document.name}\nEncoding ${document.encoding}\nOriginal lines ${first.line}–${last.endLine}\nSelected headings ${first.title} / ${last.title}\n\n${document.text.slice(first.start, last.end)}`,
    }));
  }

  /** Decode a selected local text and expose chapter ranges before importing the chosen source. */
  async function readFile(selectedFile?: File) {
    if (!selectedFile) return;
    if (selectedFile.size > 100_000_000) {
      setError("The reference file exceeds 100 MB.");
      return;
    }
    try {
      const decoded = decodeReferenceText(await selectedFile.arrayBuffer());
      const chapters = referenceChapters(decoded.text);
      setError("");
      if (chapters.length) {
        const document = { ...decoded, name: selectedFile.name, chapters };
        setReference(document);
        selectReferenceRange(document, 1, 1);
        return;
      }
      setReference(null);
      setDraft((previous) => ({
        ...previous,
        title: selectedFile.name,
        content: decoded.text,
      }));
    } catch {
      setError("Unable to decode this file as UTF-8, UTF-16 or GB18030 text.");
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
      setReference(null);
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
              placeholder={t(
                "Search keywords across the project, character facts, and sources…",
              )}
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
                detail={t(
                  "Try a character, location, or a more specific keyword.",
                )}
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
                      {wordCount(source.content).toLocaleString()}{" "}
                      {t("characters")}
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
            detail={t(
              "Import TXT or Markdown, or paste source text and its citation. Sources are stored locally and sent to your model when you analyze them.",
            )}
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
                  <RequiredLabel>{t("Source title")}</RequiredLabel>
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
                  <Select
                    value={draft.category}
                    onChange={(e) =>
                      setDraft({ ...draft, category: e.target.value })
                    }
                  >
                    <option value="research">{t("Reference")}</option>
                    <option value="craft">{t("Writing methods")}</option>
                    <option value="world">{t("Worldbuilding")}</option>
                  </Select>
                </label>
              </div>
              {reference && (
                <div className="reference-import-range">
                  <p>
                    {t("Detected encoding")} · {reference.encoding} ·{" "}
                    {t("Detected chapters")} {reference.chapters.length}
                  </p>
                  <p className="muted">
                    {t(
                      "Choose a chapter range to preserve original line references. Duplicate headings keep their original order.",
                    )}
                  </p>
                  <div className="form-grid">
                    <label>
                      {t("First chapter position")}
                      <input
                        type="number"
                        min={1}
                        max={chapterRange.end}
                        value={chapterRange.start}
                        onChange={(event) =>
                          selectReferenceRange(
                            reference,
                            Number(event.target.value),
                            chapterRange.end,
                          )
                        }
                      />
                    </label>
                    <label>
                      {t("Last chapter position")}
                      <input
                        type="number"
                        min={chapterRange.start}
                        max={reference.chapters.length}
                        value={chapterRange.end}
                        onChange={(event) =>
                          selectReferenceRange(
                            reference,
                            chapterRange.start,
                            Number(event.target.value),
                          )
                        }
                      />
                    </label>
                  </div>
                </div>
              )}
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
              <label>
                <RequiredLabel>{t("Source content")}</RequiredLabel>
                <textarea
                  className="source-input"
                  aria-label={t("Source content")}
                  value={draft.content}
                  onChange={(e) =>
                    setDraft({ ...draft, content: e.target.value })
                  }
                  placeholder={t(
                    "Paste material you can use, including its author, publication date, and original URL…",
                  )}
                  required
                />
              </label>
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
              <span>
                {wordCount(selected.content)} {t("characters")}
              </span>
            </div>
            {/^https?:\/\//.test(selected.url) && (
              <a
                className="source-url"
                href={selected.url}
                target="_blank"
                rel="noreferrer"
              >
                {t("Open original source")} <IconExternalLink size={16} />
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
                {t("Compile this source into the Wiki")}
              </Button>
              {onAskSource && (
                <Button
                  onClick={() => {
                    onAskSource(selected);
                    setSelected(null);
                  }}
                >
                  <IconArrowUpRight size={16} />
                  {t("Ask about this source")}
                </Button>
              )}
              <p className="muted">
                {t(
                  "The complete source is analyzed in chunks. Proposed facts enter the Wiki review queue.",
                )}
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
