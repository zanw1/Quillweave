import { ChangeSetEditor } from "./ChangeSetEditor";
import "./review.css";
import type { ChangeSet, Project } from "../../domain/types";

interface Props {
  project: Project;
  changesets: ChangeSet[];
  onRefresh: () => Promise<void>;
  onChangeSetUpdated?: (changeset: ChangeSet) => void;
  onOpenTarget?: (kind: string, id: string) => void;
  beforeConfirm?: () => Promise<Project | null>;
}

/** Render unique candidates using their current persisted review state. */
export function ChangeReviewCard({
  project,
  changesets,
  onRefresh,
  onChangeSetUpdated,
  onOpenTarget,
  beforeConfirm,
}: Props) {
  const unique = new Map(changesets.map((cs) => [cs.id, cs]));
  for (const saved of project.studio.changesets || []) {
    if (unique.has(saved.id)) unique.set(saved.id, saved);
  }
  return (
    <div className="changeset-review-list">
      {[...unique.values()].map((cs) => (
        <ChangeSetEditor
          key={cs.id}
          project={project}
          changeset={cs}
          onRefresh={onRefresh}
          onChangeSetUpdated={onChangeSetUpdated}
          onOpenTarget={onOpenTarget}
          beforeConfirm={beforeConfirm}
        />
      ))}
    </div>
  );
}
