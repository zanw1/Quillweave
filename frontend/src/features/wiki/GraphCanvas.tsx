import { useEffect, useRef } from "react";
import cytoscape, {
  type Core,
  type NodeSingular,
  type ElementDefinition,
  type StylesheetStyle,
} from "cytoscape";
import { t } from "../../lib/i18n";
import type { GraphNode, GraphEdge } from "../../domain/types";
export type Selection = { kind: "node" | "edge"; id: string } | null;
export type LayoutMode = "network" | "circle";
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
/** Size nodes by distinct neighbors without double-counting parallel review states. */
function connectionCounts(nodes: GraphNode[], edges: GraphEdge[]) {
  const neighbors = new Map(nodes.map((node) => [node.id, new Set<string>()]));
  for (const edge of edges) {
    neighbors.get(edge.source)?.add(edge.target);
    neighbors.get(edge.target)?.add(edge.source);
  }
  return new Map([...neighbors].map(([id, adjacent]) => [id, adjacent.size]));
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
export function GraphCanvas({
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
      aria-label={t(
        "Knowledge graph. Node count {0}. Edge count {1}. Use entry navigation or the graph list view to access details.",
        nodes.length,
        edges.length,
      )}
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
export function fitGraph(cy: Core) {
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
export function layoutGraph(cy: Core, mode: LayoutMode) {
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
