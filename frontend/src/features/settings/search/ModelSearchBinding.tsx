import { useEffect, useState } from "react";
import { Select } from "../../../components/Select";
import { Button, ErrorBox } from "../../../components/ui";
import { message } from "../../../lib/api";
import { t } from "../../../lib/i18n";
import { searchSelection, selectionValue } from "../../../domain/search";
import { useSearchSettings } from "./state";

/** Associate a saved model with search independently, preserving unsaved choices after CAS conflicts. */
export function ModelSearchBinding({ modelId }: { modelId: string }) {
  const state = useSearchSettings();
  const [choice, setChoice] = useState("inherit");
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  useEffect(() => {
    setDirty(false);
    setSaved(false);
    setChoice("inherit");
  }, [modelId]);
  useEffect(() => {
    if (state.view && !dirty)
      setChoice(selectionValue(state.view.settings.bindings[modelId]));
  }, [state.view, dirty, modelId]);
  /** Save only this binding and leave both the model draft and search-profile metadata untouched. */
  async function save() {
    setBusy(true);
    state.setError("");
    try {
      await state.mutate("set_binding", {
        model_profile_id: modelId,
        binding: searchSelection(choice),
      });
      setDirty(false);
      setSaved(true);
    } catch (error) {
      state.setError(message(error));
    } finally {
      setBusy(false);
    }
  }
  const exists = state.view?.models.some((model) => model.id === modelId);
  return (
    <section className="model-search-binding">
      <label>
        {t("Web search for this model")}
        <Select
          aria-label={t("Web search for this model")}
          disabled={busy || !exists}
          value={choice}
          onChange={(event) => {
            setChoice(event.target.value);
            setDirty(true);
            setSaved(false);
          }}
        >
          <option value="inherit">{t("Inherit default search profile")}</option>
          <option value="off">{t("Disable web search")}</option>
          {state.view?.settings.profiles.map((profile) => (
            <option key={profile.id} value={profile.id}>
              {profile.name}
              {profile.enabled ? "" : ` · ${t("Disabled")}`}
            </option>
          ))}
        </Select>
      </label>
      <div className="search-actions">
        <Button
          disabled={busy || !dirty || !exists}
          onClick={() => void save()}
        >
          {saved ? t("Saved") : t("Save search association")}
        </Button>
        <Button
          disabled={busy}
          onClick={() => void state.reload().catch(() => {})}
        >
          {t("Reload")}
        </Button>
      </div>
      <small>
        {!exists
          ? t("Save this model profile before associating search.")
          : t(
              "Research and agent tools use this association. Ordinary prose requests do not search automatically.",
            )}
      </small>
      {state.error && <ErrorBox>{state.error}</ErrorBox>}
    </section>
  );
}
