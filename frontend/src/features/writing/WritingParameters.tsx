import { t } from "../../lib/i18n";
import { useEffect, useState } from "react";
import { api } from "../../lib/api";
import "./writing.css";
import type { StylePreset } from "../../domain/types";

export interface WritingParametersValue {
  writing_style: string;
  default_chapter_words: number;
  narrative_pov: string;
}

const legacyPovLabels: Record<string, string> = {
  "\u7b2c\u4e00\u4eba\u79f0": "First person",
  "\u7b2c\u4e09\u4eba\u79f0\u6709\u9650\u89c6\u89d2": "Third-person limited",
  "\u5168\u77e5\u89c6\u89d2": "Omniscient",
};

/** Share writing controls between project creation and planning using backend style presets. */
export function WritingParameters({
  value,
  onChange,
}: {
  value: WritingParametersValue;
  onChange: (patch: Partial<WritingParametersValue>) => void;
}) {
  const [presets, setPresets] = useState<StylePreset[]>([]);
  useEffect(() => {
    let active = true;
    api<StylePreset[]>("writing_presets")
      .then((result) => {
        if (active) setPresets(result);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, []);
  const selected = presets.find((p) => p.id === value.writing_style);
  return (
    <div className="writing-parameters">
      <div className="form-grid">
        <label>
          {t("Writing style")}
          <select
            value={value.writing_style}
            onChange={(e) => onChange({ writing_style: e.target.value })}
          >
            {!presets.length && <option value="custom">{t("Custom style")}</option>}
            {presets.map((p) => (
              <option key={p.id} value={p.id}>
                {t(p.name)}
              </option>
            ))}
          </select>
          <small>
            {t(selected?.description || "Style notes are editable creative choices.")}
          </small>
        </label>
        <label>
          {t("Default chapter target characters")}
          <input
            type="number"
            min={100}
            max={50000}
            step={100}
            value={value.default_chapter_words}
            onChange={(e) =>
              onChange({ default_chapter_words: Number(e.target.value) })
            }
          />
          <small>
            {t("Each chapter can override this target. The editor displays measured character counts.")}
          </small>
        </label>
        <label>
          {t("Narrative viewpoint")}
          <select
            value={legacyPovLabels[value.narrative_pov] || value.narrative_pov}
            onChange={(e) => onChange({ narrative_pov: e.target.value })}
          >
            <option value="First person">{t("First person")}</option>
            <option value="Third-person limited">{t("Third-person limited")}</option>
            <option value="Omniscient">{t("Omniscient")}</option>
          </select>
        </label>
      </div>
    </div>
  );
}
