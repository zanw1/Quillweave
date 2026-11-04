import { t } from "../lib/i18n";
import { useEffect, useRef, type ReactNode } from "react";
import {
  IconX,
  IconArrowRight,
  IconFileText,
  IconAlertCircle,
} from "@tabler/icons-react";

/** Share visual hierarchy and disabled behavior across lightweight buttons. */
export function Button({
  children,
  primary = false,
  className = "",
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { primary?: boolean }) {
  return (
    <button
      type="button"
      className={`button ${primary ? "primary" : ""} ${className}`}
      {...props}
    >
      {children}
    </button>
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
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  wide?: boolean;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    ref.current?.showModal();
    return () => ref.current?.close();
  }, []);
  return (
    <dialog
      ref={ref}
      className={`modal ${wide ? "wide" : ""}`}
      onCancel={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
      aria-label={title}
    >
      <header>
        <h2>{title}</h2>
        <button
          type="button"
          className="icon-button"
          onClick={onClose}
          aria-label={t("Close dialog")}
        >
          <IconX size={20} />
        </button>
      </header>
      {children}
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
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    ref.current?.showModal();
    return () => ref.current?.close();
  }, []);
  return (
    <dialog
      ref={ref}
      className="drawer"
      onCancel={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
      aria-label={title}
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
        <h2>{title}</h2>
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
