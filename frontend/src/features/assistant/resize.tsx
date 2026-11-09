import {
  useCallback,
  useEffect,
  useLayoutEffect,
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
  const commit = useCallback((next: number | null) => {
    const value = next == null ? null : Math.round(next);
    setSize(value);
    try {
      if (value == null) localStorage.removeItem(key);
      else localStorage.setItem(key, String(value));
    } catch {
      /* The layout still follows the drag when storage is unavailable. */
    }
  }, [key]);
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
  const min = 200;
  return { min, max: Math.max(min, Math.round(panelHeight - 160)) };
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

  useLayoutEffect(() => {
    const element = target?.current ?? handleRef.current?.parentElement;
    if (!element) return;
    const next = bounds(element);
    setRange((current) =>
      current.min === next.min && current.max === next.max ? current : next,
    );
  }, [bounds, target, value]);

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
    const origin = element.getBoundingClientRect()[axis === "x" ? "width" : "height"];
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
    const current = value ?? element.getBoundingClientRect()[axis === "x" ? "width" : "height"];
    const step = event.shiftKey ? 80 : 24;
    const wider =
      axis === "x"
        ? event.key === "ArrowLeft"
        : event.key === "ArrowUp";
    const narrower =
      axis === "x"
        ? event.key === "ArrowRight"
        : event.key === "ArrowDown";
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
