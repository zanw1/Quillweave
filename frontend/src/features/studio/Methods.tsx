import { useEffect, useState } from "react";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import { Badge, Button, ErrorBox } from "../../components/ui";
import type { Project } from "../../domain/types";
import type { WritingSkill } from "../../domain/studio";
import { EvaluationLab } from "./EvaluationLab";
import { ExternalMemory } from "./Research";

/** Maintain transparent craft preferences and keep research experiments separate from accepted story canon. */
export function StudioMethods({
  project,
  onProject,
}: {
  project: Project;
  onProject: (project: Project) => void;
}) {
  const [skills, setSkills] = useState<WritingSkill[]>([]);
  const [selected, setSelected] = useState(project.studio.skills);
  const [preferences, setPreferences] = useState(
    project.studio.preferences.join("\n"),
  );
  const [base, setBase] = useState(project.revision);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    void api<WritingSkill[]>("writing_skills")
      .then(setSkills)
      .catch((error) => setError(message(error)));
  }, []);
  useEffect(() => {
    setSelected(project.studio.skills);
    setPreferences(project.studio.preferences.join("\n"));
    setBase(project.revision);
  }, [project.id]);

  /** Save the author's chosen methods and durable preferences without overwriting knowledge collections. */
  async function save() {
    setBusy(true);
    setError("");
    try {
      const next = await api<Project>("save_studio", {
        project_id: project.id,
        revision: base,
        studio: {
          ...project.studio,
          skills: selected,
          preferences: preferences.split("\n").filter(Boolean),
        },
      });
      onProject(next);
      setBase(next.revision);
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <section className="studio-section">
        <h2>{t("Writing approaches")}</h2>
        <div className="method-grid">
          {skills.map((skill) => (
            <article className="research-card" key={skill.id}>
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={selected.includes(skill.id)}
                  onChange={(event) =>
                    setSelected(
                      event.target.checked
                        ? [...selected, skill.id]
                        : selected.filter((id) => id !== skill.id),
                    )
                  }
                />
                <strong>{t(skill.name)}</strong>
              </label>
              <p>{t(skill.description)}</p>
              <p className="muted">
                {t("Version")} {skill.version} · {t("Applicable forms")}:{" "}
                {skill.modes
                  .map((mode) =>
                    t(
                      (
                        {
                          all: "All kinds",
                          serial: "Web serial",
                          traditional: "Traditional novel",
                          hybrid: "Hybrid",
                        } as Record<string, string>
                      )[mode] || mode,
                    ),
                  )
                  .join(" / ")}
              </p>
              <a href={skill.source} target="_blank" rel="noreferrer">
                {t("Source")}
              </a>
            </article>
          ))}
        </div>
        <label className="studio-form">
          {t("Preferences")}
          <textarea
            value={preferences}
            onChange={(event) => setPreferences(event.target.value)}
            placeholder={t("One per line")}
          />
        </label>
        <Button primary disabled={busy} onClick={() => void save()}>
          {t("Save approaches")}
        </Button>
        {error && <ErrorBox>{error}</ErrorBox>}
        <h3>{t("Saved author decisions")}</h3>
        {project.studio.decisions.map((decision) => (
          <details className="decision-card" key={decision.id}>
            <summary>
              <Badge tone={decision.status === "accepted" ? "green" : "gray"}>
                {t(
                  decision.status === "accepted"
                    ? "Accepted"
                    : decision.status === "rejected"
                      ? "Rejected"
                      : "Proposed",
                )}
              </Badge>{" "}
              {decision.title}
            </summary>
            <p>{decision.content}</p>
          </details>
        ))}
      </section>
      <ExternalMemory project={project} />
      <EvaluationLab project={project} />
    </>
  );
}
