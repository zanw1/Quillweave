import { useState } from "react";
import {
  IconAdjustmentsHorizontal,
  IconChevronDown,
} from "@tabler/icons-react";
import { Button, Modal } from "../../components/ui";
import { Select } from "../../components/Select";
import { t } from "../../lib/i18n";
import type {
  ChatModelSelection,
  ModelSettings,
  ProviderCatalog,
} from "../../domain/types";
import { modelMetadata } from "../settings/ModelPicker";
import { effortNames } from "../settings/options";

interface Props {
  settings: ModelSettings | null;
  catalog: ProviderCatalog | null;
  selection?: ChatModelSelection;
  onChange: (selection: ChatModelSelection) => void;
  onSettings: () => void;
  expanded: boolean;
  onExpanded: (value: boolean) => void;
}

/** Choose the next submission's model without changing any already running request. */
export function ChatModelControls({
  settings,
  catalog,
  selection,
  onChange,
  onSettings,
  expanded,
  onExpanded,
}: Props) {
  const [filter, setFilter] = useState("");
  const profiles =
    settings?.profiles.filter((profile) => profile.enabled) || [];
  const profile = profiles.find((item) => item.id === selection?.profile_id);
  const metadata = modelMetadata(catalog, profile?.config);
  const preset = catalog?.presets.find(
    (item) => item.id === profile?.config.provider_id,
  );
  const efforts =
    metadata?.reasoning_efforts || preset?.reasoning_efforts || [];
  const limits = { ...metadata?.limits, ...profile?.config.model_limits };
  const effort =
    selection?.reasoning_effort ||
    (profile?.config.thinking_mode === "disabled"
      ? "none"
      : profile?.config.reasoning_effort);
  const effortLabel =
    effort === "none"
      ? t("Thinking off")
      : effort
        ? t(effortNames[effort] || effort)
        : "";

  /** Switch profiles with fresh automatic output and the target model's default reasoning. */
  function choose(profileId: string) {
    onChange({ profile_id: profileId, output_mode: "auto" });
  }
  return (
    <>
      <button
        className="chat-model-trigger text-button"
        aria-label={t("Choose model")}
        title={
          profile
            ? `${profile.name} · ${profile.config.model}`
            : t("Choose model")
        }
        onClick={() => onExpanded(true)}
      >
        <span className="chat-model-name">
          {profile ? metadata?.name || profile.config.model : t("Choose model")}
        </span>
        {effortLabel && (
          <span className="chat-model-reasoning">{effortLabel}</span>
        )}
        <IconChevronDown size={13} />
      </button>
      {expanded && (
        <Modal
          title={t("Model for this conversation")}
          onClose={() => onExpanded(false)}
          footer={
            <>
              <Button onClick={onSettings}>
                <IconAdjustmentsHorizontal size={15} />
                {t("Manage connections")}
              </Button>
              <Button primary onClick={() => onExpanded(false)}>
                {t("Done")}
              </Button>
            </>
          }
        >
          <div className="modal-content studio-form">
            <p className="muted">
              {t(
                "Changes apply to the next submission. Every reply keeps the model that actually generated it.",
              )}
            </p>
            <input
              aria-label={t("Search models")}
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
              placeholder={t("Search models")}
            />
            <label>
              {t("Model")}
              <Select
                aria-label={t("Conversation model")}
                value={selection?.profile_id || ""}
                onChange={(event) => choose(event.target.value)}
              >
                <option value="" disabled>
                  {t("Choose model")}
                </option>
                {selection?.profile_id && !profile && (
                  <option value={selection.profile_id} disabled>
                    {t("Unavailable profile (select another)")}
                  </option>
                )}
                {profiles
                  .filter((item) =>
                    `${item.name} ${item.config.model}`
                      .toLocaleLowerCase()
                      .includes(filter.toLocaleLowerCase()),
                  )
                  .map((item) => (
                    <option value={item.id} key={item.id}>
                      {item.name} · {item.config.model}
                    </option>
                  ))}
              </Select>
            </label>
            {profile && selection && (
              <>
                {!!efforts.length && (
                  <label>
                    {t("Reasoning effort")}
                    <Select
                      aria-label={t("Conversation reasoning effort")}
                      value={selection.reasoning_effort || ""}
                      onChange={(event) =>
                        onChange({
                          ...selection,
                          reasoning_effort: event.target.value || undefined,
                        })
                      }
                    >
                      <option value="">{t("Model default")}</option>
                      {efforts.map((effort) => (
                        <option key={effort} value={effort}>
                          {effort}
                        </option>
                      ))}
                    </Select>
                  </label>
                )}
                <label>
                  {t("Output budget")}
                  <Select
                    aria-label={t("Conversation output mode")}
                    value={selection.output_mode}
                    onChange={(event) =>
                      onChange({
                        ...selection,
                        output_mode: event.target.value as "auto" | "custom",
                        max_tokens:
                          event.target.value === "custom"
                            ? profile.config.max_tokens
                            : undefined,
                      })
                    }
                  >
                    <option value="auto">
                      {t("Automatic from model capacity")}
                    </option>
                    <option value="custom">{t("Custom output tokens")}</option>
                  </Select>
                </label>
                {selection.output_mode === "custom" && (
                  <label>
                    {t("Maximum output tokens")}
                    <input
                      type="number"
                      min={1}
                      max={
                        limits.max_output_tokens ||
                        catalog?.resource_limits.max_output_tokens
                      }
                      value={selection.max_tokens ?? ""}
                      onChange={(event) =>
                        onChange({
                          ...selection,
                          max_tokens: Number(event.target.value),
                        })
                      }
                    />
                  </label>
                )}
                <p className="muted">
                  {t("Context window")} ·{" "}
                  {limits.context_window_tokens?.toLocaleString() ||
                    t("Unknown")}{" "}
                  tokens
                  <br />
                  {t("Model output limit (tokens)")} ·{" "}
                  {limits.max_output_tokens?.toLocaleString() || t("Unknown")}
                </p>
              </>
            )}
          </div>
        </Modal>
      )}
    </>
  );
}
