import { t } from "../../lib/i18n";
import { useEffect, useState } from "react";
import {
  IconPlus,
  IconTrash,
  IconCheck,
  IconPlayerPlay,
  IconSettings,
  IconSearch,
  IconCopy,
} from "@tabler/icons-react";
import { api, message } from "../../lib/api";
import { Badge, Button, Empty, ErrorBox } from "../../components/ui";
import { ModelAssignments } from "./models";
import { ModelPicker, modelEfforts, modelMetadata } from "./ModelPicker";
import "./settings.css";
import type {
  ModelProfile,
  ModelSettingsResponse,
  ProviderCatalog,
  ProviderConfig,
  ProviderPreset,
  SecretUpdate,
} from "../../domain/types";

const protocolNames: Record<string, string> = {
  openai: "Chat Completions compatible",
  responses: "OpenAI Responses compatible",
  anthropic: "Anthropic Messages",
  gemini: "Gemini Interactions",
  gemini_generate: "Gemini Generate Content",
  ollama: "Ollama native API",
};
const effortNames: Record<string, string> = {
  none: "None",
  minimal: "Minimal",
  low: "Low",
  medium: "Medium",
  high: "High",
  xhigh: "Extra high",
  max: "Maximum",
};

/** Recognize filled workspace endpoint templates while retaining custom gateway URLs. */
function endpointMatches(value: string, template: string) {
  const current = value.replace(/\/+$/, "");
  if (!template.includes("{WorkspaceId}")) return current === template;
  const [prefix, suffix] = template.split("{WorkspaceId}");
  return current.startsWith(prefix) && current.endsWith(suffix);
}

/** Choose hints for the provider, protocol, and known model, with backend enforcement. */
function selectedPreset(
  catalog: ProviderCatalog | null,
  config?: ProviderConfig,
) {
  if (!config?.provider_id) return undefined;
  const matches =
    catalog?.presets.filter(
      (p) =>
        p.config.provider_id === config.provider_id &&
        p.config.protocol === config.protocol,
    ) || [];
  return matches.find((p) => p.models.includes(config.model)) || matches[0];
}

/** Offer protocol-standard custom fields alongside provider-specific preset controls. */
function customOptions(
  protocol: string,
  catalog: ProviderCatalog | null,
): Pick<
  ProviderPreset,
  | "thinking_modes"
  | "reasoning_efforts"
  | "temperature_policy"
  | "temperature_fixed_models"
> {
  return {
    temperature_fixed_models:
      protocol === "anthropic"
        ? catalog?.presets.find((p) => p.id === "anthropic")
            ?.temperature_fixed_models
        : [],
    thinking_modes: protocol === "ollama" ? ["enabled", "disabled"] : [],
    reasoning_efforts:
      protocol === "responses"
        ? ["none", "minimal", "low", "medium", "high", "xhigh", "max"]
        : protocol === "anthropic"
          ? ["low", "medium", "high", "max"]
          : protocol.startsWith("gemini")
            ? ["minimal", "low", "medium", "high"]
            : [],
    temperature_policy:
      protocol === "anthropic" ? "anthropic_version" : "optional",
  };
}

/** Edit independent model profiles and task routing with explicit credential operations. */
export function Settings() {
  const [data, setData] = useState<ModelSettingsResponse | null>(null);
  const [selected, setSelected] = useState("");
  const [tab, setTab] = useState("profiles");
  const [query, setQuery] = useState("");
  const [updates, setUpdates] = useState<Record<string, SecretUpdate>>({});
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [dirty, setDirty] = useState(false);
  const [catalog, setCatalog] = useState<ProviderCatalog | null>(null);
  const [newPreset, setNewPreset] = useState("openai");
  const [loading, setLoading] = useState(true);
  const [loadAttempt, setLoadAttempt] = useState(0);
  useEffect(() => {
    let active = true;
    /** Enable editing once configuration and provider constraints are ready together. */
    async function load() {
      setLoading(true);
      setError("");
      try {
        const [result, providers] = await Promise.all([
          api<ModelSettingsResponse>("get_model_settings"),
          api<ProviderCatalog>("provider_presets"),
        ]);
        if (active) {
          setData(result);
          setCatalog(providers);
          setSelected(result.settings.profiles[0]?.id || "");
        }
      } catch (e) {
        if (active) setError(message(e));
      } finally {
        if (active) setLoading(false);
      }
    }
    void load();
    return () => {
      active = false;
    };
  }, [loadAttempt]);
  const profile = data?.settings.profiles.find(
    (profile) => profile.id === selected,
  );
  const preset = selectedPreset(catalog, profile?.config);
  const baseOptions =
    preset || customOptions(profile?.config.protocol || "openai", catalog);
  const metadata = modelMetadata(catalog, profile?.config);
  const options = {
    ...baseOptions,
    temperature_policy:
      metadata?.temperature_supported === false
        ? "none"
        : baseOptions.temperature_policy,
    reasoning_efforts: modelEfforts(
      profile?.config.protocol || "openai",
      baseOptions.reasoning_efforts,
      metadata?.reasoning_efforts,
      profile?.config.provider_id,
    ),
  };
  const temperatureAllowed =
    !options.temperature_fixed_models?.includes(profile?.config.model || "") &&
    options.temperature_policy !== "none" &&
    (options.temperature_policy !== "thinking_off" ||
      profile?.config.thinking_mode === "disabled") &&
    (options.temperature_policy !== "anthropic_version" ||
      (profile?.config.api_version || "2026-10-21") === "2023-06-01");
  const endpoint = preset?.endpoints.find((item) =>
    endpointMatches(profile?.config.base_url || "", item.url),
  );
  const workspaceTemplate = endpoint?.url.includes("{WorkspaceId}")
    ? endpoint.url
    : undefined;
  const workspaceValue =
    workspaceTemplate && profile
      ? profile.config.base_url
          .replace(/\/+$/, "")
          .slice(
            workspaceTemplate.split("{WorkspaceId}")[0].length,
            -workspaceTemplate.split("{WorkspaceId}")[1].length,
          )
          .replace("{WorkspaceId}", "")
      : "";

  /** Generate preset names without treating the profile being renamed as a duplicate. */
  function uniqueName(stem: string, excludeId = "") {
    const names = new Set(
      data?.settings.profiles
        .filter((p) => p.id !== excludeId)
        .map((p) => p.name.trim().toLocaleLowerCase()),
    );
    let value = stem,
      index = 2;
    while (names.has(value.toLocaleLowerCase())) value = `${stem} ${index++}`;
    return value;
  }
  /** Edit nonsensitive parameters while retaining stable profile IDs. */
  function edit(patch: Partial<ModelProfile>) {
    if (!data || !profile) return;
    setData({
      ...data,
      settings: {
        ...data.settings,
        profiles: data.settings.profiles.map((p) =>
          p.id === selected ? { ...p, ...patch } : p,
        ),
      },
    });
    setDirty(true);
    setNotice("");
  }
  /** Create an independent profile or copy its nonsensitive configuration. */
  function add(copy = false) {
    if (!data) return;
    const id = crypto.randomUUID();
    const template = catalog?.presets.find((p) => p.id === newPreset);
    const next: ModelProfile =
      copy && profile
        ? {
            ...profile,
            id,
            name: uniqueName(profile.name + " Copy"),
            config: { ...profile.config, api_key: "" },
          }
        : {
            id,
            name: uniqueName(t(template?.name || "Custom profile")),
            enabled: true,
            config: template
              ? { ...template.config, api_key: "" }
              : {
                  protocol: "openai",
                  base_url: "",
                  model: "",
                  temperature: 0.7,
                  max_tokens: 4096,
                  timeout_seconds: 120,
                  context_chars: 24000,
                  api_key: "",
                },
          };
    setData({
      ...data,
      settings: {
        ...data.settings,
        profiles: [...data.settings.profiles, next],
      },
    });
    setSelected(id);
    setDirty(true);
    setTab("profiles");
    setNotice("");
  }

  /** Change the actual provider and reset connection-specific credential drafts. */
  function applyPreset(id: string) {
    if (!profile) return;
    const template = catalog?.presets.find((p) => p.id === id);
    edit({
      ...(template ? { name: uniqueName(t(template.name), profile.id) } : {}),
      config: template
        ? { ...template.config, api_key: "" }
        : {
            ...profile.config,
            provider_id: "",
            thinking_mode: "auto",
            reasoning_effort: "",
            model_limits: undefined,
          },
    });
    setUpdates((previous) => {
      const next = { ...previous };
      delete next[profile.id];
      return next;
    });
    setError("");
    setNotice(
      "Preset applied. Review the model, endpoint, and key before saving.",
    );
  }
  /** Check global references before deletion, with project references enforced by the backend. */
  function remove() {
    if (!data || !profile) return;
    if (
      data.settings.default_profile === profile.id ||
      Object.values(data.settings.routes).includes(profile.id)
    ) {
      setError(
        "This profile is assigned to a global task. Update task routing first.",
      );
      return;
    }
    if (!window.confirm(t('Delete model profile "{0}"?', profile.name))) return;
    const profiles = data.settings.profiles.filter((p) => p.id !== profile.id);
    setData({ ...data, settings: { ...data.settings, profiles } });
    setUpdates((previous) => {
      const next = { ...previous };
      delete next[profile.id];
      return next;
    });
    setSelected(profiles[0]?.id || "");
    setDirty(true);
  }
  /** Record explicit preserve, clear, or replace operations separately from profile data. */
  function updateSecret(update: SecretUpdate) {
    setUpdates({ ...updates, [selected]: update });
    setDirty(true);
    setNotice("");
  }
  /** Save profiles and routing and receive credential-presence flags. */
  async function save() {
    if (!data || !catalog) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      setData(
        await api<ModelSettingsResponse>("save_model_settings", {
          settings: data.settings,
          key_updates: updates,
        }),
      );
      setUpdates({});
      setDirty(false);
      setNotice("Model profiles and task routing saved.");
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }
  /** Test a saved profile's HTTP connection and streamed text after an explicit author action. */
  async function test() {
    if (!profile) return;
    setTesting(true);
    setError("");
    setNotice("");
    try {
      const result = await api<{ message: string; elapsed_ms: number }>(
        "test_model",
        { profile_id: profile.id },
      );
      setNotice(`${profile.name}: ${result.message} (${result.elapsed_ms} ms)`);
    } catch (e) {
      setError(message(e));
    } finally {
      setTesting(false);
    }
  }
  const keyUpdate = updates[selected];
  return (
    <fieldset
      className="workspace-page model-settings-page"
      disabled={busy || loading}
      aria-busy={busy || loading}
      aria-label="Model profile editor"
    >
      <div className="section-title">
        <div>
          <p className="eyebrow">{t("MODELS FOR EVERY PART OF YOUR STORY")}</p>
          <h1>{t("Model profiles and task routing")}</h1>
          <p>
            {t(
              "Choose models for planning, writing, review, and knowledge compilation.",
            )}
          </p>
        </div>
        <Button
          primary
          disabled={!data || !catalog || busy || !dirty}
          onClick={save}
        >
          {busy ? t("Saving") : t("Save all settings")}
        </Button>
      </div>
      {loading && (
        <div className="info-strip" role="status">
          {t("Loading model profiles and provider rules…")}
        </div>
      )}
      {error && <ErrorBox>{error}</ErrorBox>}
      {!loading && !data && error && (
        <Button onClick={() => setLoadAttempt((attempt) => attempt + 1)}>
          {t("Reload model profiles")}
        </Button>
      )}
      {notice && (
        <div className="info-strip">
          <IconCheck size={17} />
          {t(notice)}
        </div>
      )}
      <div className="tabs">
        <button
          className={tab === "profiles" ? "active" : ""}
          onClick={() => setTab("profiles")}
        >
          {t("Model profiles")}{" "}
          <span>{data?.settings.profiles.length || 0}</span>
        </button>
        <button
          className={tab === "routes" ? "active" : ""}
          onClick={() => setTab("routes")}
        >
          {t("Task routing")}
        </button>
      </div>
      {data && catalog && tab === "profiles" && (
        <div className="model-workspace">
          <aside className="model-list">
            <div className="provider-preset-controls">
              <label className="provider-preset-picker">
                {t("New profile template")}
                <select
                  aria-label={t("New profile template")}
                  value={newPreset}
                  onChange={(e) => setNewPreset(e.target.value)}
                >
                  {!catalog && (
                    <option value="openai">{t("Loading presets…")}</option>
                  )}
                  {catalog?.presets.map((p) => (
                    <option key={p.id} value={p.id}>
                      {t(p.name)}
                    </option>
                  ))}
                  <option value="">{t("Custom compatible profile")}</option>
                </select>
              </label>
              <Button
                primary
                disabled={!catalog && !!newPreset}
                onClick={() => add()}
              >
                <IconPlus size={17} />
                {t("Add profile")}
              </Button>
              <p className="provider-catalog-note">
                {t(
                  "This template is used only when adding a profile. Edit the active connection on the right.",
                )}
              </p>
            </div>
            <label className="search">
              <IconSearch size={16} />
              <input
                aria-label={t("Search model profiles")}
                placeholder={t("Search profiles…")}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </label>
            <div className="model-profile-list">
              {data.settings.profiles
                .filter((p) =>
                  `${p.name} ${p.config.model} ${p.config.provider_id || ""}`
                    .toLowerCase()
                    .includes(query.toLowerCase()),
                )
                .map((p) => (
                  <button
                    key={p.id}
                    className={`model-list-item ${selected === p.id ? "active" : ""}`}
                    onClick={() => {
                      setSelected(p.id);
                      setError("");
                      setNotice("");
                    }}
                  >
                    <span>
                      <strong>{p.name}</strong>
                      <small>{p.config.model || t("Model ID required")}</small>
                    </span>
                    <Badge tone={p.enabled ? "blue" : "gray"}>
                      {p.enabled ? t("Enabled") : t("Disabled")}
                    </Badge>
                  </button>
                ))}
            </div>
          </aside>
          <div className="model-editor">
            {profile ? (
              <>
                <div className="model-editor-heading">
                  <div>
                    <h2>{profile.name}</h2>
                    <p>
                      {t(
                        "Each profile keeps its endpoint, model, and parameters.",
                      )}
                    </p>
                  </div>
                  <div>
                    <Button
                      onClick={() => {
                        if (data) {
                          setData({
                            ...data,
                            settings: {
                              ...data.settings,
                              default_profile: selected,
                            },
                          });
                          setDirty(true);
                        }
                      }}
                    >
                      {t("Set as default")}
                    </Button>
                    <Button onClick={() => add(true)}>
                      <IconCopy size={15} />
                      {t("Copy")}
                    </Button>
                    <Button onClick={remove}>
                      <IconTrash size={15} />
                      {t("Delete")}
                    </Button>
                  </div>
                </div>
                <div className="provider-summary">
                  <strong>{t(preset?.name || "Custom API")}</strong>
                  <p>
                    {t(
                      preset?.description ||
                        "Set the API protocol, endpoint, and model.",
                    )}
                  </p>
                  {preset && (
                    <a href={preset.docs_url} target="_blank" rel="noreferrer">
                      {t("Official documentation ↗")}
                    </a>
                  )}
                  {catalog && (
                    <small>
                      {t("Preset checked on")}
                      {catalog.checked_at}
                    </small>
                  )}
                </div>
                <div className="form-grid">
                  <label>
                    {t("Profile name")}
                    <input
                      value={profile.name}
                      onChange={(e) => edit({ name: e.target.value })}
                      maxLength={80}
                    />
                  </label>
                  <label>
                    {t("Service provider")}
                    <select
                      aria-label={t("Service provider")}
                      value={preset?.id || ""}
                      onChange={(event) => applyPreset(event.target.value)}
                    >
                      <option value="">{t("Custom compatible profile")}</option>
                      {catalog.presets.map((item) => (
                        <option key={item.id} value={item.id}>
                          {t(item.name)}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label>
                    {t("API protocol")}
                    <select
                      aria-label={t("API protocol")}
                      value={profile.config.protocol}
                      onChange={(e) =>
                        edit({
                          config: {
                            ...profile.config,
                            protocol: e.target.value,
                            provider_id: "",
                            thinking_mode: "auto",
                            reasoning_effort: "",
                            api_version: "",
                            temperature: null,
                            model_limits: undefined,
                          },
                        })
                      }
                    >
                      {Object.entries(protocolNames).map(([id, name]) => (
                        <option key={id} value={id}>
                          {t(name)}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="checkbox-label">
                    <input
                      type="checkbox"
                      checked={profile.enabled}
                      onChange={(e) => edit({ enabled: e.target.checked })}
                    />
                    {t("Enable this profile")}
                  </label>
                </div>
                {!!preset?.endpoints.length && preset.endpoints.length > 1 && (
                  <div className="form-grid provider-endpoints">
                    <label>
                      {t("Region / Endpoint")}
                      <select
                        aria-label={t("Service region")}
                        value={endpoint?.url || ""}
                        onChange={(e) => {
                          if (e.target.value)
                            edit({
                              config: {
                                ...profile.config,
                                base_url: e.target.value,
                              },
                            });
                        }}
                      >
                        <option value="">{t("Custom endpoint")}</option>
                        {preset.endpoints.map((item) => (
                          <option key={item.url} value={item.url}>
                            {t(item.name)}
                          </option>
                        ))}
                      </select>
                    </label>
                    {workspaceTemplate && (
                      <label>
                        {t("Bailian workspace ID")}
                        <input
                          aria-label={t("Bailian workspace ID")}
                          value={workspaceValue}
                          placeholder={t("Workspace ID from the console")}
                          onChange={(e) =>
                            edit({
                              config: {
                                ...profile.config,
                                base_url: workspaceTemplate.replace(
                                  "{WorkspaceId}",
                                  e.target.value.trim() || "{WorkspaceId}",
                                ),
                              },
                            })
                          }
                        />
                      </label>
                    )}
                  </div>
                )}
                <label>
                  {t("API Base URL")}
                  <input
                    type="url"
                    aria-label={t("API Base URL")}
                    value={profile.config.base_url}
                    onChange={(e) =>
                      edit({
                        config: { ...profile.config, base_url: e.target.value },
                      })
                    }
                    placeholder={
                      profile.config.protocol === "ollama"
                        ? "http://localhost:11434"
                        : "https://…/v1"
                    }
                  />
                  <small>
                    {t(
                      "Include the provider's version path. Changing the endpoint or protocol requires a key check.",
                    )}
                  </small>
                </label>
                {profile.config.protocol === "anthropic" && (
                  <label>
                    {t("Anthropic API version")}
                    <select
                      aria-label={t("Anthropic API version")}
                      value={profile.config.api_version || "2026-10-21"}
                      onChange={(e) =>
                        edit({
                          config: {
                            ...profile.config,
                            api_version: e.target.value,
                            temperature: null,
                          },
                        })
                      }
                    >
                      <option value="2026-10-21">
                        {t("2026-10-21 · Current API")}
                      </option>
                      <option value="2023-06-01">
                        {t("2023-06-01 · Legacy compatibility")}
                      </option>
                    </select>
                    <small>
                      {t(
                        "The current API uses model-default temperature. Legacy requests follow the selected model's limits.",
                      )}
                    </small>
                  </label>
                )}
                <div className="model-key-heading">
                  <span>{t("API Key")}</span>
                  <Badge tone={data.key_status[profile.id] ? "green" : "gray"}>
                    {data.key_status[profile.id]
                      ? t("Configured for this session")
                      : t("Not configured")}
                  </Badge>
                </div>
                <label>
                  <span className="sr-only">{t("API key operation")}</span>
                  <select
                    aria-label={t("API key operation")}
                    value={keyUpdate?.action || "preserve"}
                    onChange={(e) =>
                      updateSecret(
                        e.target.value === "replace"
                          ? { action: "replace", value: "" }
                          : e.target.value === "clear"
                            ? { action: "clear" }
                            : { action: "preserve" },
                      )
                    }
                  >
                    <option value="preserve">{t("Keep current key")}</option>
                    <option value="replace">{t("Enter / Replace key")}</option>
                    <option value="clear">{t("Clear current key")}</option>
                  </select>
                </label>
                {keyUpdate?.action === "replace" && (
                  <label>
                    {t("New API key")}
                    <input
                      type="password"
                      autoComplete="off"
                      value={keyUpdate.value}
                      onChange={(e) =>
                        updateSecret({
                          action: "replace",
                          value: e.target.value,
                        })
                      }
                    />
                  </label>
                )}
                <p className="field-hint">
                  {t(
                    "Keys are held in this app process's memory. Local models can use an empty key.",
                  )}
                </p>
                <ModelPicker
                  key={profile.id}
                  profileId={profile.id}
                  config={profile.config}
                  catalog={catalog}
                  examples={preset?.models || []}
                  keyUpdate={updates[profile.id]}
                  onLimits={(model_limits) =>
                    edit({ config: { ...profile.config, model_limits } })
                  }
                  onSelect={(model, model_limits) => {
                    const nextMetadata = modelMetadata(catalog, {
                      ...profile.config,
                      model,
                    });
                    edit({
                      config: {
                        ...profile.config,
                        model,
                        model_limits,
                        thinking_mode: "auto",
                        reasoning_effort: "",
                        temperature:
                          nextMetadata?.temperature_supported === false ||
                          options.temperature_policy === "thinking_off" ||
                          options.temperature_fixed_models?.includes(model)
                            ? null
                            : profile.config.temperature,
                      },
                    });
                  }}
                />
                <div className="settings-divider" />
                <h3 className="model-section-heading">
                  {t("Request parameters")}
                </h3>
                {(options.thinking_modes.length > 0 ||
                  options.reasoning_efforts.length > 0) && (
                  <div className="form-grid provider-thinking">
                    {options.thinking_modes.length > 0 && (
                      <label>
                        {t("Thinking mode")}
                        <select
                          aria-label={t("Thinking mode")}
                          value={profile.config.thinking_mode || "auto"}
                          onChange={(e) =>
                            edit({
                              config: {
                                ...profile.config,
                                thinking_mode: e.target.value,
                                reasoning_effort:
                                  e.target.value === "disabled"
                                    ? ""
                                    : profile.config.reasoning_effort,
                                temperature:
                                  options.temperature_policy ===
                                    "thinking_off" &&
                                  e.target.value !== "disabled"
                                    ? null
                                    : profile.config.temperature,
                              },
                            })
                          }
                        >
                          <option value="auto">{t("Model default")}</option>
                          {options.thinking_modes.map((mode) => (
                            <option key={mode} value={mode}>
                              {mode === "enabled"
                                ? t("Thinking on")
                                : t("Thinking off")}
                            </option>
                          ))}
                        </select>
                      </label>
                    )}
                    {options.reasoning_efforts.length > 0 && (
                      <label>
                        {t("Reasoning effort")}
                        <select
                          aria-label={t("Reasoning effort")}
                          disabled={profile.config.thinking_mode === "disabled"}
                          value={
                            profile.config.reasoning_effort === "auto"
                              ? ""
                              : profile.config.reasoning_effort || ""
                          }
                          onChange={(e) =>
                            edit({
                              config: {
                                ...profile.config,
                                reasoning_effort: e.target.value,
                              },
                            })
                          }
                        >
                          <option value="">{t("Model default")}</option>
                          {options.reasoning_efforts.map((level) => (
                            <option key={level} value={level}>
                              {t(effortNames[level] || level)}
                            </option>
                          ))}
                        </select>
                      </label>
                    )}
                  </div>
                )}
                <p className="field-hint">
                  {t(
                    "Thinking uses the output budget and adds latency. Final answer text is used as prose. Available parameters depend on the selected model.",
                  )}
                </p>
                <div className="form-grid">
                  <label>
                    {t("Temperature (optional)")}
                    <input
                      type="number"
                      min={0}
                      max={
                        options.temperature_policy === "range01" ||
                        profile.config.protocol === "anthropic"
                          ? 1
                          : 2
                      }
                      step={0.1}
                      disabled={!temperatureAllowed}
                      value={
                        temperatureAllowed
                          ? (profile.config.temperature ?? "")
                          : ""
                      }
                      onChange={(e) =>
                        edit({
                          config: {
                            ...profile.config,
                            temperature:
                              e.target.value === ""
                                ? null
                                : Number(e.target.value),
                          },
                        })
                      }
                    />
                    {!temperatureAllowed && (
                      <small>
                        {t(
                          "This API or thinking mode uses model-default temperature.",
                        )}
                      </small>
                    )}
                  </label>
                  <label>
                    {t("Maximum output tokens")}
                    <input
                      type="number"
                      min={1}
                      max={Math.min(
                        profile.config.model_limits?.max_output_tokens ||
                          metadata?.limits.max_output_tokens ||
                          catalog.resource_limits.max_output_tokens,
                        catalog.resource_limits.max_output_tokens,
                      )}
                      value={profile.config.max_tokens}
                      onChange={(e) =>
                        edit({
                          config: {
                            ...profile.config,
                            max_tokens: Number(e.target.value),
                          },
                        })
                      }
                    />
                  </label>
                  <label>
                    {t("Local context budget (characters)")}
                    <input
                      type="number"
                      min={1}
                      max={catalog.resource_limits.max_context_chars}
                      value={profile.config.context_chars}
                      onChange={(e) =>
                        edit({
                          config: {
                            ...profile.config,
                            context_chars: Number(e.target.value),
                          },
                        })
                      }
                    />
                    <small>
                      {t(
                        "Used to assemble story text locally. Characters are not tokens; the service enforces its actual token window.",
                      )}
                    </small>
                  </label>
                  <label>
                    {t("Timeout (seconds)")}
                    <input
                      type="number"
                      min={10}
                      max={3600}
                      value={profile.config.timeout_seconds}
                      onChange={(e) =>
                        edit({
                          config: {
                            ...profile.config,
                            timeout_seconds: Number(e.target.value),
                          },
                        })
                      }
                    />
                  </label>
                </div>
                <div className="model-test-row">
                  <Button
                    disabled={dirty || testing || !profile.config.model.trim()}
                    onClick={test}
                  >
                    <IconPlayerPlay size={16} />
                    {testing
                      ? t("Testing…")
                      : t("Test connection and streaming")}
                  </Button>
                  <span>
                    {dirty
                      ? t("Save your changes before testing.")
                      : t(
                          "Testing sends a short request to this profile and may incur a charge.",
                        )}
                  </span>
                </div>
              </>
            ) : (
              <Empty
                title={t("Select or add a model profile")}
                detail={t(
                  "Save multiple models or parameter sets for the same service.",
                )}
              />
            )}
          </div>
        </div>
      )}
      {data && catalog && tab === "routes" && (
        <section className="model-routing-card">
          <div className="settings-card-heading">
            <span className="soft-icon">
              <IconSettings size={22} />
            </span>
            <div>
              <h2>{t("Global default and task routing")}</h2>
              <p>
                {t(
                  "Unassigned tasks use the default profile. Each project can override task routing in Story planning.",
                )}
              </p>
            </div>
          </div>
          <label>
            {t("Default model profile")}
            <select
              value={data.settings.default_profile}
              onChange={(e) => {
                setData({
                  ...data,
                  settings: {
                    ...data.settings,
                    default_profile: e.target.value,
                  },
                });
                setDirty(true);
              }}
            >
              <option value="">{t("Unassigned (offline writing)")}</option>
              {data.settings.default_profile &&
                !data.settings.profiles.some(
                  (p) => p.id === data.settings.default_profile && p.enabled,
                ) && (
                  <option value={data.settings.default_profile}>
                    {t("Current default profile unavailable")}
                  </option>
                )}
              {data.settings.profiles
                .filter((p) => p.enabled)
                .map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name} · {p.config.model || t("Model ID required")}
                  </option>
                ))}
            </select>
          </label>
          <div className="settings-divider" />
          <ModelAssignments
            settings={data.settings}
            routes={data.settings.routes}
            inheritLabel={t("Use default model")}
            onChange={(routes) => {
              setData({ ...data, settings: { ...data.settings, routes } });
              setDirty(true);
            }}
          />
          <p className="field-hint">
            {t(
              "Wiki extraction and memory compilation can use different models. Assign a usable profile to each task.",
            )}
          </p>
        </section>
      )}
    </fieldset>
  );
}
