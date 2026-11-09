import { useEffect, useState } from "react";
import { IconBook2, IconTimeline, IconMap2 } from "@tabler/icons-react";
import { t } from "../../lib/i18n";
import { Badge } from "../../components/ui";
import { Wiki } from "../wiki/Wiki";
import { Memories } from "../studio/Memories";
import { WorldMap } from "./WorldMap";
import type { Project, WikiHealth } from "../../domain/types";
import { ReviewSetupButton } from "../studio/ReviewSetupButton";

interface Props {
  project: Project;
  onProject: (project: Project) => void;
  onChapter: (chapterId: string) => void;
  findings?: WikiHealth["findings"];
  initialSection?: "wiki" | "memory";
  focusId?: string;
  onFocus?: (id: string) => void;
  onOpenTarget?: (kind: string, id: string) => void;
  onAsk?: (text: string, action?: string, intent?: string) => void;
}

/** Merge Character/World Wiki and Story Memory into a unified Step 2 hub. */
export function CharacterWorldHub({
  project,
  onProject,
  onChapter,
  findings = [],
  initialSection = "wiki",
  focusId,
  onFocus,
  onOpenTarget,
  onAsk,
}: Props) {
  const [section, setSection] = useState<"wiki" | "memory" | "map">(
    initialSection,
  );
  useEffect(() => {
    if (project.studio.memories.some((item) => item.id === focusId))
      setSection("memory");
    else if (project.entities.some((item) => item.id === focusId))
      setSection("wiki");
    else if (
      [
        ...(project.studio.locations || []),
        ...(project.studio.routes || []),
      ].some((item) => item.id === focusId)
    )
      setSection("map");
  }, [focusId]);

  return (
    <div className="character-world-hub">
      <div
        className="hub-switcher"
        role="tablist"
        aria-label={t("Story settings")}
      >
        <button
          role="tab"
          aria-selected={section === "map"}
          className={`hub-tab ${section === "map" ? "active" : ""}`}
          onClick={() => setSection("map")}
        >
          <IconMap2 size={16} />
          {t("World map")}
        </button>
        <button
          role="tab"
          aria-selected={section === "wiki"}
          className={`hub-tab ${section === "wiki" ? "active" : ""}`}
          onClick={() => setSection("wiki")}
        >
          <IconBook2 size={16} />
          {t("Setting records")}
          <Badge>{project.entities.length}</Badge>
        </button>
        <button
          role="tab"
          aria-selected={section === "memory"}
          className={`hub-tab ${section === "memory" ? "active" : ""}`}
          onClick={() => setSection("memory")}
        >
          <IconTimeline size={16} />
          {t("Narrative state & clues")}
          <Badge>{project.studio.memories.length}</Badge>
        </button>
        {onAsk && (
          <span className="hub-review">
            <ReviewSetupButton onAsk={onAsk} />
          </span>
        )}
      </div>
      {section === "map" ? (
        <WorldMap
          project={project}
          onProject={onProject}
          onFocus={onFocus}
          focusId={focusId}
          onChapter={onChapter}
          onOpenTarget={onOpenTarget}
        />
      ) : section === "wiki" ? (
        <Wiki
          project={project}
          onProject={onProject}
          onChapter={onChapter}
          focusId={focusId}
          onFocus={onFocus}
        />
      ) : (
        <div className="workspace-page">
          <Memories
            project={project}
            onProject={onProject}
            onWiki={() => setSection("wiki")}
            focusId={focusId}
          />
          {findings.length > 0 && (
            <section className="studio-section">
              <h3>{t("Review hints")}</h3>
              {findings.map((finding, index) => (
                <article className="research-card" key={index}>
                  <Badge>{finding.kind}</Badge>
                  <strong>{finding.title}</strong>
                  <p>{t(finding.detail)}</p>
                  <small>{finding.source_id}</small>
                </article>
              ))}
            </section>
          )}
        </div>
      )}
    </div>
  );
}
