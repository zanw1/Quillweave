import {
  IconBook2,
  IconCompass,
  IconHome,
  IconPencil,
  IconSettings,
  IconSitemap,
  IconUsers,
} from "@tabler/icons-react";
import { WorkspaceBrand } from "../../app/WorkspaceControls";
import { t } from "../../lib/i18n";
import type { Page, Project } from "../../domain/types";

interface Props {
  page: Page;
  project: Project | null;
  loading: boolean;
  busy: boolean;
  selectedPage: boolean;
  activeFlowStep: string;
  onHome: () => void;
  onSwitchProject: () => void;
  onSelectFlowStep: (step: string) => void;
  onPage: (page: Page) => void;
}

const sections = [
  { key: "setup", label: "Workspace overview", icon: IconCompass },
  { key: "world", label: "Story settings", icon: IconUsers },
  { key: "plan", label: "Outline", icon: IconSitemap },
  { key: "write", label: "Manuscript", icon: IconPencil },
];

/** Keep project navigation to four destinations and one global settings entry. */
export function FlowSidebar({
  page,
  project,
  loading,
  busy,
  selectedPage,
  activeFlowStep,
  onHome,
  onSwitchProject,
  onSelectFlowStep,
  onPage,
}: Props) {
  const pending =
    project?.studio.changesets?.filter(
      (cs) => cs.status === "staged" || cs.status === "conflicted",
    ).length || 0;
  return (
    <aside className="sidebar">
      <WorkspaceBrand onHome={onHome} />
      <button
        className="workspace-picker"
        onClick={onSwitchProject}
        disabled={loading || busy}
        aria-label={t("Switch project")}
      >
        <span className="workspace-monogram">
          <IconBook2 size={20} />
        </span>
        <span>
          {project?.title || t("My workspace")}
          <small>{t("Switch project")}</small>
        </span>
      </button>
      <button
        className={`nav-item ${page === "home" ? "active" : ""}`}
        onClick={onHome}
      >
        <IconHome size={20} />
        {t("My projects")}
      </button>
      <nav className="flow-step-list" aria-label={t("Story workspace")}>
        {sections.map((section) => {
          const Icon = section.icon;
          const active = selectedPage && activeFlowStep === section.key;
          return (
            <button
              key={section.key}
              className={`nav-item flow-step-item ${active ? "active" : ""}`}
              aria-current={active ? "page" : undefined}
              aria-label={t(section.label)}
              disabled={loading || busy}
              onClick={() => onSelectFlowStep(section.key)}
            >
              <Icon size={18} />
              <span>{t(section.label)}</span>
              {section.key === "setup" && pending > 0 && (
                <span className="nav-count" aria-label={t("Pending changes")}>
                  {pending}
                </span>
              )}
            </button>
          );
        })}
      </nav>
      <div className="sidebar-bottom">
        <button
          className={`nav-item ${page === "settings" ? "active" : ""}`}
          onClick={() => onPage("settings")}
        >
          <IconSettings size={20} />
          {t("Settings")}
        </button>
        <div className="local-status">
          <span className="connection connected" />
          <div>{t("Saved locally · Export anytime")}</div>
        </div>
      </div>
    </aside>
  );
}
