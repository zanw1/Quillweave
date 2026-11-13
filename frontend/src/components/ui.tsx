import { t } from "../lib/i18n";
import type { SecretUpdate } from "../domain/types";
import {
  lazy,
  Suspense,
  useEffect,
  useId,
  useRef,
  type ReactNode,
  type ComponentProps,
  type MouseEvent,
  type PointerEvent,
} from "react";
import {
  IconX,
  IconArrowRight,
  IconFileText,
  IconAlertCircle,
} from "@tabler/icons-react";

const StreamdownResponse = lazy(() => import("./MarkdownResponse"));

/** Load Markdown rendering only for AI output and preserve plain text while the chunk loads. */
export function MarkdownResponse(props: {
  text: string;
  streaming?: boolean;
  incomplete?: boolean;
}) {
  return (
    <Suspense fallback={<div className="answer-text">{props.text}</div>}>
      <StreamdownResponse {...props} />
    </Suspense>
  );
}

/** Share visual hierarchy and disabled behavior across lightweight buttons. */
export function Button({
  children,
  primary = false,
  className = "",
  size = "md",
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  primary?: boolean;
  size?: "sm" | "md";
}) {
  return (
    <button
      type="button"
      className={`button ${primary ? "primary" : ""} ${className}`}
      data-size={size}
      {...props}
    >
      {children}
    </button>
  );
}

/** Keep boolean settings keyboard-native while sharing the Tailwind Plus switch field structure. */
export function Switch({
  className = "",
  ...props
}: Omit<ComponentProps<"input">, "type" | "role">) {
  return (
    <span className={`qw-switch ${className}`}>
      <input
        {...props}
        type="checkbox"
        role="switch"
        className="qw-switch-input"
      />
      <span className="qw-switch-track" aria-hidden="true">
        <span />
      </span>
    </span>
  );
}

/** Edit keys directly while representing a retained server-side secret without reading or resubmitting it. */
export function SecretField({
  label,
  ariaLabel = label,
  configured,
  update,
  onChange,
  disabled = false,
}: {
  label: string;
  ariaLabel?: string;
  configured: boolean;
  update?: SecretUpdate;
  onChange: (update: SecretUpdate) => void;
  disabled?: boolean;
}) {
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const value = update?.action === "replace" ? update.value : "";
  const retained = configured && (!update || update.action === "preserve");
  return (
    <div className="secret-field">
      <label htmlFor={id}>{label}</label>
      <div className="secret-input-row">
        <input
          ref={input}
          id={id}
          aria-label={ariaLabel}
          type="password"
          autoComplete="new-password"
          spellCheck={false}
          disabled={disabled}
          value={value}
          placeholder={retained ? "••••••••" : undefined}
          onChange={(event) =>
            onChange(
              event.target.value
                ? { action: "replace", value: event.target.value }
                : { action: "clear" },
            )
          }
        />
        {(retained || value) && (
          <Button
            disabled={disabled}
            aria-label={`${t("Clear key")} · ${ariaLabel}`}
            onClick={() => {
              onChange({ action: "clear" });
              input.current?.focus();
            }}
          >
            {t("Clear key")}
          </Button>
        )}
      </div>
    </div>
  );
}

/** Mark required fields visually while native required semantics preserve their accessible names. */
export function RequiredLabel({ children }: { children: ReactNode }) {
  return (
    <span className="qw-field-label">
      {children}
      <span className="qw-required" aria-hidden="true">
        *
      </span>
    </span>
  );
}

/** Share native focus containment and dismiss only a complete click on the actual backdrop. */
function useDialog(onClose: () => void, open = true, modal = true, initialFocus?: string) {
  const ref = useRef<HTMLDialogElement>(null);
  const backdropPress = useRef(false);
  const previousModal = useRef(modal);
  const retainedFocus = useRef<HTMLElement | null>(null);
  useEffect(() => {
    const dialog = ref.current;
    const modeChanged = previousModal.current !== modal;
    previousModal.current = modal;
    if (!modeChanged) retainedFocus.current = null;
    if (!dialog) return;
    let deferPromotion = modeChanged && modal;

    /** Keep existing overlays above a docked panel while it becomes a modal drawer on resize. */
    function syncDialog() {
      if (!dialog || !open) return;
      const anotherModal = [...document.querySelectorAll<HTMLDialogElement>("dialog[open]")]
        .some((candidate) => candidate !== dialog && candidate.matches(":modal"));
      if (deferPromotion && anotherModal) {
        const focused = document.activeElement;
        if (!dialog.open) dialog.show();
        if (focused instanceof HTMLElement && focused.isConnected) focused.focus({ preventScroll: true });
        return;
      }
      deferPromotion = false;
      if (dialog.open && dialog.matches(":modal") === modal) return;
      const focused = document.activeElement instanceof HTMLElement && dialog.contains(document.activeElement)
        ? document.activeElement : retainedFocus.current;
      retainedFocus.current = null;
      document.removeEventListener("close", syncDialog, true);
      if (dialog.open) dialog.close();
      if (modal) dialog.showModal();
      else dialog.show();
      const target = focused?.isConnected && focused.getClientRects().length > 0 && !focused.matches(":disabled")
        ? focused
        : initialFocus
          ? dialog.querySelector<HTMLElement>(`${initialFocus}:not(:disabled)`) ?? dialog.querySelector<HTMLElement>("button:not(:disabled)")
          : null;
      target?.focus({ preventScroll: true });
    }
    if (open) {
      document.addEventListener("close", syncDialog, true);
      syncDialog();
    }
    return () => {
      document.removeEventListener("close", syncDialog, true);
      if (document.activeElement instanceof HTMLElement && dialog.contains(document.activeElement)) {
        retainedFocus.current = document.activeElement;
      }
      dialog.close();
    };
  }, [open, modal, initialFocus]);
  /** Separate a backdrop from the dialog's own blank content and scrollbar. */
  function outside(
    event: MouseEvent<HTMLDialogElement> | PointerEvent<HTMLDialogElement>,
  ) {
    const box = event.currentTarget.getBoundingClientRect();
    return (
      event.clientX < box.left ||
      event.clientX > box.right ||
      event.clientY < box.top ||
      event.clientY > box.bottom
    );
  }
  return {
    ref,
    onCancel: (event: React.SyntheticEvent<HTMLDialogElement>) => {
      event.preventDefault();
      event.stopPropagation();
      if (event.target !== event.currentTarget) return;
      onClose();
    },
    onPointerDown: (event: PointerEvent<HTMLDialogElement>) => {
      backdropPress.current =
        event.target === event.currentTarget && outside(event);
    },
    onClick: (event: MouseEvent<HTMLDialogElement>) => {
      if (
        backdropPress.current &&
        event.target === event.currentTarget &&
        outside(event)
      )
        onClose();
      backdropPress.current = false;
    },
  };
}

/** Keep panel children mounted while switching between a docked panel and a focus-contained drawer. */
export function DockablePanel({
  title,
  open,
  modal,
  onClose,
  children,
  className,
  initialFocus,
}: {
  title: string;
  open: boolean;
  modal: boolean;
  onClose: () => void;
  children: ReactNode;
  className: string;
  initialFocus?: string;
}) {
  const dialog = useDialog(onClose, open, modal, initialFocus);
  return (
    <dialog {...dialog} aria-label={title} className={className}>
      {children}
    </dialog>
  );
}

/** Provide a clear next action for empty states and search results. */
export function Empty({
  title,
  detail,
  children,
}: {
  title: string;
  detail: string;
  children?: ReactNode;
}) {
  return (
    <div className="empty">
      <IconFileText size={34} stroke={1.4} />
      <h3>{title}</h3>
      <p>{detail}</p>
      {children}
    </div>
  );
}

/** Use a native dialog for focus containment, Escape handling, and accessible semantics. */
export function Modal({
  title,
  children,
  onClose,
  wide = false,
  footer,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  wide?: boolean;
  footer?: ReactNode;
}) {
  const dialog = useDialog(onClose);
  const titleId = useId();
  return (
    <dialog
      {...dialog}
      className={`modal ${wide ? "wide" : ""}`}
      aria-label={title}
      aria-labelledby={titleId}
    >
      <header>
        <h2 id={titleId}>{title}</h2>
        <button
          type="button"
          className="icon-button"
          onClick={onClose}
          aria-label={t("Close dialog")}
        >
          <IconX size={20} />
        </button>
      </header>
      <div className="modal-body">{children}</div>
      {footer && <footer className="modal-actions">{footer}</footer>}
    </dialog>
  );
}

/** Use a dialog-backed detail drawer alongside the main table. */
export function Drawer({
  title,
  subtitle,
  children,
  onClose,
}: {
  title: string;
  subtitle: string;
  children: ReactNode;
  onClose: () => void;
}) {
  const dialog = useDialog(onClose);
  const titleId = useId();
  return (
    <dialog
      {...dialog}
      className="drawer"
      aria-label={title}
      aria-labelledby={titleId}
    >
      <header>
        <span>{subtitle}</span>
        <button
          className="icon-button"
          onClick={onClose}
          aria-label={t("Close details")}
        >
          <IconX size={21} />
        </button>
      </header>
      <div className="drawer-body">
        <h2 id={titleId}>{title}</h2>
        {children}
      </div>
    </dialog>
  );
}

/** Communicate status with both text and color. */
export function Badge({
  children,
  tone = "gray",
}: {
  children: ReactNode;
  tone?: "gray" | "blue" | "green" | "amber" | "red";
}) {
  return <span className={`badge ${tone}`}>{children}</span>;
}

/** Display recoverable errors while retaining the current editing state. */
export function ErrorBox({ children }: { children: ReactNode }) {
  return (
    <div className="error-box" role="alert">
      <IconAlertCircle size={18} />
      <span>{typeof children === "string" ? t(children) : children}</span>
    </div>
  );
}

/** Render suggested actions that populate a writing request. */
export function Suggestion({
  children,
  onClick,
}: {
  children: ReactNode;
  onClick: () => void;
}) {
  return (
    <button className="suggestion" onClick={onClick}>
      <span>{children}</span>
      <IconArrowRight size={17} />
    </button>
  );
}
