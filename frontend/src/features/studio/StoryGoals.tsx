import { useEffect, useState } from "react";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import { Button, ErrorBox, RequiredLabel } from "../../components/ui";
import { Select } from "../../components/Select";
import { WritingParameters } from "../writing/WritingParameters";
import type { Project } from "../../domain/types";
import { publishSetupDraft } from "./setupDrafts";

export const formNames: Record<string, string> = {
  undecided: "Undecided",
  serial: "Web serial",
  traditional: "Traditional novel",
  hybrid: "Hybrid",
};
export const stageNames: Record<string, string> = {
  idea: "Idea",
  direction: "Direction",
  setting: "Setting",
  world: "World",
  planning: "Planning",
  writing: "Writing",
  review: "Review",
};

/** Edit the complete creative brief with an explicit base revision and revisable length targets. */
export function StoryGoals({
  project,
  onProject,
  onClose,
}: {
  project: Project;
  onProject: (project: Project) => void;
  onClose: () => void;
}) {
  const [draft, setDraft] = useState(project.studio);
  const [base] = useState(project.revision);
  const [story, setStory] = useState({
    title: project.title,
    genre: project.genre,
    premise: project.premise,
    style: project.style,
    outline: project.outline,
    writing_style: project.writing_style,
    default_chapter_words: project.default_chapter_words,
    narrative_pov: project.narrative_pov,
  });
  const [error, setError] = useState("");
  useEffect(() => {
    publishSetupDraft("goals", {
      label: story.title || t("Story goals"),
      text: JSON.stringify({ story, studio: draft }),
    });
    return () => publishSetupDraft("goals", null);
  }, [draft, story]);
  const [busy, setBusy] = useState(false);

  /** Save story metadata and studio goals as a single revision-protected command. */
  async function save() {
    setBusy(true);
    setError("");
    try {
      const next = await api<Project>("save_studio", {
        project_id: project.id,
        revision: base,
        studio: draft,
        story,
      });
      onProject(next);
      onClose();
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      className="modal-content studio-form"
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <div className="form-grid">
        <label>
          <RequiredLabel>{t("Project title")}</RequiredLabel>
          <input
            value={story.title}
            onChange={(event) =>
              setStory({ ...story, title: event.target.value })
            }
            maxLength={120}
            required
          />
        </label>
        <label>
          {t("Genre")}
          <input
            value={story.genre}
            onChange={(event) =>
              setStory({ ...story, genre: event.target.value })
            }
          />
        </label>
      </div>
      <div className="form-grid">
        <label>
          {t("Story language")}
          <Select
            value={draft.language}
            onChange={(event) =>
              setDraft({ ...draft, language: event.target.value })
            }
          >
            <option value="inherit">{t("Follow interface")}</option>
            <option value="zh-CN">中文</option>
            <option value="en">English</option>
          </Select>
        </label>
        <label>
          {t("Story form")}
          <Select
            value={draft.mode}
            onChange={(event) =>
              setDraft({ ...draft, mode: event.target.value })
            }
          >
            {Object.entries(formNames).map(([key, value]) => (
              <option value={key} key={key}>
                {t(value)}
              </option>
            ))}
          </Select>
        </label>
        <label>
          {t("Stage")}
          <Select
            value={draft.stage}
            onChange={(event) =>
              setDraft({ ...draft, stage: event.target.value })
            }
          >
            {Object.entries(stageNames).map(([key, value]) => (
              <option value={key} key={key}>
                {t(value)}
              </option>
            ))}
          </Select>
        </label>
      </div>
      <div className="form-grid">
        {(
          [
            ["target_characters", "Total target characters"],
            ["target_chapters", "Target chapters"],
            ["target_volumes", "Target volumes"],
          ] as const
        ).map(([key, title]) => (
          <label key={key}>
            {t(title)}
            <input
              type="number"
              min={0}
              step={1}
              value={draft[key]}
              onChange={(event) =>
                setDraft({ ...draft, [key]: Number(event.target.value) })
              }
            />
            <small>0 = {t("Unspecified")}</small>
          </label>
        ))}
      </div>
      <Button
        onClick={() =>
          setDraft({
            ...draft,
            target_chapters: Math.max(
              1,
              Math.round(
                draft.target_characters /
                  Math.max(1, story.default_chapter_words),
              ),
            ),
          })
        }
        disabled={!draft.target_characters}
      >
        {t("Estimate chapter count")}
      </Button>
      {draft.target_characters > 0 &&
        draft.target_chapters > 0 &&
        draft.target_characters !==
          draft.target_chapters * story.default_chapter_words && (
          <p role="status" className="studio-notice">
            {t("Planning warnings")}: {draft.target_chapters} ×{" "}
            {story.default_chapter_words} ={" "}
            {(
              draft.target_chapters * story.default_chapter_words
            ).toLocaleString()}{" "}
            ≠ {draft.target_characters.toLocaleString()}
          </p>
        )}
      <WritingParameters
        value={story}
        onChange={(patch) => setStory({ ...story, ...patch })}
      />
      <div className="form-grid">
        <label>
          {t("Pacing")}
          <input
            value={draft.pacing}
            onChange={(event) =>
              setDraft({ ...draft, pacing: event.target.value })
            }
          />
        </label>
        <label>
          {t("Recurring reading experience")}
          <textarea
            value={draft.narrative_focus}
            placeholder={t(
              "Describe the activities, relationships, voice and changes readers should want to revisit.",
            )}
            onChange={(event) =>
              setDraft({ ...draft, narrative_focus: event.target.value })
            }
          />
        </label>
        <label>
          {t("Target audience")}
          <input
            value={draft.audience}
            onChange={(event) =>
              setDraft({ ...draft, audience: event.target.value })
            }
          />
        </label>
      </div>
      <label>
        {t("Core premise")}
        <textarea
          value={story.premise}
          onChange={(event) =>
            setStory({ ...story, premise: event.target.value })
          }
        />
      </label>
      <label>
        {t("Narrative style and boundaries")}
        <textarea
          value={story.style}
          onChange={(event) =>
            setStory({ ...story, style: event.target.value })
          }
        />
      </label>
      <label>
        {t("Story outline")}
        <textarea
          value={story.outline}
          onChange={(event) =>
            setStory({ ...story, outline: event.target.value })
          }
        />
      </label>
      <div className="form-grid">
        <label>
          {t("Naming preference")}
          <Select
            value={draft.naming_style}
            onChange={(event) =>
              setDraft({ ...draft, naming_style: event.target.value })
            }
          >
            <option value="original">{t("Originality first")}</option>
            <option value="market">{t("Market fit")}</option>
          </Select>
        </label>
        <label>
          {t("Forbidden names and patterns")}
          <textarea
            value={draft.forbidden_names.join("\n")}
            onChange={(event) =>
              setDraft({
                ...draft,
                forbidden_names: event.target.value.split("\n").filter(Boolean),
              })
            }
            placeholder={t("One per line")}
          />
        </label>
      </div>
      {error && <ErrorBox>{error}</ErrorBox>}
      <div className="modal-actions">
        <Button onClick={onClose}>{t("Cancel")}</Button>
        <Button primary type="submit" disabled={busy}>
          {t("Save goals")}
        </Button>
      </div>
    </form>
  );
}
