import type {
  ProviderCatalog,
  ProviderConfig,
  ProviderPreset,
} from "../../domain/types";

export const protocolNames: Record<string, string> = {
  openai: "Chat Completions compatible",
  responses: "OpenAI Responses compatible",
  anthropic: "Anthropic Messages",
  gemini: "Gemini Interactions",
  gemini_generate: "Gemini Generate Content",
  ollama: "Ollama native API",
};
export const effortNames: Record<string, string> = {
  none: "None",
  minimal: "Minimal",
  low: "Low",
  medium: "Medium",
  high: "High",
  xhigh: "Extra high",
  max: "Maximum",
};

/** Recognize filled workspace endpoint templates while retaining custom gateway URLs. */
export function endpointMatches(value: string, template: string) {
  const current = value.replace(/\/+$/, "");
  if (!template.includes("{WorkspaceId}")) return current === template;
  const [prefix, suffix] = template.split("{WorkspaceId}");
  return current.startsWith(prefix) && current.endsWith(suffix);
}

/** Choose hints for the provider, protocol, and known model, with backend enforcement. */
export function selectedPreset(
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
export function customOptions(
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
