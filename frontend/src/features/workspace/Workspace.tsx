import { useCallback, useEffect, useRef, useState } from "react";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import { Button, DockablePanel, ErrorBox, Modal } from "../../components/ui";
import { Assistant } from "../assistant/Assistant";
import { Studio } from "../studio/Studio";
import { Plans } from "../studio/Plans";
import { Research } from "../studio/Research";
import { Library } from "../library/Library";
import { CharacterWorldHub } from "../world/CharacterWorldHub";
import { Writing } from "../writing/Editor";
import type {
  Chapter,
  Page,
  Project,
  Source,
  WorkspaceContext,
} from "../../domain/types";
import "./workspace.css";

interface Props {
  project: Project;
  chapter?: Chapter;
  page: Page;
  activeStep: string;
  assistantOpen: boolean;
  sourcesOpen: boolean;
  onAssistantOpen: (open: boolean) => void;
  onSourcesClose: () => void;
  onProject: (project: Project) => void;
  onChapter: (id: string) => void;
  onStep: (step: string) => void;
  onSettings: () => void;
}

/** Own one project conversation, its current focus and cross-process refresh independently of page content. */
export function Workspace({
  project,
  chapter,
  page,
  activeStep,
  assistantOpen,
  sourcesOpen,
  onAssistantOpen,
  onSourcesClose,
  onProject,
  onChapter,
  onStep,
  onSettings,
}: Props) {
  const [seed, setSeed] = useState({ text: "", action: "agent", version: 0 });
  const [focus, setFocus] = useState<WorkspaceContext>({});
  const [selection, setSelection] = useState<{
    chapterId: string;
    text: string;
  } | null>(null);
  const [source, setSource] = useState<Source | undefined>();
  const [researchTab, setResearchTab] = useState(false);
  const [narrow, setNarrow] = useState(() => window.innerWidth < 1280);
  const [error, setError] = useState("");
  const editorSave = useRef<(() => Promise<Project | null>) | null>(null);
  const refreshRef = useRef<() => Promise<void>>(async () => {});
  const activeChapter = page === "write" ? chapter : undefined;
  // Request assembly also checks chapter identity before this effect clears an older selection.
  useEffect(() => {
    setSelection((current) =>
      current?.chapterId === activeChapter?.id ? current : null,
    );
  }, [activeChapter?.id]);
  const step =
    page === "write"
      ? "write"
      : page === "wiki"
        ? "world"
        : activeStep === "plan"
          ? "plan"
          : "setup";

  /** Refresh persisted data while the application shell rejects responses for a different active project. */
  async function refresh() {
    onProject(await api<Project>("get_project", { project_id: project.id }));
  }
  refreshRef.current = refresh;

  useEffect(() => {
    const query = window.matchMedia("(max-width: 1279px)");
    /** Switch presentation without remounting the conversation or its running request. */
    function resize() {
      setNarrow(query.matches);
      if (query.matches) onAssistantOpen(false);
    }
    query.addEventListener("change", resize);
    return () => query.removeEventListener("change", resize);
  }, [onAssistantOpen]);

  useEffect(() => {
    /** Observe CLI writes and pending candidates when this workspace is visible. */
    function synchronize() {
      if (document.visibilityState !== "hidden")
        void refreshRef.current().catch((cause) => setError(message(cause)));
    }
    const timer = window.setInterval(synchronize, 3000);
    window.addEventListener("focus", synchronize);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("focus", synchronize);
    };
  }, [project.id]);

  const registerSave = useCallback(
    (save: (() => Promise<Project | null>) | null) => {
      editorSave.current = save;
    },
    [],
  );

  /** Save current prose edits before sending the chapter's authoritative content to the model. */
  async function beforeStart() {
    if (page === "write" && editorSave.current) return editorSave.current();
    return api<Project>("get_project", { project_id: project.id });
  }

  /** Focus the shared composer with a task hint while retaining its current conversation. */
  function ask(text: string, action = "agent") {
    setSeed((previous) => ({ text, action, version: previous.version + 1 }));
    onAssistantOpen(true);
  }

  /** Open the actual saved resource and make its identity available to the next conversation turn. */
  function openTarget(kind: string, id: string) {
    if (kind === "chapter") {
      onChapter(id);
      onStep("write");
    } else if (kind === "plan") {
      setFocus({ active_plan_id: id });
      onStep("plan");
    } else if (["entity", "memory", "location", "route"].includes(kind)) {
      setFocus({ active_entity_id: id });
      onStep("world");
    } else {
      onStep("setup");
    }
    if (narrow) onAssistantOpen(false);
  }

  /** Send source analysis through the same conversation after closing the material browser. */
  function analyzeSource(item: Source) {
    setSource(item);
    onSourcesClose();
    ask(
      t("Review this source and propose useful story material with citations."),
    );
  }

  const context: WorkspaceContext = {
    active_source_id: source?.id,
    active_step: step,
    active_chapter_id: activeChapter?.id,
    active_entity_id: step === "world" ? focus.active_entity_id : undefined,
    active_plan_id: step === "plan" ? focus.active_plan_id : undefined,
    selected_text:
      step === "write" && selection?.chapterId === activeChapter?.id
        ? selection?.text
        : undefined,
  };

  return (
    <div className="unified-workspace">
      <section className="workspace-content">
        {error && <ErrorBox>{error}</ErrorBox>}
        {step === "setup" && (
          <Studio
            project={project}
            onProject={onProject}
            onSettings={onSettings}
            onAsk={ask}
            onSelectFlowStep={onStep}
            onOpenTarget={openTarget}
          />
        )}
        {step === "plan" && (
          <div className="workspace-section">
            <h1>{t("Story outline")}</h1>
            <Plans
              project={project}
              onProject={onProject}
              onAsk={ask}
              focusId={focus.active_plan_id}
              onFocus={(id) => setFocus({ active_plan_id: id })}
              onChapter={(id) => {
                onChapter(id);
                onStep("write");
              }}
            />
          </div>
        )}
        {step === "world" && (
          <CharacterWorldHub
            project={project}
            onProject={onProject}
            onOpenTarget={openTarget}
            focusId={focus.active_entity_id}
            onFocus={(id) => setFocus({ active_entity_id: id })}
            onChapter={(id) => {
              onChapter(id);
              onStep("write");
            }}
          />
        )}
        {step === "write" && (
          <Writing
            project={project}
            chapter={chapter}
            onProject={onProject}
            onChapter={onChapter}
            onSettings={onSettings}
            onWiki={() => onStep("world")}
            seed=""
            embedded
            onRegisterSave={registerSave}
            onSelection={(text) =>
              setSelection({ chapterId: chapter?.id || "", text })
            }
          />
        )}
      </section>
      <DockablePanel
        title={t("Creative partner")}
        open={assistantOpen}
        modal={narrow}
        onClose={() => onAssistantOpen(false)}
        className={`workspace-assistant ${narrow ? "is-drawer" : "is-docked"}`}
      >
        <Assistant
          project={project}
          chapter={activeChapter}
          source={source}
          global
          workspaceContext={context}
          initialAction="agent"
          exploring={
            !project.premise.trim() &&
            !project.chapters.length &&
            !project.studio.plans.length &&
            !project.studio.decisions.some((item) => item.status === "accepted")
          }
          seed={seed.text}
          seedAction={seed.action}
          seedVersion={seed.version}
          onSeedConsumed={() =>
            setSeed((previous) => ({ ...previous, text: "" }))
          }
          onSettings={onSettings}
          onRefresh={refresh}
          beforeStart={beforeStart}
          onOpenTarget={openTarget}
          onClose={() => onAssistantOpen(false)}
          onClearSource={() => setSource(undefined)}
        />
      </DockablePanel>
      {sourcesOpen && (
        <Modal title={t("Sources & research")} wide onClose={onSourcesClose}>
          <div className="workspace-sources">
            <div
              className="tabs"
              role="tablist"
              aria-label={t("Sources & research")}
            >
              <button
                role="tab"
                aria-selected={!researchTab}
                onClick={() => setResearchTab(false)}
              >
                {t("Local sources")}
              </button>
              <button
                role="tab"
                aria-selected={researchTab}
                onClick={() => setResearchTab(true)}
              >
                {t("Web research")}
              </button>
            </div>
            {researchTab ? (
              <Research
                project={project}
                onProject={onProject}
                onAsk={(text) => {
                  onSourcesClose();
                  ask(text);
                }}
              />
            ) : (
              <Library
                project={project}
                onProject={onProject}
                onSettings={onSettings}
                onAskSource={analyzeSource}
              />
            )}
            <Button onClick={onSourcesClose}>{t("Close")}</Button>
          </div>
        </Modal>
      )}
    </div>
  );
}
