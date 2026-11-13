import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  type RefObject,
} from "react";
import "./resize.css";

interface SizeRange {
  min: number;
  max: number;
}

/** Read a saved panel size, ignoring missing or unusable values. */
function readSize(key: string) {
  try {
    const value = Number(localStorage.getItem(key));
    return Number.isFinite(value) && value >= 80 ? Math.round(value) : null;
  } catch {
    return null;
  }
}

/** Keep a panel size for this browser without failing when storage is blocked. */
export function useStoredSize(key: string) {
  const [size, setSize] = useState<number | null>(() => readSize(key));
  const commit = useCallback(
    (next: number | null) => {
      const value = next == null ? null : Math.round(next);
      setSize(value);
      try {
        if (value == null) localStorage.removeItem(key);
        else localStorage.setItem(key, String(value));
      } catch {
        /* The layout still follows the drag when storage is unavailable. */
      }
    },
    [key],
  );
  return [size, commit] as const;
}

function clamp(value: number, range: SizeRange) {
  return Math.min(range.max, Math.max(range.min, value));
}

/** Leave the manuscript column usable beside the docked chat. */
export function assistantWidthBounds(panel: HTMLElement): SizeRange {
  const frame = panel.closest(".unified-workspace") ?? panel.parentElement;
  const total = frame?.getBoundingClientRect().width ?? window.innerWidth;
  const min = 300;
  return { min, max: Math.max(min, Math.round(total - 320)) };
}

/** Leave room to read the conversation while the composer can still hold a reply. */
export function composerSizeBounds(composer: HTMLElement): SizeRange {
  const panel = composer.closest(".assistant-panel");
  const panelHeight = panel?.getBoundingClientRect().height ?? 640;
  const style = getComputedStyle(composer);
  const padding =
    parseFloat(style.paddingTop) + parseFloat(style.paddingBottom);
  const extras = [...composer.children].filter(
    (child) => !child.classList.contains("chat-input-shell"),
  );
  const extraHeight = extras.reduce((height, child) => {
    const childStyle = getComputedStyle(child);
    return (
      height +
      child.getBoundingClientRect().height +
      parseFloat(childStyle.marginTop) +
      parseFloat(childStyle.marginBottom)
    );
  }, padding);
  const max = Math.max(200, Math.round(panelHeight - 160));
  return {
    min: Math.min(max, Math.max(200, Math.ceil(extraHeight + 144))),
    max,
  };
}

/** Drag or keyboard-resize one edge, and restore the automatic size on double-click. */
export function ResizeHandle({
  axis,
  label,
  resetLabel,
  value,
  onChange,
  bounds,
  target,
}: {
  axis: "x" | "y";
  label: string;
  resetLabel: string;
  value: number | null;
  onChange: (next: number | null) => void;
  bounds: (element: HTMLElement) => SizeRange;
  target?: RefObject<HTMLElement | null>;
}) {
  const handleRef = useRef<HTMLDivElement>(null);
  const release = useRef<(() => void) | null>(null);
  const [dragging, setDragging] = useState(false);
  const [range, setRange] = useState<SizeRange>({ min: 0, max: 0 });

  /** Resolve the element this handle resizes. */
  function subject() {
    return target?.current ?? handleRef.current?.parentElement ?? null;
  }

  // Sibling refs are attached by the passive-effect phase. Measuring the parent
  // while the explicit composer ref is pending would expand it to the full pane.
  useEffect(() => {
    const element = target ? target.current : handleRef.current?.parentElement;
    if (!element) return;
    /** Reconcile persisted sizes with the space required by current controls and chips. */
    function reconcile() {
      if (!element!.getBoundingClientRect().width || !element!.getBoundingClientRect().height) return;
      const next = bounds(element!);
      setRange((current) =>
        current.min === next.min && current.max === next.max ? current : next,
      );
      if (value != null) {
        const adjusted = clamp(value, next);
        if (adjusted !== value) onChange(adjusted);
      }
    }
    const observer = new ResizeObserver(reconcile);
    observer.observe(element);
    const frame = element.closest(axis === "y" ? ".assistant-panel" : ".unified-workspace");
    if (frame) observer.observe(frame);
    /** Track newly inserted skill, context and error rows without observing streamed conversation text. */
    function observeControls() {
      if (axis === "y") for (const child of element!.children) observer.observe(child);
      reconcile();
    }
    const mutations = new MutationObserver(observeControls);
    if (axis === "y") mutations.observe(element, { childList: true });
    observeControls();
    return () => { observer.disconnect(); mutations.disconnect(); };
  }, [axis, bounds, target, value, onChange]);

  useEffect(() => () => release.current?.(), []);

  /** Track the pointer until release, growing the pane when it is pulled outward. */
  function onPointerDown(event: ReactPointerEvent<HTMLDivElement>) {
    if (event.pointerType === "mouse" && event.button !== 0) return;
    const element = subject();
    if (!element) return;
    event.preventDefault();
    event.stopPropagation();
    const handle = event.currentTarget;
    const limits = bounds(element);
    const origin =
      element.getBoundingClientRect()[axis === "x" ? "width" : "height"];
    const start = axis === "x" ? event.clientX : event.clientY;
    setRange(limits);
    setDragging(true);
    handle.setPointerCapture(event.pointerId);
    const previousCursor = document.body.style.cursor;
    const previousSelect = document.body.style.userSelect;
    document.body.style.cursor = axis === "x" ? "col-resize" : "row-resize";
    document.body.style.userSelect = "none";

    /** Apply the pointer delta. The right and bottom panes grow when the pointer moves toward the opposite edge. */
    function move(next: PointerEvent) {
      const point = axis === "x" ? next.clientX : next.clientY;
      onChange(clamp(origin - (point - start), limits));
    }
    /** Restore the document after the drag, including when the pointer is cancelled. */
    function finish() {
      setDragging(false);
      document.body.style.cursor = previousCursor;
      document.body.style.userSelect = previousSelect;
      handle.removeEventListener("pointermove", move);
      handle.removeEventListener("pointerup", finish);
      handle.removeEventListener("pointercancel", finish);
      release.current = null;
    }
    release.current = finish;
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", finish);
    handle.addEventListener("pointercancel", finish);
  }

  /** Move the edge from the keyboard using the same limits as a drag. */
  function onKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    const element = subject();
    if (!element) return;
    const limits = bounds(element);
    const current =
      value ??
      element.getBoundingClientRect()[axis === "x" ? "width" : "height"];
    const step = event.shiftKey ? 80 : 24;
    const wider =
      axis === "x" ? event.key === "ArrowLeft" : event.key === "ArrowUp";
    const narrower =
      axis === "x" ? event.key === "ArrowRight" : event.key === "ArrowDown";
    if (!wider && !narrower) return;
    event.preventDefault();
    setRange(limits);
    onChange(clamp(current + (wider ? step : -step), limits));
  }

  return (
    <div
      ref={handleRef}
      className={`panel-resize panel-resize-${axis}`}
      role="separator"
      aria-orientation={axis === "x" ? "vertical" : "horizontal"}
      aria-label={label}
      aria-valuemin={range.max ? range.min : undefined}
      aria-valuemax={range.max || undefined}
      aria-valuenow={value ?? undefined}
      title={`${label} · ${resetLabel}`}
      tabIndex={0}
      data-dragging={dragging ? "true" : "false"}
      onPointerDown={onPointerDown}
      onKeyDown={onKeyDown}
      onDoubleClick={() => onChange(null)}
    />
  );
}
