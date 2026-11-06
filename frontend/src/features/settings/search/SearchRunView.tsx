import { Badge, Button, MarkdownResponse } from "../../../components/ui";
import { date } from "../../../lib/api";
import { t } from "../../../lib/i18n";
import type { SearchRun } from "../../../domain/search";

const statuses: Record<string, string> = {
  queued: "Waiting to search",
  running: "Searching",
  succeeded: "Search finished",
  empty: "Connected, no results",
  failed: "Search failed",
  cancelled: "Search cancelled",
  interrupted: "Search interrupted",
};
const verdicts: Record<string, string> = {
  passed: "Search verified",
  connected_empty: "Connected, no results",
  inconclusive: "Search not verified",
  failed: "Search failed",
};
export const readingNames: Record<string, string> = {
  search_excerpt: "Search excerpt",
  provider_passage: "Provider-selected passage",
  fetched_page: "Fetched page",
  model_summary: "Provider summary",
};

/** Display evidence and reported usage without presenting a model's claim or missing cost as successful free search. */
export function SearchRunView({
  run,
  onCancel,
}: {
  run: SearchRun;
  onCancel?: () => void;
}) {
  const usage = run.response.usage;
  return (
    <section className="search-run-view" aria-label={t("Search run status")}>
      <div className="search-run-heading">
        <Badge
          tone={
            run.status === "failed"
              ? "red"
              : run.status === "succeeded"
                ? "green"
                : "gray"
          }
        >
          {t(statuses[run.status] || run.status)}
        </Badge>
        <span>{run.effective_profile.profile.name}</span>
        {["queued", "running"].includes(run.status) && onCancel && (
          <Button size="sm" onClick={onCancel}>
            {t("Cancel search")}
          </Button>
        )}
      </div>
      <p>
        <strong>
          {t(
            run.effective_profile.profile.mode === "native" ||
              run.effective_profile.profile.family === "openrouter"
              ? "Research question"
              : "Executed query",
          )}
          :
        </strong>{" "}
        {run.query || run.url}
      </p>
      {run.response.search_state === "not_triggered" && (
        <p className="studio-notice">
          {t(
            "The model answered without verified browsing. This is not a successful web-search test.",
          )}
        </p>
      )}
      {run.error && (
        <p role="alert" className="error-box">
          {t(run.error.message)}
        </p>
      )}
      {run.response.warnings.map((warning, index) => (
        <p className="studio-notice" key={index}>
          {t(warning)}
        </p>
      ))}
      <div className="search-usage">
        <span>
          {t("Reserved attempts")}: {usage.attempts_reserved}
        </span>
        <span>
          {t("Reported searches")}: {usage.search_calls ?? t("Not reported")}
        </span>
        <span>
          {t("Reported fetches")}: {usage.fetch_calls ?? t("Not reported")}
        </span>
        <span>
          {t("Model tokens")}:{" "}
          {usage.model_input_tokens === null &&
          usage.model_output_tokens === null
            ? t("Not reported")
            : `${usage.model_input_tokens ?? "?"} / ${usage.model_output_tokens ?? "?"}`}
        </span>
        <span>
          {t("Reported cost")}:{" "}
          {usage.reported_cost === null
            ? t("Not reported")
            : `${usage.reported_cost} ${usage.currency || ""}`}
        </span>
      </div>
      {run.attempts.length > 1 && (
        <ol className="search-attempts">
          {run.attempts.map((attempt, index) => (
            <li key={index}>
              {attempt.provider} · {t(attempt.status)}
              {attempt.error ? ` · ${t(attempt.error.message)}` : ""}
            </li>
          ))}
        </ol>
      )}
      {run.purpose === "test" &&
        ["succeeded", "empty", "failed", "cancelled", "interrupted"].includes(
          run.status,
        ) && (
          <p>
            <strong>{t("Test verdict")}:</strong>{" "}
            {t(
              verdicts[run.response.test_verdict] || run.response.test_verdict,
            )}
          </p>
        )}
      <small>
        {t("Started")}: {date(run.created_at)} · {t("Actual engine")}:{" "}
        {run.response.engine || t("Not reported")}
      </small>
      {run.response.answer && (
        <div className="search-answer">
          <h3>{t("Research answer")}</h3>
          <MarkdownResponse text={run.response.answer.text} />
          <ul>
            {run.response.answer.citations.map((citation) => (
              <li key={citation.url}>
                <a href={citation.url} target="_blank" rel="noreferrer">
                  {citation.title || citation.url}
                </a>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}
