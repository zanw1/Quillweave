import { Select } from "../../components/Select";
import { t } from "../../lib/i18n";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Core } from "cytoscape";
import {
  GraphCanvas,
  fitGraph,
  layoutGraph,
  type Selection,
  type LayoutMode,
} from "./GraphCanvas";
import {
  IconSearch,
  IconZoomIn,
  IconZoomOut,
  IconFocus2,
  IconRefresh,
  IconGraph,
  IconList,
  IconArrowUpRight,
  IconX,
  IconAdjustmentsHorizontal,
  IconLayoutSidebar,
  IconLayoutSidebarRight,
  IconMaximize,
  IconMinimize,
  IconTypography,
  IconChevronRight,
  IconGitBranch,
  IconBook2,
} from "@tabler/icons-react";
import { api, message } from "../../lib/api";
import { Badge, Button, Empty, ErrorBox } from "../../components/ui";
import {
  entityKinds,
  type GraphEdge,
  type GraphNode,
  type KnowledgeGraph as GraphData,
  type Project,
} from "../../domain/types";
import "./graph.css";

const colors: Record<string, string> = {
  character: "#5b9cf6",
  faction: "#9b7cde",
  location: "#43b3a1",
  world: "#78ac62",
  item: "#e6b342",
  concept: "#bd80e5",
  chapter: "#f29a57",
  source: "#8c9cb3",
};
const labels: Record<string, string> = {
  ...entityKinds,
  chapter: "Chapter source",
  source: "Reference",
};

/** Filter the visible projection and retain one-hop neighbors for search matches. */
function projectGraph(
  graph: GraphData | null,
  kind: string,
  sources: boolean,
  isolated: boolean,
  query: string,
) {
  if (!graph) return { nodes: [] as GraphNode[], edges: [] as GraphEdge[] };
  let nodes = graph.nodes.filter((node) =>
    node.entity_id ? kind === "all" || node.kind === kind : sources,
  );
  let ids = new Set(nodes.map((node) => node.id));
  let edges = graph.edges.filter(
    (edge) => ids.has(edge.source) && ids.has(edge.target),
  );
  if (!isolated) {
    const connected = new Set(
      edges.flatMap((edge) => [edge.source, edge.target]),
    );
    nodes = nodes.filter((node) => connected.has(node.id));
  }
  if (query.trim()) {
    const needle = query.trim().toLocaleLowerCase();
    const matches = new Set(
      nodes
        .filter((node) =>
          `${node.label} ${node.description}`
            .toLocaleLowerCase()
            .includes(needle),
        )
        .map((node) => node.id),
    );
    const neighborhood = new Set(matches);
    for (const edge of edges) {
      if (matches.has(edge.source)) neighborhood.add(edge.target);
      if (matches.has(edge.target)) neighborhood.add(edge.source);
    }
    nodes = nodes.filter((node) => neighborhood.has(node.id));
  }
  ids = new Set(nodes.map((node) => node.id));
  edges = edges.filter((edge) => ids.has(edge.source) && ids.has(edge.target));
  return { nodes, edges };
}

/** Size nodes by distinct neighbors without double-counting parallel review states. */
function connectionCounts(nodes: GraphNode[], edges: GraphEdge[]) {
  const neighbors = new Map(nodes.map((node) => [node.id, new Set<string>()]));
  for (const edge of edges) {
    neighbors.get(edge.source)?.add(edge.target);
    neighbors.get(edge.target)?.add(edge.source);
  }
  return new Map([...neighbors].map(([id, adjacent]) => [id, adjacent.size]));
}

/** Organize entry navigation, the graph canvas, and evidence reader as view state. */
export function KnowledgeGraph({
  project,
  onEntity,
  onChapter,
}: {
  project: Project;
  onEntity: (id: string) => void;
  onChapter: (id: string) => void;
}) {
  const [graph, setGraph] = useState<GraphData | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [scope, setScope] = useState("all");
  const [pending, setPending] = useState(false);
  const [sources, setSources] = useState(true);
  const [isolated, setIsolated] = useState(false);
  const [kind, setKind] = useState("all");
  const [query, setQuery] = useState("");
  const [view, setView] = useState("graph");
  const [selection, setSelection] = useState<Selection>(null);
  const [directory, setDirectory] = useState(() => window.innerWidth >= 1280);
  const [inspecting, setInspecting] = useState(() => window.innerWidth >= 1100);
  const [expanded, setExpanded] = useState(false);
  const [showLabels, setShowLabels] = useState(true);
  const [layout, setLayout] = useState<LayoutMode>("network");
  const [zoom, setZoom] = useState(100);
  const canvas = useRef<Core | null>(null);
  const workspace = useRef<HTMLDivElement>(null);
  const expandButton = useRef<HTMLButtonElement>(null);
  /** Open the evidence reader when selecting an entry from navigation or the canvas. */
  const select = useCallback((value: Selection) => {
    setSelection(value);
    if (value) setInspecting(true);
  }, []);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    setSelection(null);
    setGraph(null);
    const through =
      scope === "all"
        ? Math.max(0, ...project.chapters.map((c) => c.number))
        : Number(scope);
    api<GraphData>("knowledge_graph", {
      project_id: project.id,
      through_chapter: through,
      include_pending: pending,
    })
      .then((value) => {
        if (active) setGraph(value);
      })
      .catch((e) => {
        if (active) setError(message(e));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [project, scope, pending]);

  useEffect(() => {
    if (!expanded) return;
    const previous = document.activeElement as HTMLElement | null;
    expandButton.current?.focus();
    /** Keep keyboard focus inside graph focus mode and return on Escape. */
    function keydown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        setExpanded(false);
      }
      if (event.key !== "Tab") return;
      const items = [
        ...(workspace.current?.querySelectorAll<HTMLElement>(
          'button:not(:disabled), input, select, summary, [tabindex="0"]',
        ) || []),
      ].filter((element) => element.getClientRects().length > 0);
      const first = items[0],
        last = items[items.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    }
    document.addEventListener("keydown", keydown);
    return () => {
      document.removeEventListener("keydown", keydown);
      previous?.focus();
    };
  }, [expanded]);

  const projection = useMemo(
    () => projectGraph(graph, kind, sources, isolated, query),
    [graph, kind, sources, isolated, query],
  );
  const connections = useMemo(
    () => connectionCounts(projection.nodes, projection.edges),
    [projection],
  );
  const groups = Object.entries(labels)
    .map(([id, label]) => ({
      id,
      label,
      nodes: projection.nodes.filter((node) => node.kind === id),
    }))
    .filter((group) => group.nodes.length);
  const node =
    selection?.kind === "node"
      ? projection.nodes.find((n) => n.id === selection.id)
      : undefined;
  const edge =
    selection?.kind === "edge"
      ? projection.edges.find((e) => e.id === selection.id)
      : undefined;
  const evidenceIds = node?.evidence_ids || edge?.evidence_ids || [];
  const evidenceCount = new Set(
    projection.edges.flatMap((item) => item.evidence_ids),
  ).size;
  const featured = [...projection.nodes]
    .filter((item) => item.entity_id)
    .sort(
      (a, b) =>
        (connections.get(b.id) || 0) - (connections.get(a.id) || 0) ||
        a.label.localeCompare(b.label),
    )
    .slice(0, 5);

  /** Control zoom and layout independently of entity and evidence identity. */
  function control(action: string) {
    const cy = canvas.current;
    if (!cy) return;
    if (action === "fit") fitGraph(cy);
    else if (action === "layout") layoutGraph(cy, layout);
    else
      cy.zoom({
        level: Math.min(
          3,
          Math.max(cy.minZoom(), cy.zoom() * (action === "in" ? 1.2 : 1 / 1.2)),
        ),
        renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 },
      });
  }
  /** Reset visibility filters to the current confirmed network. */
  function resetFilters() {
    setKind("all");
    setScope("all");
    setQuery("");
    setSources(true);
    setIsolated(false);
    setPending(false);
    setSelection(null);
  }

  return (
    <div
      ref={workspace}
      className={`knowledge-graph ${expanded ? "graph-expanded" : ""}`}
      role={expanded ? "dialog" : undefined}
      aria-modal={expanded || undefined}
      aria-label={expanded ? t("Knowledge graph focus mode") : undefined}
    >
      <div className="graph-toolbar">
        <div className="graph-toolbar-title">
          <IconGraph size={19} />
          <strong>{t("Story knowledge graph")}</strong>
          <span>
            {t("Nodes")} {projection.nodes.length}
          </span>
          <span>
            {t("Edges")} {projection.edges.length}
          </span>
        </div>
        <div className="graph-toolbar-actions">
          <button
            className={directory ? "active" : ""}
            aria-label={t("Toggle entry navigation")}
            aria-pressed={directory}
            title={t("Entry navigation")}
            onClick={() => setDirectory(!directory)}
          >
            <IconLayoutSidebar size={18} />
          </button>
          <button
            className={inspecting ? "active" : ""}
            aria-label={t("Toggle graph details")}
            aria-pressed={inspecting}
            title={t("Evidence details")}
            onClick={() => setInspecting(!inspecting)}
          >
            <IconLayoutSidebarRight size={18} />
          </button>
          <button
            ref={expandButton}
            aria-label={
              expanded
                ? t("Exit graph focus mode")
                : t("Enter graph focus mode")
            }
            title={expanded ? t("Exit focus · Esc") : t("Focus mode")}
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? <IconMinimize size={18} /> : <IconMaximize size={18} />}
          </button>
        </div>
      </div>
      <div className="graph-filter-row">
        <label className="search">
          <IconSearch size={16} />
          <input
            aria-label={t("Search graph")}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setSelection(null);
            }}
            placeholder={t("Find characters, locations, chapters…")}
          />
          {query && (
            <button
              aria-label={t("Clear graph search")}
              onClick={() => {
                setQuery("");
                setSelection(null);
              }}
            >
              <IconX size={14} />
            </button>
          )}
        </label>
        <Select
          aria-label={t("Graph entity type")}
          value={kind}
          onChange={(e) => {
            setKind(e.target.value);
            setSelection(null);
          }}
        >
          <option value="all">{t("All types")}</option>
          {Object.entries(entityKinds).map(([id, label]) => (
            <option key={id} value={id}>
              {t(label)}
            </option>
          ))}
        </Select>
        <Select
          aria-label={t("Graph chapter range")}
          value={scope}
          onChange={(e) => setScope(e.target.value)}
        >
          <option value="all">{t("All written chapters")}</option>
          <option value="0">{t("Reference sources only")}</option>
          {project.chapters.map((c) => (
            <option key={c.id} value={c.number}>
              {t("Through chapter")} {c.number}
            </option>
          ))}
        </Select>
        <details className="graph-filter-menu">
          <summary>
            <IconAdjustmentsHorizontal size={16} />
            {t("Filters")}
            {(pending || !sources || isolated) && (
              <span className="graph-filter-count">
                {Number(pending) + Number(!sources) + Number(isolated)}
              </span>
            )}
          </summary>
          <div className="graph-options">
            <label>
              <input
                type="checkbox"
                checked={sources}
                onChange={(e) => {
                  setSources(e.target.checked);
                  setSelection(null);
                }}
              />
              {t("Show source nodes")}
            </label>
            <label>
              <input
                type="checkbox"
                checked={pending}
                onChange={(e) => setPending(e.target.checked)}
              />
              {t("Show pending evidence")}
            </label>
            <label>
              <input
                type="checkbox"
                checked={isolated}
                onChange={(e) => {
                  setIsolated(e.target.checked);
                  setSelection(null);
                }}
              />
              {t("Show entries without evidence")}
            </label>
            <p>{t("Dashed relationships await author review.")}</p>
            <button onClick={resetFilters}>{t("Reset filters")}</button>
          </div>
        </details>
        <div className="segmented">
          <button
            aria-label={t("Graph view")}
            aria-pressed={view === "graph"}
            className={view === "graph" ? "active" : ""}
            onClick={() => setView("graph")}
          >
            <IconGraph size={18} />
          </button>
          <button
            aria-label={t("Graph list view")}
            aria-pressed={view === "list"}
            className={view === "list" ? "active" : ""}
            onClick={() => setView("list")}
          >
            <IconList size={18} />
          </button>
        </div>
      </div>
      {error && <ErrorBox>{error}</ErrorBox>}
      <div
        className={`graph-workspace ${directory ? "with-directory" : ""} ${inspecting ? "with-inspector" : ""}`}
      >
        {directory && (
          <nav
            className="graph-directory"
            aria-label={t("Graph entry navigation")}
          >
            <header>
              <span>{t("Knowledge entries")}</span>
              <small>{projection.nodes.length}</small>
            </header>
            {groups.map((group) => (
              <details key={group.id} open>
                <summary>
                  <IconChevronRight size={13} />
                  <i style={{ background: colors[group.id] }} />
                  <strong>{t(group.label)}</strong>
                  <span>{group.nodes.length}</span>
                </summary>
                <div>
                  {group.nodes.map((item) => (
                    <button
                      key={item.id}
                      className={selection?.id === item.id ? "active" : ""}
                      aria-label={t("Locate {0}", item.label)}
                      title={item.label}
                      onClick={() => select({ kind: "node", id: item.id })}
                    >
                      <span>{item.label}</span>
                      <small>{connections.get(item.id) || 0}</small>
                    </button>
                  ))}
                </div>
              </details>
            ))}
            {!groups.length && <p>{t("No entries match the filters")}</p>}
            <footer>
              {t(
                "Browse by type and select an entry to locate its connections.",
              )}
            </footer>
          </nav>
        )}
        <section className="graph-stage" aria-label={t("Graph canvas")}>
          {loading ? (
            <div className="loading">
              {t("Building the story connections…")}
            </div>
          ) : !projection.nodes.length ? (
            <div className="graph-empty">
              <Empty
                title={t("No graph nodes match the filters")}
                detail={t(
                  "Broaden the filters, or compile and review chapter relationships.",
                )}
              />
              <Button onClick={resetFilters}>{t("Reset filters")}</Button>
            </div>
          ) : view === "graph" ? (
            <GraphCanvas
              projectId={project.id}
              nodes={projection.nodes}
              edges={projection.edges}
              selection={selection}
              onSelect={select}
              coreRef={canvas}
              layout={layout}
              showLabels={showLabels}
              onZoom={setZoom}
            />
          ) : (
            <div className="graph-accessible-list">
              {projection.nodes.map((item) => (
                <button
                  key={item.id}
                  className={selection?.id === item.id ? "active" : ""}
                  onClick={() => select({ kind: "node", id: item.id })}
                >
                  <span
                    className="graph-node-dot"
                    style={{ background: colors[item.kind] }}
                  />
                  <strong>{item.label}</strong>
                  <span>{t(labels[item.kind])}</span>
                  <small>
                    {item.evidence_ids.length} {t("evidence records")}
                  </small>
                  <IconChevronRight size={15} />
                </button>
              ))}
            </div>
          )}
          {view === "graph" && projection.nodes.length > 0 && !loading && (
            <>
              <div className="graph-view-controls">
                <Select
                  aria-label={t("Graph layout")}
                  value={layout}
                  onChange={(e) => setLayout(e.target.value as LayoutMode)}
                >
                  <option value="network">{t("Relationship layout")}</option>
                  <option value="circle">{t("Circle layout")}</option>
                </Select>
                <button
                  aria-label={t("Show node labels")}
                  aria-pressed={showLabels}
                  className={showLabels ? "active" : ""}
                  title={t("Node labels")}
                  onClick={() => setShowLabels(!showLabels)}
                >
                  <IconTypography size={17} />
                </button>
              </div>
              <div className="graph-controls">
                <button
                  aria-label={t("Zoom in on graph")}
                  title={t("Zoom in")}
                  onClick={() => control("in")}
                >
                  <IconZoomIn size={19} />
                </button>
                <output aria-label={t("Graph zoom level")}>{zoom}%</output>
                <button
                  aria-label={t("Zoom out of graph")}
                  title={t("Zoom out")}
                  onClick={() => control("out")}
                >
                  <IconZoomOut size={19} />
                </button>
                <span />
                <button
                  aria-label={t("Fit to canvas")}
                  title={t("Fit to canvas")}
                  onClick={() => control("fit")}
                >
                  <IconFocus2 size={19} />
                </button>
                <button
                  aria-label={t("Rearrange graph")}
                  title={t("Rearrange")}
                  onClick={() => control("layout")}
                >
                  <IconRefresh size={18} />
                </button>
              </div>
              <div className="graph-legend">
                <strong>{t("Node types")}</strong>
                {groups.map((group) => (
                  <span key={group.id}>
                    <i style={{ background: colors[group.id] }} />
                    {t(group.label)}
                    <small>{group.nodes.length}</small>
                  </span>
                ))}
                <p>{t("Larger nodes have more connections")}</p>
              </div>
              {selection && (
                <button
                  className="graph-clear-focus"
                  onClick={() => select(null)}
                >
                  <IconX size={13} />
                  {t("Clear selection")}
                </button>
              )}
            </>
          )}
          <div className="graph-stage-footer">
            <span>
              {t("Drag to move · Scroll to zoom · Select for evidence")}
            </span>
            <span>
              <IconGitBranch size={13} />
              {t("Solid = confirmed / Dashed = pending")}
            </span>
          </div>
        </section>
        {inspecting && (
          <aside
            className="graph-inspector"
            aria-label={t("Graph evidence details")}
          >
            <header>
              <IconBook2 size={16} />
              <h3>
                {node
                  ? t("Entry details")
                  : edge
                    ? t("Relationship details")
                    : t("Graph overview")}
              </h3>
              <button
                className="icon-button"
                aria-label={t("Collapse graph details")}
                onClick={() => setInspecting(false)}
              >
                <IconX size={17} />
              </button>
            </header>
            {!node && !edge ? (
              <div className="graph-overview">
                <p className="graph-kicker">{t("STORY CONNECTIONS")}</p>
                <h2>{t("Follow the connections in your story.")}</h2>
                <p>
                  {t(
                    "Move from a character to a place or from a choice to its source. Select a node to explore its connections.",
                  )}
                </p>
                <div className="graph-stat-grid">
                  <div>
                    <strong>
                      {projection.nodes.filter((item) => item.entity_id).length}
                    </strong>
                    <span>{t("Story entries")}</span>
                  </div>
                  <div>
                    <strong>{evidenceCount}</strong>
                    <span>{t("Source evidence")}</span>
                  </div>
                </div>
                <h4>{t("Start with these entries")}</h4>
                {featured.map((item) => (
                  <button
                    className="graph-featured"
                    key={item.id}
                    onClick={() => select({ kind: "node", id: item.id })}
                  >
                    <i style={{ background: colors[item.kind] }} />
                    <span>
                      <strong>{item.label}</strong>
                      <small>
                        {t(labels[item.kind])} {t("· Neighbors")}{" "}
                        {connections.get(item.id) || 0}
                      </small>
                    </span>
                    <IconArrowUpRight size={15} />
                  </button>
                ))}
                <div className="graph-reading-note">
                  <IconFocus2 size={17} />
                  <p>
                    {t("Select a node to highlight its neighbors.")}
                    <br />
                    {t("Select empty space to return to the full graph.")}
                  </p>
                </div>
              </div>
            ) : (
              <>
                <div className="graph-detail-heading">
                  <h2>{node?.label || t("Relationship evidence")}</h2>
                  <Badge tone={edge?.status === "pending" ? "amber" : "blue"}>
                    {node
                      ? t(labels[node.kind])
                      : edge?.status === "pending"
                        ? t("Pending review")
                        : t("Confirmed")}
                  </Badge>
                  <p>
                    {(node?.source_id
                      ? t(node.description)
                      : node?.description) ||
                      `${projection.nodes.find((n) => n.id === edge?.source)?.label} — ${projection.nodes.find((n) => n.id === edge?.target)?.label}`}
                  </p>
                  {node?.entity_id && (
                    <Button onClick={() => onEntity(node.entity_id!)}>
                      {t("Open Wiki entry")}
                      <IconArrowUpRight size={14} />
                    </Button>
                  )}
                  {node?.source_id &&
                    project.chapters.some((c) => c.id === node.source_id) && (
                      <Button onClick={() => onChapter(node.source_id!)}>
                        {t("Open chapter")}
                        <IconArrowUpRight size={14} />
                      </Button>
                    )}
                </div>
                {node && (
                  <details className="graph-connected" key={node.id}>
                    <summary>
                      <IconChevronRight size={13} />
                      <span>{t("Neighboring relationships")}</span>
                      <small>
                        {
                          projection.edges.filter(
                            (edge) =>
                              edge.source === node.id ||
                              edge.target === node.id,
                          ).length
                        }
                      </small>
                    </summary>
                    {projection.edges
                      .filter(
                        (e) => e.source === node.id || e.target === node.id,
                      )
                      .map((edge) => (
                        <button
                          key={edge.id}
                          onClick={() => select({ kind: "edge", id: edge.id })}
                        >
                          {
                            projection.nodes.find(
                              (n) =>
                                n.id ===
                                (edge.source === node.id
                                  ? edge.target
                                  : edge.source),
                            )?.label
                          }
                          <small>{t(edge.label)}</small>
                        </button>
                      ))}
                  </details>
                )}
                <div className="graph-evidence-list">
                  <h4>{t("Source evidence")}</h4>
                  {evidenceIds.map((id) => {
                    const fact = project.facts.find((f) => f.id === id);
                    return fact ? (
                      <article key={id}>
                        <Badge
                          tone={fact.status === "confirmed" ? "green" : "amber"}
                        >
                          {fact.chapter
                            ? t("Chapter {0}", fact.chapter)
                            : t("Sources")}{" "}
                          ·{" "}
                          {fact.status === "confirmed"
                            ? t("Confirmed")
                            : t("Pending review")}
                        </Badge>
                        <strong>
                          {fact.entity} · {fact.claim}
                        </strong>
                        <blockquote>{fact.quote}</blockquote>
                        <small>
                          {fact.epistemic === "inferred"
                            ? t("Inferred")
                            : t("Explicit")}
                        </small>
                        {project.chapters.some(
                          (c) => c.id === fact.source_id,
                        ) && (
                          <button
                            className="text-button"
                            onClick={() => onChapter(fact.source_id)}
                          >
                            {t("Back to source")}
                            <IconArrowUpRight size={13} />
                          </button>
                        )}
                      </article>
                    ) : null;
                  })}
                </div>
              </>
            )}
          </aside>
        )}
      </div>
      <footer className="graph-footnote">
        <span>{t("Trace each connection back to its source.")}</span>
        <span>
          {t("Stale evidence excluded")} {graph?.omitted_stale || 0}{" "}
          {t("records · Unresolved relationships")}{" "}
          {graph?.unresolved_relations || 0} {t("records")}
        </span>
      </footer>
    </div>
  );
}
