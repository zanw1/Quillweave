import { t } from "../../lib/i18n";
import { EvaluationLab } from "../studio/EvaluationLab";
import type { Project } from "../../domain/types";

/** Keep optional evaluation tools behind an explicit advanced section for the selected story. */
export function AdvancedLabSection({ project }: { project?: Project }) {
  return (
    <details className="studio-section">
      <summary>{t("Advanced & experiments")}</summary>
      {project ? (
        <EvaluationLab project={project} />
      ) : (
        <p className="muted">
          {t("Open a project to inspect its experiments.")}
        </p>
      )}
    </details>
  );
}
