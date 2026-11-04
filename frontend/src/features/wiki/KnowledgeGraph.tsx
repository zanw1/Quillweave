import { t } from "../../lib/i18n";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import cytoscape, {
  type Core,
  type NodeSingular,
  type ElementDefinition,
  type StylesheetStyle,
} from "cytoscape";
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
type Selection = { kind: "node" | "edge"; id: string } | null;
type LayoutMode = "network" | "circle";

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
          <span>{t("Nodes")}{" "}{projection.nodes.length}</span>
          <span>{t("Edges")}{" "}{projection.edges.length}</span>
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
              expanded ? t("Exit graph focus mode") : t("Enter graph focus mode")
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
        <select
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
        </select>
        <select
          aria-label={t("Graph chapter range")}
          value={scope}
          onChange={(e) => setScope(e.target.value)}
        >
          <option value="all">{t("All written chapters")}</option>
          <option value="0">{t("Reference sources only")}</option>
          {project.chapters.map((c) => (
            <option key={c.id} value={c.number}>
              {t("Through chapter")}{" "}{c.number}
            </option>
          ))}
        </select>
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
          <nav className="graph-directory" aria-label={t("Graph entry navigation")}>
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
              {t("Browse by type and select an entry to locate its connections.")}
            </footer>
          </nav>
        )}
        <section className="graph-stage" aria-label={t("Graph canvas")}>
          {loading ? (
            <div className="loading">{t("Building the story connections…")}</div>
          ) : !projection.nodes.length ? (
            <div className="graph-empty">
              <Empty
                title={t("No graph nodes match the filters")}
                detail={t("Broaden the filters, or compile and review chapter relationships.")}
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
                  <small>{item.evidence_ids.length}{" "}{t("evidence records")}</small>
                  <IconChevronRight size={15} />
                </button>
              ))}
            </div>
          )}
          {view === "graph" && projection.nodes.length > 0 && !loading && (
            <>
              <div className="graph-view-controls">
                <select
                  aria-label={t("Graph layout")}
                  value={layout}
                  onChange={(e) => setLayout(e.target.value as LayoutMode)}
                >
                  <option value="network">{t("Relationship layout")}</option>
                  <option value="circle">{t("Circle layout")}</option>
                </select>
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
            <span>{t("Drag to move · Scroll to zoom · Select for evidence")}</span>
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
                  {t("Move from a character to a place or from a choice to its source. Select a node to explore its connections.")}
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
                        {t(labels[item.kind])}{" "}{t("· Neighbors")}{" "}
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
                    {(node?.source_id ? t(node.description) : node?.description) ||
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
                          {fact.chapter ? t("Chapter {0}", fact.chapter) : t("Sources")}{" "}
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
          {t("Stale evidence excluded")}{" "}{graph?.omitted_stale || 0}{" "}{t("records · Unresolved relationships")}{" "}{graph?.unresolved_relations || 0}{" "}{t("records")}
        </span>
      </footer>
    </div>
  );
}

/** Create independent canvas data with connection count encoded as visual size. */
function graphElements(
  nodes: GraphNode[],
  edges: GraphEdge[],
): ElementDefinition[] {
  const counts = connectionCounts(nodes, edges);
  return [
    ...nodes.map((node, index) => {
      const degree = counts.get(node.id) || 0;
      const size =
        (node.source_id ? 23 : 30) + Math.min(24, Math.sqrt(degree) * 9);
      return {
        data: {
          ...node,
          color: colors[node.kind] || "#8c9cb3",
          size,
          degree,
          canvasLabel: node.source_id
            ? node.label.replace(
                /^(?:Chapter\s*|\u7b2c\s*)(\d+)\s*(?:\u7ae0)?\s*[—·-]?\s*/i,
                "$1 · ",
              )
            : node.label,
          labelAlign: "right",
          displaySize: size,
          fontSize: 12,
          labelMargin: 7,
          labelWidth: 150,
          focusLabelWidth: 240,
          outlineWidth: 1.5,
          borderWidth: 2,
          haloPadding: 7,
        },
        position: {
          x: Math.cos(index * 2.39996) * Math.sqrt(index + 1) * 70,
          y: Math.sin(index * 2.39996) * Math.sqrt(index + 1) * 70,
        },
      };
    }),
    ...edges.map((edge) => ({
      data: {
        ...edge,
        weight: Math.min(3.4, 1 + Math.sqrt(edge.evidence_ids.length) * 0.55),
        displayWeight: 1.4,
        neighborWeight: 2,
        focusWeight: 3,
        fontSize: 10,
      },
    })),
  ];
}

/** Render nodes and edges with draggable layout and clean up observers on unmount. */
function GraphCanvas({
  projectId,
  nodes,
  edges,
  selection,
  onSelect,
  coreRef,
  layout,
  showLabels,
  onZoom,
}: {
  projectId: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
  selection: Selection;
  onSelect: (value: Selection) => void;
  coreRef: React.MutableRefObject<Core | null>;
  layout: LayoutMode;
  showLabels: boolean;
  onZoom: (value: number) => void;
}) {
  const container = useRef<HTMLDivElement>(null);
  const positions = useRef<Record<string, { x: number; y: number }>>({});
  const previousLayout = useRef<LayoutMode | null>(null);
  useEffect(() => {
    if (!container.current) return;
    positions.current = {};
    previousLayout.current = null;
    try {
      const saved = JSON.parse(
        sessionStorage.getItem(`quillweave-graph-v4-${projectId}`) || "{}",
      );
      if (saved.layout === "network" || saved.layout === "circle") {
        previousLayout.current = saved.layout;
      }
      for (const [id, point] of Object.entries(saved.positions || {})) {
        const value = point as { x: number; y: number };
        if (value && Number.isFinite(value.x) && Number.isFinite(value.y))
          positions.current[id] = value;
      }
    } catch {
      /* Recompute layout when the local view cache is unavailable. */
    }
    const cy = cytoscape({
      container: container.current,
      style: graphStyles(),
      minZoom: 0.01,
      maxZoom: 3,
      boxSelectionEnabled: false,
      autounselectify: true,
    });
    coreRef.current = cy;
    cy.on("tap", "node", (event) =>
      onSelect({ kind: "node", id: event.target.id() }),
    );
    cy.on("tap", "edge", (event) =>
      onSelect({ kind: "edge", id: event.target.id() }),
    );
    cy.on("tap", (event) => {
      if (event.target === cy) onSelect(null);
    });
    cy.on("mouseover", "node, edge", (event) => {
      event.target.addClass("hovered");
      if (container.current) container.current.style.cursor = "pointer";
    });
    cy.on("mouseout", "node, edge", (event) => {
      event.target.removeClass("hovered");
      if (container.current) container.current.style.cursor = "grab";
    });
    cy.on("zoom", () => {
      updateGraphScale(cy);
      onZoom(Math.round(cy.zoom() * 100));
    });
    cy.on("free", "node", (event) => {
      positions.current[event.target.id()] = { ...event.target.position() };
      avoidLabelOverlap(cy);
    });
    const observer = new ResizeObserver(() => {
      cy.resize();
      fitGraph(cy);
    });
    observer.observe(container.current);
    return () => {
      cy.nodes().forEach((item) => {
        positions.current[item.id()] = { ...item.position() };
      });
      try {
        sessionStorage.setItem(
          `quillweave-graph-v4-${projectId}`,
          JSON.stringify({
            positions: positions.current,
            layout: previousLayout.current,
          }),
        );
      } catch {
        /* Continue writing when the view cache cannot be saved. */
      }
      observer.disconnect();
      cy.destroy();
      coreRef.current = null;
    };
  }, [projectId, coreRef, onSelect, onZoom]);
  useEffect(() => {
    const cy = coreRef.current;
    if (!cy) return;
    cy.nodes().forEach((item) => {
      positions.current[item.id()] = { ...item.position() };
    });
    const items = graphElements(nodes, edges);
    const ids = new Set(items.map((item) => String(item.data.id)));
    const cached = nodes.every((node) => positions.current[node.id]);
    cy.batch(() => {
      cy.elements()
        .filter((element) => !ids.has(element.id()))
        .remove();
      for (const item of items) {
        const existing = cy.getElementById(String(item.data.id));
        if (existing.length) existing.data(item.data);
        else
          cy.add({
            ...item,
            position: positions.current[String(item.data.id)] || item.position,
          });
      }
    });
    if (
      !cached ||
      (previousLayout.current !== null && previousLayout.current !== layout)
    )
      layoutGraph(cy, layout);
    else fitGraph(cy);
    updateGraphScale(cy);
    previousLayout.current = layout;
  }, [nodes, edges, layout, coreRef]);
  useEffect(() => {
    const cy = coreRef.current;
    if (!cy) return;
    cy.batch(() => {
      cy.elements().removeClass("focused neighbor dim");
      cy.nodes().toggleClass("hide-label", !showLabels);
      if (!selection) return;
      const chosen = cy.getElementById(selection.id);
      if (!chosen.length) return;
      const neighborhood =
        selection.kind === "edge"
          ? chosen.union(chosen.connectedNodes())
          : chosen.closedNeighborhood();
      cy.elements().difference(neighborhood).addClass("dim");
      neighborhood.addClass("neighbor");
      chosen.addClass("focused");
      if (selection.kind === "edge")
        chosen.connectedNodes().addClass("focused");
    });
    avoidLabelOverlap(cy);
  }, [selection, nodes, edges, showLabels, coreRef]);
  return (
    <div
      ref={container}
      className="graph-canvas"
      role="img"
      aria-label={t("Knowledge graph. Node count {0}. Edge count {1}. Use entry navigation or the graph list view to access details.", nodes.length, edges.length)}
    />
  );
}

/** Define type colors, evidence edges, and readable focus levels. */
function graphStyles(): StylesheetStyle[] {
  return [
    {
      selector: "node",
      style: {
        label: "data(canvasLabel)",
        width: "data(displaySize)",
        height: "data(displaySize)",
        "background-color": "data(color)",
        "border-color": "#ffffff",
        "border-width": "data(borderWidth)",
        "font-size": "data(fontSize)",
        "font-weight": 600,
        "font-family": "-apple-system, BlinkMacSystemFont, sans-serif",
        color: "#29384e",
        "text-valign": "center",
        "text-halign": (node: NodeSingular) =>
          node.data("labelAlign") as "left" | "right",
        "text-margin-x": (node: NodeSingular) =>
          Number(node.data("labelMargin")),
        "text-wrap": "ellipsis",
        "text-max-width": "data(labelWidth)",
        "text-outline-color": "#f8fafd",
        "text-outline-width": "data(outlineWidth)",
        "text-outline-opacity": 0.95,
        "overlay-opacity": 0,
      },
    },
    {
      selector: "edge",
      style: {
        width: "data(displayWeight)",
        "line-color": "#bacce5",
        "curve-style": "bezier",
        "control-point-step-size": 30,
        opacity: 0.65,
        label: "",
        "font-size": "data(fontSize)",
        color: "#546781",
        "text-background-color": "#ffffff",
        "text-background-opacity": 0.95,
        "text-background-padding": "4px",
        "text-rotation": "autorotate",
        "overlay-opacity": 0,
      },
    },
    {
      selector: 'edge[kind="relation"]',
      style: { "line-color": "#7c96bf", opacity: 0.85 },
    },
    {
      selector: 'edge[status="pending"]',
      style: { "line-style": "dashed", "line-color": "#bd955a" },
    },
    {
      selector: "node.dim",
      style: {
        "background-opacity": 0.42,
        "text-opacity": 0.65,
        "border-opacity": 0.4,
      },
    },
    { selector: "edge.dim", style: { opacity: 0.18 } },
    { selector: "node.hide-label", style: { label: "" } },
    { selector: "node.crowded-label", style: { label: "" } },
    {
      selector: "node.neighbor",
      style: { label: "data(canvasLabel)", "text-opacity": 1 },
    },
    {
      selector: "edge.neighbor",
      style: {
        opacity: 1,
        "line-color": "#6e8ebb",
        width: "data(neighborWeight)",
      },
    },
    {
      selector: 'edge.neighbor[status="pending"]',
      style: { "line-color": "#b28b4d" },
    },
    {
      selector: "node.focused, node.hovered",
      style: {
        label: "data(label)",
        "underlay-color": "data(color)",
        "underlay-shape": "ellipse",
        "underlay-opacity": 0.14,
        "underlay-padding": "data(haloPadding)",
        "z-index": 10,
        "background-opacity": 1,
        "text-opacity": 1,
        "text-max-width": "data(focusLabelWidth)",
      },
    },
    {
      selector: "edge.focused, edge.hovered",
      style: {
        label: "data(label)",
        "line-color": "#416fae",
        width: "data(focusWeight)",
        opacity: 1,
        "z-index": 9,
      },
    },
  ];
}

/** Keep labels at readable screen sizes as graph nodes scale. */
function updateGraphScale(cy: Core) {
  const zoom = cy.zoom();
  const narrowScale = Math.min(1, Math.max(0.55, cy.width() / 600));
  const density = Math.max(
    0.62,
    Math.min(1, 5 / Math.sqrt(Math.max(1, cy.nodes().length))),
  );
  cy.batch(() => {
    cy.nodes().forEach((node) => {
      node.data({
        displaySize:
          (Number(node.data("size")) *
            density *
            narrowScale *
            Math.pow(zoom, 0.15)) /
          zoom,
        fontSize: 12 / zoom,
        labelMargin: 7 / zoom,
        labelWidth: 150 / zoom,
        focusLabelWidth: 240 / zoom,
        outlineWidth: 1.5 / zoom,
        borderWidth: 2 / zoom,
        haloPadding: 7 / zoom,
      });
    });
    cy.edges().forEach((edge) => {
      edge.data({
        displayWeight: (Number(edge.data("weight")) * 0.75) / zoom,
        neighborWeight: 2 / zoom,
        focusWeight: 3 / zoom,
        fontSize: 10 / zoom,
      });
    });
  });
  avoidLabelOverlap(cy);
}

/** Place high-connection labels first and avoid existing labels in dense graphs. */
function avoidLabelOverlap(cy: Core) {
  cy.nodes().removeClass("crowded-label");
  if (cy.nodes().length < 20) return;
  const rectangles: Array<{ x: number; y: number; w: number; h: number }> = [];
  const ordered = cy
    .nodes()
    .toArray()
    .sort(
      (a, b) =>
        Number(b.hasClass("neighbor")) - Number(a.hasClass("neighbor")) ||
        Number(b.data("degree")) - Number(a.data("degree")) ||
        a.id().localeCompare(b.id()),
    );
  cy.batch(() => {
    for (const node of ordered) {
      const point = node.renderedPosition();
      const width = Math.min(
        150,
        Array.from(String(node.data("canvasLabel"))).reduce(
          (total, char) => total + (/[^\x00-\x7f]/.test(char) ? 12 : 6.7),
          0,
        ),
      );
      const radius = node.renderedWidth() / 2 + 7;
      const candidates = [
        { x: point.x + radius, y: point.y - 8, w: width, h: 16, side: "right" },
        {
          x: point.x - radius - width,
          y: point.y - 8,
          w: width,
          h: 16,
          side: "left",
        },
      ];
      const label = candidates.find(
        (candidate) =>
          candidate.x >= 12 &&
          candidate.x + candidate.w <= cy.width() - 12 &&
          !rectangles.some(
            (rect) =>
              candidate.x < rect.x + rect.w + 4 &&
              candidate.x + candidate.w + 4 > rect.x &&
              candidate.y < rect.y + rect.h + 3 &&
              candidate.y + candidate.h + 3 > rect.y,
          ),
      );
      if (!label && !node.hasClass("neighbor")) node.addClass("crowded-label");
      else {
        const placed = label || candidates[0];
        node.data({
          labelAlign: placed.side,
          labelMargin: (placed.side === "right" ? 7 : -7) / cy.zoom(),
        });
        rectangles.push(placed);
      }
    }
  });
}

/** Fit node bounds to the viewport with room for labels. */
function fitGraph(cy: Core) {
  if (!cy.nodes().length || cy.width() <= 0 || cy.height() <= 0) return;
  const bounds = cy
    .nodes()
    .boundingBox({ includeLabels: false, includeOverlays: false });
  const zoom = Math.max(
    cy.minZoom(),
    Math.min(
      1.25,
      (cy.width() - 185) / Math.max(1, bounds.w),
      (cy.height() - 140) / Math.max(1, bounds.h),
    ),
  );
  cy.viewport({
    zoom,
    pan: {
      x: (cy.width() - 65) / 2 - (bounds.x1 + bounds.w / 2) * zoom,
      y: cy.height() / 2 - (bounds.y1 + bounds.h / 2) * zoom,
    },
  });
}

/** Use a layered small-graph layout or force-directed large-graph layout. */
function layoutGraph(cy: Core, mode: LayoutMode) {
  cy.zoom(1);
  updateGraphScale(cy);
  const common = {
    animate: false,
    fit: false,
    padding: 60,
    nodeDimensionsIncludeLabels: true,
    stop: () => finishLayout(cy, mode),
  };
  if (mode === "circle")
    cy.layout({
      ...common,
      name: "circle",
      spacingFactor: 1.3,
      avoidOverlap: true,
    }).run();
  else if (cy.nodes().length <= 12) {
    // Place small graphs on a deterministic center and outer ring.
    const ordered = cy
      .nodes()
      .toArray()
      .sort(
        (a, b) =>
          Number(b.data("degree")) - Number(a.data("degree")) ||
          a.id().localeCompare(b.id()),
      );
    const radius = 135 + Math.min(60, ordered.length * 6);
    cy.batch(() =>
      ordered.forEach((node, index) => {
        const angle =
          -Math.PI / 2 +
          ((index - 1) * Math.PI * 2) / Math.max(1, ordered.length - 1);
        node.position(
          index === 0
            ? { x: 0, y: 0 }
            : { x: Math.cos(angle) * radius, y: Math.sin(angle) * radius },
        );
      }),
    );
    fitGraph(cy);
  } else
    cy.layout({
      ...common,
      name: "cose",
      randomize: false,
      nodeRepulsion: () => 7000,
      idealEdgeLength: () => 115,
      nodeOverlap: 24,
      componentSpacing: 100,
      numIter: 650,
      gravity: 0.65,
    }).run();
}

/** Wait for committed layout coordinates before fitting the viewport. */
function finishLayout(cy: Core, mode: LayoutMode) {
  // Expand dense layouts across the available canvas dimensions.
  if (mode === "network" && cy.nodes().length > 12) {
    const bounds = cy
      .nodes()
      .boundingBox({ includeLabels: false, includeOverlays: false });
    const factor = Math.max(
      0.65,
      Math.min(
        4,
        (cy.width() - 185) / (cy.height() - 140) / (bounds.w / bounds.h),
      ),
    );
    cy.nodes().positions((node) => ({
      x: node.position("x") * factor,
      y: node.position("y"),
    }));
  }
  fitGraph(cy);
}
