import { useSyncExternalStore } from "react";
import { zhCN } from "../locales/zh-CN";

export type Language = "zh-CN" | "en";
const storageKey = "quillweave-language";
const listeners = new Set<() => void>();

/** Restore a supported interface language, including when storage is unavailable. */
function readLanguage(): Language {
  try {
    return localStorage.getItem(storageKey) === "zh-CN" ? "zh-CN" : "en";
  } catch {
    return "en";
  }
}

let language = readLanguage();

/** Keep browser accessibility metadata aligned with the visible interface. */
function updateDocument() {
  if (typeof document !== "undefined") document.documentElement.lang = language;
}
updateDocument();

/** Return a stable snapshot for React and locale-aware formatting helpers. */
export function getLanguage(): Language {
  return language;
}

/** Update the interface immediately and retain the preference across launches. */
export function setLanguage(value: Language) {
  if (value !== "en" && value !== "zh-CN") return;
  language = value;
  try {
    localStorage.setItem(storageKey, value);
  } catch {
    /* Keep the current session usable when browser storage is blocked. */
  }
  updateDocument();
  listeners.forEach((listener) => listener());
}

/** Observe language changes in this window and other windows sharing its storage. */
function subscribe(listener: () => void) {
  listeners.add(listener);
  /** Apply a preference changed by another window without writing it back. */
  function onStorage(event: StorageEvent) {
    if (event.key !== storageKey && event.key !== null) return;
    language = readLanguage();
    updateDocument();
    listener();
  }
  window.addEventListener("storage", onStorage);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", onStorage);
  };
}

/** Rerender the application on a language change while retaining component state. */
export function useLanguage() {
  return useSyncExternalStore(subscribe, getLanguage);
}

/** Substitute numbered values without interpreting characters inside author content. */
function interpolate(text: string, values: (string | number)[]) {
  return text.replace(/\{(\d+)\}/g, (placeholder, index: string) =>
    String(values[Number(index)] ?? placeholder),
  );
}

const messagePatterns = Object.keys(zhCN)
  .filter((key) => /\{\d+\}/.test(key))
  .map((key) => ({
    key,
    pattern: new RegExp(
      "^" + key.split(/\{\d+\}/).map((part) =>
        part.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"),
      ).join("([\\s\\S]+?)") + "$",
    ),
  }));

/** Translate application-owned text and formatted server notices with English fallback. */
export function t(text: string, ...values: (string | number)[]): string {
  if (language === "en") return interpolate(text, values);
  if (Object.prototype.hasOwnProperty.call(zhCN, text))
    return interpolate(zhCN[text], values);
  if (!values.length) {
    for (const { key, pattern } of messagePatterns) {
      const match = pattern.exec(text);
      if (match) return interpolate(zhCN[key], match.slice(1));
    }
    const separator = text.indexOf("：");
    if (separator >= 0)
      return text.slice(0, separator + 1) + t(text.slice(separator + 1));
  }
  return interpolate(text, values);
}
