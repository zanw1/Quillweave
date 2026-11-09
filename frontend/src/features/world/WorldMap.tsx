import { useEffect, useId, useState } from "react";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import {
  Badge,
  Button,
  Empty,
  ErrorBox,
  Modal,
  RequiredLabel,
} from "../../components/ui";
import { Select } from "../../components/Select";
import { ChangeReviewCard } from "../assistant/ChangeReviewCard";
import type {
  ChangeOperation,
  ChangeSet,
  WorldLocation,
  WorldRoute,
} from "../../domain/studio";
import type { Project } from "../../domain/types";
import { publishSetupDraft } from "../studio/setupDrafts";
import "./world-map.css";

interface Props {
  project: Project;
  onProject: (project: Project) => void;
  onFocus?: (id: string) => void;
  onChapter: (id: string) => void;
  onOpenTarget?: (kind: string, id: string) => void;
  focusId?: string;
}

/** Select narrative references using visible titles rather than requiring the author to enter IDs. */
function ReferenceChoices({
  title,
  items,
  selected,
  onChange,
}: {
  title: string;
  items: { id: string; title: string }[];
  selected: string[];
  onChange: (ids: string[]) => void;
}) {
  return (
    <details className="map-reference-choices">
      <summary>
        {title} · {selected.length}
      </summary>
      {items.map((item) => (
        <label className="checkbox-label" key={item.id}>
          <input
            type="checkbox"
            checked={selected.includes(item.id)}
            onChange={(event) =>
              onChange(
                event.target.checked
                  ? [...selected, item.id]
                  : selected.filter((id) => id !== item.id),
              )
            }
          />
          {item.title}
        </label>
      ))}
      {!items.length && <p className="muted">{t("No entries yet")}</p>}
    </details>
  );
}

/** Edit the same durable map used by planning and model tools through the shared review service. */
export function WorldMap({
  project,
  onProject,
  onFocus,
  onChapter,
  onOpenTarget,
  focusId,
}: Props) {
  const locations = project.studio.locations || [];
  const routes = project.studio.routes || [];
  const [location, setLocation] = useState<WorldLocation | null>(null);
  const [route, setRoute] = useState<WorldRoute | null>(null);
  const [selected, setSelected] = useState(focusId || "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [receipt, setReceipt] = useState<ChangeSet | null>(null);
  useEffect(() => {
    publishSetupDraft(
      "location",
      location
        ? {
            label: location.name || t("Location"),
            text: JSON.stringify(location),
          }
        : null,
    );
    return () => publishSetupDraft("location", null);
  }, [location]);
  useEffect(() => {
    publishSetupDraft(
      "route",
      route
        ? { label: route.transport || t("Travel route"), text: JSON.stringify(route) }
        : null,
    );
    return () => publishSetupDraft("route", null);
  }, [route]);
  const marker = useId().replaceAll(":", "");
  const focused = locations.find((item) => item.id === selected);
  const indexed = new Map(locations.map((item) => [item.id, item]));
  const pending = (project.studio.changesets || []).filter(
    (cs) =>
      (cs.status === "staged" || cs.status === "conflicted") &&
      cs.operations.some(
        (op) => op.target_kind === "location" || op.target_kind === "route",
      ),
  );
  const xValues = locations.map((item) => item.x);
  const yValues = locations.map((item) => item.y);
  const left = Math.min(0, ...xValues) - 70;
  const top = Math.min(0, ...yValues) - 60;
  const width = Math.max(600, ...xValues) - left + 80;
  const height = Math.max(360, ...yValues) - top + 80;

  useEffect(() => {
    if (focusId) {
      setSelected(focusId);
      document
        .getElementById(`route-${focusId}`)
        ?.scrollIntoView({ block: "nearest" });
    }
  }, [focusId]);

  /** Focus a place for inspection and for the next shared conversation turn. */
  function focus(id: string) {
    setSelected(id);
    onFocus?.(id);
  }

  /** Initialize a new place, optionally linked to an existing world entry. */
  function addLocation(entityId = "") {
    const entity = project.entities.find((item) => item.id === entityId);
    setLocation({
      id: crypto.randomUUID(),
      name: entity?.name || "",
      kind: "place",
      region_id: "",
      description: entity?.description || "",
      x: 100 + (locations.length % 4) * 180,
      y: 90 + Math.floor(locations.length / 4) * 140,
      entity_ids: entity ? [entity.id] : [],
      plan_ids: [],
      chapter_ids: [],
    });
    setError("");
  }

  /** Initialize a route between saved places with a visible time constraint. */
  function addRoute() {
    setRoute({
      id: crypto.randomUUID(),
      from_id: locations[0]?.id || "",
      to_id: locations[1]?.id || "",
      transport: "",
      travel_hours: 1,
      bidirectional: true,
      description: "",
    });
    setError("");
  }

  /** Prepare an author edit as a persisted, validated preview before any map record changes. */
  async function stage(operations: ChangeOperation[], summary: string) {
    setBusy(true);
    setError("");
    try {
      const cs = await api<ChangeSet>("stage_changeset", {
        project_id: project.id,
        changeset: { source: "author", summary, operations },
      });
      const result = await api<{ project: Project }>("preview_changeset", {
        project_id: project.id,
        changeset_id: cs.id,
      });
      onProject(result.project);
      setLocation(null);
      setRoute(null);
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy(false);
    }
  }

  /** Build the one-operation edit consumed by the shared preview contract. */
  function operation(
    kind: "location" | "route",
    value: WorldLocation | WorldRoute,
    remove = false,
  ): ChangeOperation {
    return {
      id: crypto.randomUUID(),
      target_kind: kind,
      target_id: value.id,
      op_type: remove ? "delete" : "upsert",
      title:
        kind === "location" ? (value as WorldLocation).name : t("Travel route"),
      summary: value.description,
      after_value: value,
    };
  }

  /** Include affected route removals and region detachments in the same author-reviewed deletion. */
  function removeLocation(value: WorldLocation) {
    const operations = routes
      .filter((item) => item.from_id === value.id || item.to_id === value.id)
      .map((item) => operation("route", item, true));
    for (const child of locations.filter((item) => item.region_id === value.id))
      operations.push(operation("location", { ...child, region_id: "" }));
    operations.push(operation("location", value, true));
    void stage(operations, t("Remove map location {0}", value.name));
  }

  /** Reload map records and review receipts after confirmation or undo. */
  async function refresh() {
    onProject(await api<Project>("get_project", { project_id: project.id }));
  }

  /** Open related plans or chapters through the workspace while keeping map targets focused locally. */
  function openReviewedTarget(kind: string, id: string) {
    if ((kind === "chapter" || kind === "plan") && onOpenTarget)
      onOpenTarget(kind, id);
    else if (kind === "chapter") onChapter(id);
    else focus(id);
  }

  return (
    <section className="world-map">
      <header className="section-title">
        <div>
          <h1>{t("World map")}</h1>
          <p>
            {t("Places, regions and travel constraints linked to your story.")}
          </p>
        </div>
        <div className="studio-actions">
          <Button onClick={() => addLocation()}>{t("Add location")}</Button>
          <Button disabled={locations.length < 2} onClick={addRoute}>
            {t("Add route")}
          </Button>
        </div>
      </header>
      {error && !location && !route && <ErrorBox>{error}</ErrorBox>}
      {locations.length ? (
        <svg
          className="world-map-canvas"
          viewBox={`${left} ${top} ${width} ${height}`}
          role="img"
          aria-label={t("World map")}
        >
          <defs>
            <marker
              id={marker}
              markerWidth="8"
              markerHeight="8"
              refX="7"
              refY="3"
              orient="auto"
            >
              <path d="M0,0 L0,6 L8,3 z" fill="currentColor" />
            </marker>
          </defs>
          {routes.map((item) => {
            const from = indexed.get(item.from_id);
            const to = indexed.get(item.to_id);
            if (!from || !to) return null;
            return (
              <g key={item.id} className="world-map-route">
                <line
                  x1={from.x}
                  y1={from.y}
                  x2={to.x}
                  y2={to.y}
                  markerEnd={item.bidirectional ? undefined : `url(#${marker})`}
                />
                <text x={(from.x + to.x) / 2} y={(from.y + to.y) / 2 - 10}>
                  {item.travel_hours} {t("hours")}
                </text>
              </g>
            );
          })}
          {locations.map((item) => (
            <g
              key={item.id}
              className={`world-map-node ${item.kind} ${selected === item.id ? "selected" : ""}`}
              role="button"
              tabIndex={0}
              aria-label={item.name}
              onClick={() => focus(item.id)}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  focus(item.id);
                }
              }}
            >
              <circle
                cx={item.x}
                cy={item.y}
                r={item.kind === "region" ? 26 : 18}
              />
              <text x={item.x} y={item.y + 43}>
                {item.name}
              </text>
            </g>
          ))}
        </svg>
      ) : (
        <Empty
          title={t("Add the first place in your world")}
          detail={t(
            "Discuss locations with your creative partner or prepare a location here.",
          )}
        />
      )}
      {focused && (
        <section className="map-location-detail">
          <h2>{focused.name}</h2>
          <p className="preserve">{focused.description}</p>
          {!!focused.region_id && (
            <p>
              {t("Region")} · {indexed.get(focused.region_id)?.name}
            </p>
          )}
          <div className="studio-actions">
            <Button onClick={() => setLocation({ ...focused })}>
              {t("Edit")}
            </Button>
            {(focused.chapter_ids || []).map((id) => (
              <Button key={id} onClick={() => onChapter(id)}>
                {project.chapters.find((item) => item.id === id)?.title || id}
              </Button>
            ))}
          </div>
        </section>
      )}
      <div className="map-records">
        <section>
          <h2>{t("Locations")}</h2>
          {locations.map((item) => (
            <article key={item.id}>
              <button className="text-button" onClick={() => focus(item.id)}>
                {item.name}
              </button>
              <Badge>{t(item.kind === "region" ? "Region" : "Location")}</Badge>
              <Button disabled={busy} onClick={() => setLocation({ ...item })}>
                {t("Edit")}
              </Button>
              <Button disabled={busy} onClick={() => removeLocation(item)}>
                {t("Delete")}
              </Button>
            </article>
          ))}
        </section>
        <section>
          <h2>{t("Travel routes")}</h2>
          {routes.map((item) => (
            <article key={item.id} id={`route-${item.id}`}>
              <span>
                {indexed.get(item.from_id)?.name}{" "}
                {item.bidirectional ? "↔" : "→"}{" "}
                {indexed.get(item.to_id)?.name}
                <small>
                  {item.transport} · {item.travel_hours} {t("hours")}
                </small>
              </span>
              <Button disabled={busy} onClick={() => setRoute({ ...item })}>
                {t("Edit")}
              </Button>
              <Button
                disabled={busy}
                onClick={() =>
                  void stage(
                    [operation("route", item, true)],
                    t("Remove travel route"),
                  )
                }
              >
                {t("Delete")}
              </Button>
            </article>
          ))}
        </section>
      </div>
      {project.entities.some(
        (item) =>
          item.kind === "location" &&
          !locations.some((place) =>
            (place.entity_ids || []).includes(item.id),
          ),
      ) && (
        <details>
          <summary>{t("Use existing location entries")}</summary>
          {project.entities
            .filter(
              (item) =>
                item.kind === "location" &&
                !locations.some((place) =>
                  (place.entity_ids || []).includes(item.id),
                ),
            )
            .map((item) => (
              <Button key={item.id} onClick={() => addLocation(item.id)}>
                {item.name}
              </Button>
            ))}
        </details>
      )}
      {pending.length > 0 && (
        <section>
          <h2>{t("Pending changes")}</h2>
          <ChangeReviewCard
            project={project}
            changesets={pending}
            onRefresh={refresh}
            onChangeSetUpdated={(cs) => {
              if (cs.status !== "staged") setReceipt(cs);
            }}
            onOpenTarget={openReviewedTarget}
          />
        </section>
      )}
      {receipt && !pending.some((cs) => cs.id === receipt.id) && (
        <ChangeReviewCard
          project={project}
          changesets={[receipt]}
          onRefresh={refresh}
          onChangeSetUpdated={setReceipt}
          onOpenTarget={openReviewedTarget}
        />
      )}
      {location && (
        <Modal title={t("Location")} wide onClose={() => setLocation(null)}>
          <form
            className="modal-content studio-form"
            onSubmit={(event) => {
              event.preventDefault();
              void stage([operation("location", location)], location.name);
            }}
          >
            <label>
              <RequiredLabel>{t("Name")}</RequiredLabel>
              <input
                required
                maxLength={120}
                value={location.name}
                onChange={(event) =>
                  setLocation({ ...location, name: event.target.value })
                }
              />
            </label>
            <div className="form-grid">
              <label>
                {t("Type")}
                <Select
                  value={location.kind}
                  onChange={(event) =>
                    setLocation({
                      ...location,
                      kind: event.target.value as WorldLocation["kind"],
                    })
                  }
                >
                  <option value="place">{t("Location")}</option>
                  <option value="region">{t("Region")}</option>
                </Select>
              </label>
              <label>
                {t("Region")}
                <Select
                  value={location.region_id}
                  onChange={(event) =>
                    setLocation({ ...location, region_id: event.target.value })
                  }
                >
                  <option value="">{t("Whole world")}</option>
                  {locations
                    .filter(
                      (item) =>
                        item.kind === "region" && item.id !== location.id,
                    )
                    .map((item) => (
                      <option key={item.id} value={item.id}>
                        {item.name}
                      </option>
                    ))}
                </Select>
              </label>
              <label>
                {t("Horizontal position")}
                <input
                  type="number"
                  min={-10000}
                  max={10000}
                  value={location.x}
                  onChange={(event) =>
                    setLocation({ ...location, x: Number(event.target.value) })
                  }
                />
              </label>
              <label>
                {t("Vertical position")}
                <input
                  type="number"
                  min={-10000}
                  max={10000}
                  value={location.y}
                  onChange={(event) =>
                    setLocation({ ...location, y: Number(event.target.value) })
                  }
                />
              </label>
            </div>
            <label>
              {t("Description")}
              <textarea
                rows={5}
                maxLength={12000}
                value={location.description}
                onChange={(event) =>
                  setLocation({ ...location, description: event.target.value })
                }
              />
            </label>
            <ReferenceChoices
              title={t("Linked world entries")}
              items={project.entities.map((item) => ({
                id: item.id,
                title: item.name,
              }))}
              selected={location.entity_ids || []}
              onChange={(ids) => setLocation({ ...location, entity_ids: ids })}
            />
            <ReferenceChoices
              title={t("Linked plans")}
              items={project.studio.plans}
              selected={location.plan_ids || []}
              onChange={(ids) => setLocation({ ...location, plan_ids: ids })}
            />
            <ReferenceChoices
              title={t("Linked chapters")}
              items={project.chapters}
              selected={location.chapter_ids || []}
              onChange={(ids) => setLocation({ ...location, chapter_ids: ids })}
            />
            {error && <ErrorBox>{error}</ErrorBox>}
            <Button primary type="submit" disabled={busy}>
              {t("Preview changes")}
            </Button>
          </form>
        </Modal>
      )}
      {route && (
        <Modal title={t("Travel route")} onClose={() => setRoute(null)}>
          <form
            className="modal-content studio-form"
            onSubmit={(event) => {
              event.preventDefault();
              void stage([operation("route", route)], t("Travel route"));
            }}
          >
            <label>
              {t("From location")}
              <Select
                value={route.from_id}
                onChange={(event) =>
                  setRoute({ ...route, from_id: event.target.value })
                }
              >
                {locations.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
              </Select>
            </label>
            <label>
              {t("To location")}
              <Select
                value={route.to_id}
                onChange={(event) =>
                  setRoute({ ...route, to_id: event.target.value })
                }
              >
                {locations.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
              </Select>
            </label>
            <label>
              {t("Transport")}
              <input
                maxLength={120}
                value={route.transport}
                onChange={(event) =>
                  setRoute({ ...route, transport: event.target.value })
                }
              />
            </label>
            <label>
              <RequiredLabel>{t("Travel hours")}</RequiredLabel>
              <input
                type="number"
                min={0}
                max={87600}
                step="any"
                required
                value={route.travel_hours}
                onChange={(event) =>
                  setRoute({
                    ...route,
                    travel_hours: Number(event.target.value),
                  })
                }
              />
            </label>
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={route.bidirectional}
                onChange={(event) =>
                  setRoute({ ...route, bidirectional: event.target.checked })
                }
              />
              {t("Two-way route")}
            </label>
            <label>
              {t("Description")}
              <textarea
                value={route.description}
                onChange={(event) =>
                  setRoute({ ...route, description: event.target.value })
                }
              />
            </label>
            {error && <ErrorBox>{error}</ErrorBox>}
            <Button primary type="submit" disabled={busy}>
              {t("Preview changes")}
            </Button>
          </form>
        </Modal>
      )}
    </section>
  );
}
