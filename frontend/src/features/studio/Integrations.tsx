import { useEffect, useState } from "react";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import { Button, ErrorBox, SecretField, Switch } from "../../components/ui";
import type { SecretUpdate } from "../../domain/types";
import type {
  IntegrationResponse,
  IntegrationSettings,
} from "../../domain/studio";
import "./studio.css";

/** Configure optional external services while keeping API credentials out of persistent settings. */
export function Integrations({
  embedded = false,
  memoryOnly = false,
}: {
  embedded?: boolean;
  memoryOnly?: boolean;
}) {
  const [data, setData] = useState<IntegrationResponse | null>(null);
  const [updates, setUpdates] = useState<Record<string, SecretUpdate>>({});
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    void api<IntegrationResponse>("get_integrations")
      .then(setData)
      .catch((error) => setError(message(error)));
  }, []);

  /** Update a settings draft without sending network traffic. */
  function edit(patch: Partial<IntegrationSettings>) {
    if (data) setData({ ...data, settings: { ...data.settings, ...patch } });
  }

  /** Persist metadata with explicit preserve, replace and clear operations for each key. */
  async function save() {
    if (!data) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const latest = memoryOnly
        ? await api<IntegrationResponse>("get_integrations")
        : null;
      const settings = latest
        ? {
            ...latest.settings,
            revision: data.settings.revision,
            hindsight_url: data.settings.hindsight_url,
          }
        : data.settings;
      setData(
        await api<IntegrationResponse>("save_integrations", {
          settings,
          key_updates: updates,
        }),
      );
      setUpdates({});
      setNotice(t("Saved"));
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  /** Test the selected saved provider with a small visible query and show actual fallback errors. */
  async function test(id: string) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const result = await api<{ results: unknown[]; warnings: string[] }>(
        "test_search",
        { provider_id: id, query: "fiction worldbuilding" },
      );
      setNotice(
        result.results.length
          ? `${result.results.length} ${t("Search excerpts")}`
          : result.warnings.join("\n") || t("No results"),
      );
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="studio-integrations">
      {!embedded && (
        <h2>
          {t(memoryOnly ? "External memory settings" : "Search settings")}
        </h2>
      )}
      <p className="muted">
        {t(
          "API keys stay in this app process. Enter them again after restarting.",
        )}
      </p>
      {!memoryOnly && (
        <p className="muted">
          {t(
            "Queries are sent to the selected provider. Your complete manuscript is not sent.",
          )}
        </p>
      )}
      {error && <ErrorBox>{error}</ErrorBox>}
      {notice && (
        <p role="status" className="studio-notice">
          {notice}
        </p>
      )}
      {!data ? (
        <p>{t("Loading…")}</p>
      ) : (
        <>
          {!memoryOnly && (
            <div className="studio-form form-grid">
              <label className="checkbox-label toggle-label">
                <Switch
                  checked={data.settings.search_enabled}
                  onChange={(event) =>
                    edit({ search_enabled: event.target.checked })
                  }
                />
                {t("Enable web research")}
              </label>
              <label>
                {t("Queries per request")}
                <input
                  type="number"
                  min={1}
                  max={5}
                  value={data.settings.max_searches}
                  onChange={(event) =>
                    edit({ max_searches: Number(event.target.value) })
                  }
                />
              </label>
            </div>
          )}
          {!memoryOnly &&
            data.settings.providers.map((provider, index) => (
              <article className="integration-card" key={provider.id}>
                <header>
                  <strong>{provider.name}</strong>
                  <label className="checkbox-label toggle-label">
                    <Switch
                      checked={provider.enabled}
                      onChange={(event) =>
                        edit({
                          providers: data.settings.providers.map((item, i) =>
                            i === index
                              ? { ...item, enabled: event.target.checked }
                              : item,
                          ),
                        })
                      }
                    />
                    {t("Enabled")}
                  </label>
                </header>
                <div className="studio-form">
                  <label>
                    {t("Endpoint")}
                    <input
                      aria-label={`${provider.name} ${t("Endpoint")}`}
                      value={provider.endpoint}
                      onChange={(event) =>
                        edit({
                          providers: data.settings.providers.map((item, i) =>
                            i === index
                              ? { ...item, endpoint: event.target.value }
                              : item,
                          ),
                        })
                      }
                      placeholder={
                        provider.kind === "searxng"
                          ? "http://127.0.0.1:8080/search"
                          : "https://…"
                      }
                    />
                  </label>
                  {!["wikipedia", "searxng"].includes(provider.kind) && (
                    <SecretField
                      label={t("API key")}
                      ariaLabel={`${provider.name} ${t("API key")}`}
                      configured={!!data.key_status[provider.id]}
                      update={updates[provider.id]}
                      disabled={busy}
                      onChange={(update) =>
                        setUpdates((previous) => ({
                          ...previous,
                          [provider.id]: update,
                        }))
                      }
                    />
                  )}
                </div>
                <Button
                  disabled={busy || !provider.endpoint}
                  onClick={() => void test(provider.id)}
                >
                  {t("Test search")}
                </Button>
                <small>{t("Save settings before testing.")}</small>
              </article>
            ))}
          <article className="integration-card">
            <h3>{t("External memory")}</h3>
            <p>{t("Local memory works without an external service.")}</p>
            <div className="studio-form">
              <label>
                {t("Hindsight endpoint")}
                <input
                  value={data.settings.hindsight_url}
                  onChange={(event) =>
                    edit({ hindsight_url: event.target.value })
                  }
                  placeholder="http://127.0.0.1:8888"
                />
              </label>
              <SecretField
                label={t("Hindsight API key")}
                configured={!!data.key_status.hindsight}
                update={updates.hindsight}
                disabled={busy}
                onChange={(update) =>
                  setUpdates((previous) => ({ ...previous, hindsight: update }))
                }
              />
            </div>
          </article>
          <Button primary disabled={busy} onClick={() => void save()}>
            {t(memoryOnly ? "Save memory service" : "Save integrations")}
          </Button>
        </>
      )}
    </section>
  );
}
