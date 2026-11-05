import { useEffect, useRef, useState } from "react";
import { IconRefresh } from "@tabler/icons-react";
import { Button, ErrorBox } from "../../components/ui";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import type {
  DiscoveredModel,
  ModelLimits,
  ProviderCatalog,
  ProviderConfig,
  SecretUpdate,
} from "../../domain/types";

/** Look up metadata without ever replacing the exact model ID used in requests. */
export function modelMetadata(
  catalog: ProviderCatalog | null,
  config?: ProviderConfig,
) {
  if (!config) return undefined;
  const alias =
    catalog?.model_aliases[`${config.provider_id || ""}/${config.model}`];
  const modelId =
    config.provider_id === "openrouter" && config.model.includes("/")
      ? config.model.slice(config.model.indexOf("/") + 1)
      : config.model;
  const key = alias || modelId.trim().toLowerCase().replace(/[._]/g, "-");
  return catalog?.model_metadata[key];
}

/** Keep capability controls within the reasoning dialects implemented by the backend. */
export function modelEfforts(
  protocol: string,
  base: string[],
  extra: string[] = [],
  provider = "",
) {
  if (provider && provider !== "openai") return base;
  const allowed =
    protocol === "openai" || protocol === "responses"
      ? ["none", "minimal", "low", "medium", "high", "xhigh", "max"]
      : protocol === "anthropic"
        ? ["low", "medium", "high", "max"]
        : protocol.startsWith("gemini")
          ? ["minimal", "low", "medium", "high"]
          : [];
  return [
    ...new Set([...base, ...extra.filter((value) => allowed.includes(value))]),
  ];
}

interface Props {
  profileId: string;
  config: ProviderConfig;
  catalog: ProviderCatalog;
  examples: string[];
  keyUpdate?: SecretUpdate;
  onSelect: (id: string, limits?: ModelLimits) => void;
  onLimits: (limits?: ModelLimits) => void;
}

/** Discover account-specific models from the current connection draft and ignore stale responses. */
export function ModelPicker({
  profileId,
  config,
  catalog,
  examples,
  keyUpdate,
  onSelect,
  onLimits,
}: Props) {
  const [remote, setRemote] = useState<DiscoveredModel[] | null>(null);
  const [fetching, setFetching] = useState(false);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");
  const sequence = useRef(0);
  const identity = JSON.stringify([
    profileId,
    config.protocol,
    config.provider_id,
    config.base_url,
    config.api_version,
    keyUpdate,
  ]);
  const currentIdentity = useRef(identity);
  currentIdentity.current = identity;
  useEffect(() => {
    setRemote(null);
    setFetching(false);
    setError("");
    setFilter("");
    return () => {
      sequence.current++;
    };
  }, [identity]);

  /** Fetch without saving the draft or generating billable model output. */
  async function discover() {
    const request = ++sequence.current;
    const connection = identity;
    setFetching(true);
    setError("");
    try {
      const result = await api<{ models: DiscoveredModel[] }>(
        "list_provider_models",
        {
          profile_id: profileId,
          config: { ...config, api_key: "" },
          key_update: keyUpdate,
        },
      );
      if (
        sequence.current === request &&
        currentIdentity.current === connection
      )
        setRemote(result.models);
    } catch (cause) {
      if (
        sequence.current === request &&
        currentIdentity.current === connection
      )
        setError(message(cause));
    } finally {
      if (
        sequence.current === request &&
        currentIdentity.current === connection
      )
        setFetching(false);
    }
  }

  const choices: DiscoveredModel[] =
    remote ??
    examples.map((id) => ({
      id,
      name: id,
      limits: modelMetadata(catalog, { ...config, model: id })?.limits || {},
    }));
  const filtered = choices.filter((model) =>
    `${model.id} ${model.name}`.toLowerCase().includes(filter.toLowerCase()),
  );
  const known = modelMetadata(catalog, config);
  const limits = { ...known?.limits, ...config.model_limits };

  /** Apply a discovered model while retaining its original identifier and reported limits. */
  function select(id: string) {
    const model = choices.find((item) => item.id === id);
    onSelect(id, remote && model ? model.limits : undefined);
  }

  /** Remove an empty override so the bundled catalog remains the fallback. */
  function changeLimit(field: keyof ModelLimits, value: string) {
    const next = { ...config.model_limits };
    if (value === "") delete next[field];
    else next[field] = Number(value);
    onLimits(Object.keys(next).length ? next : undefined);
  }

  return (
    <section className="model-picker" aria-label={t("Model selection")}>
      <div className="model-discovery-heading">
        <div>
          <h3>{t("Model selection")}</h3>
          <p>
            {t("Fetch models for this connection, or enter an exact model ID.")}
          </p>
        </div>
        <Button
          disabled={fetching || !config.base_url.trim()}
          onClick={discover}
        >
          <IconRefresh size={16} />
          {fetching ? t("Fetching models…") : t("Fetch models")}
        </Button>
      </div>
      {error && <ErrorBox>{t(error)}</ErrorBox>}
      <div className="form-grid">
        <label>
          {t("Model ID")}
          <input
            aria-label={t("Model ID")}
            list="provider-model-examples"
            value={config.model}
            placeholder={t("Model ID from the provider")}
            onChange={(event) => select(event.target.value)}
          />
          <datalist id="provider-model-examples">
            {choices.slice(0, 500).map((model) => (
              <option key={model.id} value={model.id}>
                {model.name}
              </option>
            ))}
          </datalist>
        </label>
        <label>
          {t("Search available models")}
          <input
            aria-label={t("Search available models")}
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder={t("Filter by model name or ID")}
          />
        </label>
        <label className="model-picker-list">
          {remote === null
            ? t("Preset examples")
            : t("Models returned by this connection")}
          <select
            aria-label={t("Available models")}
            value=""
            onChange={(event) => {
              if (event.target.value) select(event.target.value);
            }}
          >
            <option value="">
              {t("Choose a model ({0} available)", filtered.length)}
            </option>
            {filtered.slice(0, 200).map((model) => (
              <option key={model.id} value={model.id}>
                {model.name === model.id
                  ? model.id
                  : `${model.name} · ${model.id}`}
              </option>
            ))}
          </select>
          <small>
            {remote?.length === 0
              ? t(
                  "No text models were returned. You can still enter a model ID manually.",
                )
              : t(
                  "Fetching a list does not verify generation access. Connection testing is separate.",
                )}
          </small>
        </label>
      </div>
      <div className="model-capacities">
        <span>
          {t("Model context window (tokens)")}
          <strong>
            {limits.context_window_tokens?.toLocaleString() || t("Unknown")}
          </strong>
        </span>
        <span>
          {t("Model input limit (tokens)")}
          <strong>
            {limits.max_input_tokens?.toLocaleString() || t("Unknown")}
          </strong>
        </span>
        <span>
          {t("Model output limit (tokens)")}
          <strong>
            {limits.max_output_tokens?.toLocaleString() || t("Unknown")}
          </strong>
        </span>
      </div>
      <p className="field-hint">
        {t(
          "Model limits are reported or configured token capacities. They are separate from the local character budget.",
        )}
      </p>
      <details className="model-limit-overrides">
        <summary>{t("Deployment limits (optional)")}</summary>
        <p className="field-hint">
          {t(
            "Override catalog limits for a custom deployment. Leave blank to use known model metadata.",
          )}
        </p>
        <div className="form-grid">
          <label>
            {t("Context window override (tokens)")}
            <input
              type="number"
              min={1}
              max={32000000}
              aria-label={t("Context window override (tokens)")}
              value={config.model_limits?.context_window_tokens ?? ""}
              onChange={(event) =>
                changeLimit("context_window_tokens", event.target.value)
              }
            />
          </label>
          <label>
            {t("Output limit override (tokens)")}
            <input
              type="number"
              min={1}
              max={catalog.resource_limits.max_output_tokens}
              aria-label={t("Output limit override (tokens)")}
              value={config.model_limits?.max_output_tokens ?? ""}
              onChange={(event) =>
                changeLimit("max_output_tokens", event.target.value)
              }
            />
          </label>
        </div>
      </details>
    </section>
  );
}
