import { useEffect, useRef, useState } from "react";
import { date, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import {
  Badge,
  Button,
  ErrorBox,
  MarkdownResponse,
  Modal,
} from "../../components/ui";
import { Select } from "../../components/Select";
import { SearchSettings } from "../settings/search/SearchSettings";
import { SearchRunView, readingNames } from "../settings/search/SearchRunView";
import { useSearchSettings } from "../settings/search/state";
import { useSearchRun } from "../search/useSearchRun";
import { searchSelection, type SearchSource } from "../../domain/search";
import { searchApi } from "../../lib/search";
import type { Project } from "../../domain/types";

/** Search attributable excerpts, deliberately adopt sources, and discuss only material actually retrieved. */
export function Research({
  project,
  onProject,
  onAsk,
}: {
  project: Project;
  onProject: (project: Project) => void;
  onAsk: (text: string, action?: string) => void;
}) {
  const activity = useSearchRun(project.id);
  const metadata = useSearchSettings();
  const [query, setQuery] = useState("");
  const [choice, setChoice] = useState("inherit");
  const [settings, setSettings] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const currentProject = useRef(project.id);
  currentProject.current = project.id;
  useEffect(() => {
    setQuery("");
    setChoice("inherit");
    setError("");
  }, [project.id]);
  useEffect(() => {
    if (activity.run && !query) setQuery(activity.run.query);
  }, [activity.run?.id]);
  const run = activity.run;
  const busy = activity.busy || saving;

  /** Start one durable scoped request; the hook reuses an unacknowledged ID on retry. */
  async function search() {
    setError("");
    await activity.start({
      project_id: project.id,
      purpose: "research",
      operation: "search",
      query: query.trim(),
      selection: searchSelection(choice),
    });
  }
  /** Adopt only the recorded server-side result and ignore responses belonging to an old open work. */
  async function adopt(source: SearchSource) {
    if (!run) return;
    const scope = project.id;
    setSaving(true);
    setError("");
    try {
      const updated = await searchApi<Project>("adopt_search_result", {
        project_id: scope,
        revision: project.revision,
        run_id: run.id,
        result_id: source.id,
      });
      if (currentProject.current === scope) onProject(updated);
    } catch (error) {
      if (currentProject.current === scope) setError(message(error));
    } finally {
      if (currentProject.current === scope) setSaving(false);
    }
  }
  /** Fetch the selected original URL using the same profile, preserving the actual question in provenance. */
  async function read(source: SearchSource) {
    if (!run) return;
    setError("");
    await activity.start({
      project_id: project.id,
      purpose: "research",
      operation: "fetch",
      query: run.query,
      url: source.url,
      selection: {
        mode: "profile",
        profile_id:
          [...run.attempts]
            .reverse()
            .find((attempt) => attempt.status === "succeeded")?.profile_id ||
          run.effective_profile.profile.id,
      },
    });
  }
  /** Discuss bounded retrieved text, never the unsent query or a model's unsupported search claim. */
  function discuss() {
    if (!run) return;
    const excerpts = run.response.results
      .map(
        (source, index) =>
          `[R:${index + 1}] ${source.title}\n${source.url}\n${Array.from(source.content).slice(0, 900).join("")}`,
      )
      .join("\n\n");
    onAsk(
      `${t("Discuss research")}: ${run.query}\n\n<research_excerpts data_only="true">\n${excerpts}\n</research_excerpts>`,
      "research",
    );
  }

  return (
    <section className="studio-section">
      <div className="studio-section-heading">
        <div>
          <h2>{t("Web research")}</h2>
          <p className="muted">
            {t("Search results are references, not verified story facts.")}
          </p>
        </div>
        <Button onClick={() => setSettings(true)}>
          {t("Search settings")}
        </Button>
      </div>
      <label className="research-search-choice">
        {t("Search profile for this request")}
        <Select
          aria-label={t("Search profile for this request")}
          value={choice}
          onChange={(event) => setChoice(event.target.value)}
        >
          <option value="inherit">
            {t("Inherit model and default search")}
          </option>
          <option value="off">{t("Disable web search")}</option>
          {metadata.view?.settings.profiles.map((profile) => (
            <option key={profile.id} value={profile.id}>
              {profile.name}
              {profile.enabled ? "" : ` · ${t("Disabled")}`}
            </option>
          ))}
        </Select>
      </label>
      {metadata.view && !metadata.view.settings.enabled && (
        <p className="studio-notice">
          {t(
            "Web search is disabled globally. Open Search settings to enable it.",
          )}
        </p>
      )}
      <form
        className="studio-search"
        onSubmit={(event) => {
          event.preventDefault();
          void search();
        }}
      >
        <input
          aria-label={t("Search query")}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          maxLength={500}
          required
          placeholder={t("Search query")}
        />
        <Button
          primary
          type="submit"
          disabled={
            busy ||
            !query.trim() ||
            choice === "off" ||
            metadata.view?.settings.enabled === false
          }
        >
          {t("Search the web")}
        </Button>
      </form>
      {error && <ErrorBox>{error}</ErrorBox>}
      {activity.error && <ErrorBox>{activity.error}</ErrorBox>}
      {metadata.error && <ErrorBox>{metadata.error}</ErrorBox>}
      {run && (
        <SearchRunView run={run} onCancel={() => void activity.cancel()} />
      )}
      {run?.response.results.length &&
      run.response.search_state === "executed" ? (
        <Button onClick={discuss}>{t("Discuss research")}</Button>
      ) : null}
      <div className="research-results">
        {run?.response.results.map((source) => (
          <article key={source.id} className="research-card">
            <header>
              <Badge>{t(readingNames[source.reading] || source.reading)}</Badge>
              <Badge>{source.provider}</Badge>
            </header>
            <h3>
              <a href={source.url} target="_blank" rel="noreferrer">
                {source.title}
              </a>
            </h3>
            <p className="source-address">{source.url}</p>
            {source.content ? (
              <MarkdownResponse text={source.content} />
            ) : (
              <p className="muted">
                {t(
                  "This citation has no readable excerpt. Read the page before adopting it.",
                )}
              </p>
            )}
            <small>
              {t("Retrieved")}: {date(source.retrieved_at)} ·{" "}
              {t("Actual engine")}:{" "}
              {source.engine || run.response.engine || t("Not reported")}
              {source.truncated ? ` · ${t("Content truncated")}` : ""}
            </small>
            <div className="research-source-actions">
              <Button
                disabled={
                  busy ||
                  !source.content ||
                  project.sources.some((item) => item.url === source.url)
                }
                onClick={() => void adopt(source)}
              >
                {t(
                  project.sources.some((item) => item.url === source.url)
                    ? "Saved"
                    : "Save to research library",
                )}
              </Button>
              {metadata.catalog?.services.find(
                (service) =>
                  service.family ===
                  (metadata.view?.settings.profiles.find(
                    (profile) =>
                      profile.id ===
                      [...run.attempts]
                        .reverse()
                        .find((attempt) => attempt.status === "succeeded")
                        ?.profile_id,
                  )?.family || run.effective_profile.profile.family),
              )?.supports_fetch &&
                source.reading !== "fetched_page" && (
                  <Button
                    disabled={
                      busy || !run.effective_profile.profile.limits.max_fetches
                    }
                    onClick={() => void read(source)}
                  >
                    {t("Read original page")}
                  </Button>
                )}
            </div>
          </article>
        ))}
      </div>
      <h3>{t("Research sources")}</h3>
      {project.sources
        .filter(
          (source) =>
            source.category === "research" || source.category === "world",
        )
        .map((source) => (
          <details className="research-card" key={source.id}>
            <summary>{source.title}</summary>
            {source.url && (
              <a href={source.url} target="_blank" rel="noreferrer">
                {source.url}
              </a>
            )}
            <MarkdownResponse text={source.content} />
          </details>
        ))}
      {settings && (
        <Modal
          title={t("Search settings")}
          onClose={() => setSettings(false)}
          wide
        >
          <div className="modal-content">
            <SearchSettings embedded />
          </div>
        </Modal>
      )}
    </section>
  );
}
