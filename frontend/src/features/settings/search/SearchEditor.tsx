import { useState } from "react";
import { Select } from "../../../components/Select";
import {
  Button,
  ErrorBox,
  Modal,
  RequiredLabel,
  SecretField,
  Switch,
} from "../../../components/ui";
import { message } from "../../../lib/api";
import { t } from "../../../lib/i18n";
import type {
  SearchCatalog,
  SearchFamily,
  SearchFilters,
  SearchLimits,
  SearchProfile,
  SearchSettingsView,
} from "../../../domain/search";
import type { SecretUpdate } from "../../../domain/types";
import { knownSearchLimit, matchingSearchBase } from "./state";

export const searchModeNames: Record<string, string> = {
  search: "Standalone search",
  basic: "Basic · result list",
  pro: "Pro · source passages",
  native: "Native model browsing",
  server_tool: "Search server tool",
};

/** Edit one real connection with provider-specific fields and a separate in-memory credential draft. */
export function SearchEditor({
  initial,
  view,
  catalog,
  onSave,
  onClose,
}: {
  initial: SearchProfile;
  view: SearchSettingsView;
  catalog: SearchCatalog;
  onSave: (profile: SearchProfile, secret?: SecretUpdate) => Promise<void>;
  onClose: () => void;
}) {
  const [draft, setDraft] = useState<SearchProfile>(() =>
    structuredClone(initial),
  );
  const [secret, setSecret] = useState<SecretUpdate>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const capability = catalog.services.find(
    (service) => service.family === draft.family,
  )!;
  const linkedModel =
    draft.mode === "native" ||
    draft.family === "openrouter" ||
    draft.credential_source === "model_ref";
  const compatible = view.models.filter(
    (model) =>
      model.enabled &&
      (model.family === draft.family ||
        (!model.family && matchingSearchBase(model.base_url, draft.endpoint))),
  );
  const retained =
    !!view.key_status[initial.id] &&
    initial.family === draft.family &&
    initial.region === draft.region &&
    initial.endpoint === draft.endpoint &&
    initial.credential_source === draft.credential_source &&
    initial.model_profile_id === draft.model_profile_id;
  const native =
    draft.mode === "native" ||
    (draft.family === "openrouter" &&
      ["native", "auto"].includes(draft.engine || ""));
  const supportsDomains =
    ["deepseek", "tavily", "exa"].includes(draft.family) ||
    draft.family === "openrouter" ||
    (draft.family === "kimi" && draft.mode === "pro");
  const supportsDates =
    ["deepseek", "tavily", "exa"].includes(draft.family) ||
    (draft.family === "kimi" && draft.mode === "pro");
  const sourceCanReuse = ["deepseek", "kimi", "openrouter"].includes(
    draft.family,
  );

  /** Changing credential destinations creates an explicit clear intent instead of reusing an opaque mask. */
  function edit(patch: Partial<SearchProfile>) {
    if (
      (patch.endpoint !== undefined && patch.endpoint !== draft.endpoint) ||
      (patch.region !== undefined && patch.region !== draft.region) ||
      (patch.credential_source !== undefined &&
        patch.credential_source !== draft.credential_source) ||
      (patch.model_profile_id !== undefined &&
        patch.model_profile_id !== draft.model_profile_id)
    )
      setSecret({ action: "clear" });
    setDraft((current) => ({ ...current, ...patch }));
    setError("");
  }
  /** Preserve typed limit values for backend validation rather than silently clamping user input. */
  function limits(patch: Partial<SearchLimits>) {
    setDraft((current) => ({
      ...current,
      limits: { ...current.limits, ...patch },
    }));
  }
  /** Retain only filters exposed by the selected provider mode. */
  function filters(patch: Partial<SearchFilters>) {
    setDraft((current) => ({
      ...current,
      filters: { ...current.filters, ...patch },
    }));
  }
  /** Use a documented preset without carrying a key or a model reference across services. */
  function family(value: SearchFamily) {
    const preset = structuredClone(
      catalog.presets.find((profile) => profile.family === value)!,
    );
    preset.id = draft.id;
    preset.name = draft.name;
    preset.enabled = draft.enabled;
    setDraft(preset);
    setSecret({ action: "clear" });
    setError("");
  }
  /** Keep failed saves in this dialog, including the password draft and conflict details. */
  async function save(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      await onSave({ ...draft, name: draft.name.trim() }, secret);
      setSecret(undefined);
      onClose();
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }
  /** Parse explicit hostname lists without rewriting the author's search query. */
  function domains(value: string): string[] {
    return value.split(/[\s,，]+/).filter(Boolean);
  }

  return (
    <div className="search-editor-scope">
      <Modal
        title={t("Search profile")}
        onClose={() => {
          if (!busy) onClose();
        }}
        wide
      >
        <form className="search-editor-form" onSubmit={save}>
          <fieldset
            className="search-editor-fields studio-form"
            disabled={busy}
          >
            <p className="muted">
              {t(
                "Search services and writing models can be chosen independently. Testing may incur search and model charges.",
              )}
            </p>
            {error && <ErrorBox>{error}</ErrorBox>}
            <div className="form-grid">
              <label>
                <RequiredLabel>{t("Profile name")}</RequiredLabel>
                <input
                  aria-label={t("Search profile name")}
                  required
                  maxLength={80}
                  value={draft.name}
                  onChange={(event) => edit({ name: event.target.value })}
                />
              </label>
              <label>
                {t("Search service")}
                <Select
                  aria-label={t("Search service")}
                  disabled={!!initial.legacy_id}
                  value={draft.family}
                  onChange={(event) =>
                    family(event.target.value as SearchFamily)
                  }
                >
                  {catalog.services.map((service) => (
                    <option key={service.family} value={service.family}>
                      {t(service.name)}
                    </option>
                  ))}
                </Select>
              </label>
              <label>
                {t("Search mode")}
                <Select
                  aria-label={t("Search mode")}
                  value={draft.mode}
                  onChange={(event) =>
                    edit({
                      mode: event.target.value,
                      filters: { include_content: false },
                      search_mode: "",
                      ...(event.target.value !== "native" &&
                      draft.credential_source === "own" &&
                      draft.family !== "openrouter"
                        ? { model_profile_id: "" }
                        : {}),
                    })
                  }
                >
                  {capability.modes.map((mode) => (
                    <option key={mode} value={mode}>
                      {t(searchModeNames[mode] || mode)}
                    </option>
                  ))}
                </Select>
              </label>
              {draft.family === "kimi" && (
                <label>
                  {t("Account region")}
                  <Select
                    aria-label={t("Account region")}
                    value={draft.region}
                    onChange={(event) =>
                      edit({
                        region: event.target.value,
                        endpoint:
                          event.target.value === "cn"
                            ? "https://api.moonshot.cn/v1"
                            : "https://api.moonshot.ai/v1",
                      })
                    }
                  >
                    <option value="cn">{t("Mainland API platform")}</option>
                    <option value="global">
                      {t("International API platform")}
                    </option>
                  </Select>
                </label>
              )}
            </div>
            <label>
              <RequiredLabel>{t("Service endpoint")}</RequiredLabel>
              <input
                aria-label={t("Service endpoint")}
                type="url"
                required
                value={draft.endpoint}
                onChange={(event) => edit({ endpoint: event.target.value })}
              />
            </label>
            {draft.family === "kimi" && (
              <small>
                {t(
                  "Kimi API regions and Kimi Code use separate credentials. Choose the endpoint matching your API account.",
                )}
              </small>
            )}
            {capability.requires_key && sourceCanReuse && (
              <label>
                {t("Credential source")}
                <Select
                  aria-label={t("Credential source")}
                  value={draft.credential_source}
                  onChange={(event) =>
                    edit({
                      credential_source: event.target.value as
                        | "own"
                        | "model_ref",
                      ...(event.target.value === "own" &&
                      draft.mode !== "native" &&
                      draft.family !== "openrouter"
                        ? { model_profile_id: "" }
                        : {}),
                    })
                  }
                >
                  <option value="own">{t("Enter a separate key")}</option>
                  <option value="model_ref">
                    {t("Reuse a matching model connection")}
                  </option>
                </Select>
              </label>
            )}
            {linkedModel && (
              <label>
                <RequiredLabel>{t("Research model connection")}</RequiredLabel>
                <Select
                  aria-label={t("Research model connection")}
                  value={draft.model_profile_id || ""}
                  onChange={(event) => {
                    const model = view.models.find(
                      (model) => model.id === event.target.value,
                    );
                    edit({
                      model_profile_id: event.target.value,
                      ...(model && draft.credential_source === "model_ref"
                        ? { endpoint: model.base_url }
                        : {}),
                    });
                  }}
                >
                  <option value="">
                    {t("Select a compatible model connection")}
                  </option>
                  {compatible.map((model) => (
                    <option key={model.id} value={model.id}>
                      {model.name} · {model.model}
                    </option>
                  ))}
                </Select>
                {!compatible.length && (
                  <small>
                    {t(
                      "Save a model connection for this service in Model profiles first. Custom gateways must match the search endpoint.",
                    )}
                  </small>
                )}
              </label>
            )}
            {capability.requires_key && draft.credential_source === "own" && (
              <SecretField
                label={t("API key")}
                configured={retained}
                update={secret}
                onChange={setSecret}
                disabled={busy}
              />
            )}
            {draft.family === "openrouter" && (
              <>
                <label>
                  {t("Search engine")}
                  <Select
                    aria-label={t("Search engine")}
                    value={draft.engine || "exa"}
                    onChange={(event) =>
                      edit({
                        engine: event.target.value,
                        search_mode: "",
                        filters: { include_content: false },
                      })
                    }
                  >
                    {[
                      "exa",
                      "parallel",
                      "perplexity",
                      "firecrawl",
                      "auto",
                      "native",
                    ].map((engine) => (
                      <option value={engine} key={engine}>
                        {t(
                          engine === "auto"
                            ? "Automatic engine"
                            : engine === "native"
                              ? "Upstream native search"
                              : engine,
                        )}
                      </option>
                    ))}
                  </Select>
                </label>
                {["exa", "parallel"].includes(draft.engine || "") && (
                  <label>
                    {t("Search depth")}
                    <Select
                      aria-label={t("Search depth")}
                      value={draft.search_mode || ""}
                      onChange={(event) =>
                        edit({ search_mode: event.target.value })
                      }
                    >
                      <option value="">{t("Engine default")}</option>
                      {(draft.engine === "exa"
                        ? [
                            "instant",
                            "fast",
                            "auto",
                            "deep-lite",
                            "deep",
                            "deep-reasoning",
                          ]
                        : ["turbo", "fast", "basic", "advanced"]
                      ).map((mode) => (
                        <option key={mode} value={mode}>
                          {mode}
                        </option>
                      ))}
                    </Select>
                  </label>
                )}
              </>
            )}
            <div className="form-grid">
              <label>
                {t("Results per search")}
                <input
                  aria-label={t("Results per search")}
                  type="number"
                  required
                  min={1}
                  max={
                    draft.family === "openrouter" &&
                    draft.engine === "perplexity"
                      ? 20
                      : capability.max_results
                  }
                  disabled={native}
                  value={draft.limits.max_results}
                  onChange={(event) =>
                    limits({ max_results: Number(event.target.value) })
                  }
                />
                {native && (
                  <small>
                    {t("Native search controls the returned result count.")}
                  </small>
                )}
              </label>
              <label>
                {t("Maximum search attempts")}
                <input
                  aria-label={t("Maximum search attempts")}
                  type="number"
                  required
                  min={1}
                  max={5}
                  value={draft.limits.max_searches}
                  onChange={(event) =>
                    limits({ max_searches: Number(event.target.value) })
                  }
                />
              </label>
              <label>
                {t("Timeout (seconds)")}
                <input
                  aria-label={t("Timeout (seconds)")}
                  type="number"
                  required
                  min={1}
                  max={60}
                  value={draft.limits.timeout_seconds}
                  onChange={(event) =>
                    limits({ timeout_seconds: Number(event.target.value) })
                  }
                />
              </label>
              <label>
                {t("Maximum page fetches")}
                <input
                  aria-label={t("Maximum page fetches")}
                  type="number"
                  required
                  min={0}
                  max={5}
                  disabled={!capability.supports_fetch}
                  value={draft.limits.max_fetches}
                  onChange={(event) =>
                    limits({ max_fetches: Number(event.target.value) })
                  }
                />
              </label>
              <label>
                {t("Retrieved content budget (characters)")}
                <input
                  aria-label={t("Retrieved content budget (characters)")}
                  type="number"
                  required
                  min={500}
                  max={100000}
                  step={100}
                  value={draft.limits.max_content_chars}
                  onChange={(event) =>
                    limits({ max_content_chars: Number(event.target.value) })
                  }
                />
              </label>
              <label className="checkbox-label toggle-label">
                <Switch
                  aria-label={t("Require a hard search limit")}
                  checked={draft.limits.strict_budget}
                  onChange={(event) =>
                    limits({ strict_budget: event.target.checked })
                  }
                />
                {t("Require a hard search limit")}
              </label>
            </div>
            {!knownSearchLimit(draft) && (
              <p className="studio-notice" role="status">
                {t(
                  "This native or automatic route cannot guarantee a hard search count. Strict mode blocks execution; use standalone search or a pinned engine.",
                )}
              </p>
            )}
            {!native && (
              <>
                {supportsDomains && (
                  <label>
                    {t("Allowed sites")}
                    <input
                      aria-label={t("Allowed sites")}
                      value={(draft.filters.allowed_domains || []).join(", ")}
                      onChange={(event) =>
                        filters({
                          allowed_domains: domains(event.target.value),
                        })
                      }
                      placeholder="example.org, example.com"
                    />
                  </label>
                )}
                {supportsDomains && draft.family !== "kimi" && (
                  <label>
                    {t("Excluded sites")}
                    <input
                      aria-label={t("Excluded sites")}
                      value={(draft.filters.excluded_domains || []).join(", ")}
                      onChange={(event) =>
                        filters({
                          excluded_domains: domains(event.target.value),
                        })
                      }
                    />
                  </label>
                )}
                {supportsDates && (
                  <div className="form-grid">
                    <label>
                      {t("Published after")}
                      <input
                        aria-label={t("Published after")}
                        type="date"
                        value={draft.filters.start_date || ""}
                        onChange={(event) =>
                          filters({ start_date: event.target.value })
                        }
                      />
                    </label>
                    <label>
                      {t("Published before")}
                      <input
                        aria-label={t("Published before")}
                        type="date"
                        value={draft.filters.end_date || ""}
                        onChange={(event) =>
                          filters({ end_date: event.target.value })
                        }
                      />
                    </label>
                  </div>
                )}
                {["deepseek", "tavily", "searxng"].includes(draft.family) && (
                  <label>
                    {t("Search language")}
                    <Select
                      aria-label={t("Search language")}
                      value={draft.filters.language || ""}
                      onChange={(event) =>
                        filters({ language: event.target.value })
                      }
                    >
                      <option value="">{t("Service default")}</option>
                      <option value="zh">{t("Chinese")}</option>
                      <option value="en">{t("English")}</option>
                    </Select>
                  </label>
                )}
                {(["deepseek", "tavily"].includes(draft.family) ||
                  (draft.family === "kimi" && draft.mode === "basic")) && (
                  <label className="checkbox-label">
                    <Switch
                      aria-label={t("Include page content")}
                      checked={draft.filters.include_content}
                      onChange={(event) =>
                        filters({ include_content: event.target.checked })
                      }
                    />
                    {t("Include page content")}
                  </label>
                )}
              </>
            )}
            <label className="checkbox-label">
              <Switch
                aria-label={t("Enable this search profile")}
                checked={draft.enabled}
                onChange={(event) => edit({ enabled: event.target.checked })}
              />
              {t("Enable this search profile")}
            </label>
            <small>
              <a href={capability.docs} target="_blank" rel="noreferrer">
                {t("Official search documentation")} ↗
              </a>{" "}
              ·{" "}
              {t(
                "API keys are encrypted in the local database and restored after restart.",
              )}
            </small>
          </fieldset>
          <div className="modal-actions search-editor-actions">
            <Button disabled={busy} onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button primary disabled={busy} type="submit">
              {busy ? t("Saving") : t("Save search profile")}
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
