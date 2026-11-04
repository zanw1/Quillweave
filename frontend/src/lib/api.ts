import { getLanguage, t } from "./i18n";

/** Detect the injected native-window flag before loading desktop runtime code. */
function isDesktop(): boolean {
  const bridge = (
    window as unknown as {
      _wails?: { flags?: { quillweaveDesktop?: boolean } };
    }
  )._wails;
  return bridge?.flags?.quillweaveDesktop === true;
}

/** Call restricted Wails entry points through the shared desktop command bridge. */
async function desktopCall<T>(method: string, ...args: unknown[]): Promise<T> {
  const { Call } = await import("@wailsio/runtime");
  return Call.ByName(
    `quillweave/internal/desktop.Service.${method}`,
    ...args,
  ) as Promise<T>;
}

/** Use native IPC or loopback HTTP for the same business commands. */
export async function api<T>(command: string, args: unknown = {}): Promise<T> {
  if (isDesktop())
    return desktopCall<T>("Dispatch", command, JSON.stringify(args));
  const response = await fetch("/api/command", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Quillweave-Client": "1" },
    body: JSON.stringify({ command, args }),
  });
  const payload = await response.json().catch(() => {
    throw new Error(
      "The local Go service is unavailable. Check that it is running",
    );
  });
  if (!payload.ok) throw new Error(payload.error || "Operation failed");
  return payload.result;
}

/** Export requested text with path characters removed from its filename. */
export async function download(filename: string, content: string) {
  const safe = filename.replace(/[/\\:*?"<>|]/g, "_");
  if (isDesktop()) {
    await desktopCall<void>("Export", safe, content);
    return;
  }
  const url = URL.createObjectURL(
    new Blob([content], { type: "text/plain;charset=utf-8" }),
  );
  const link = document.createElement("a");
  link.href = url;
  link.download = safe;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/** Choose a legacy project folder through the native dialog or a browser-entered path. */
export async function pickDirectory(): Promise<string | null> {
  if (!isDesktop()) return null;
  const selected = await desktopCall<string>("PickDirectoryWithLanguage", getLanguage());
  return selected || null;
}

/** Format local dates, including legacy timestamps without timezone suffixes. */
export function date(value: string) {
  return value
    ? new Date(value).toLocaleString(getLanguage() === "en" ? "en-US" : "zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
      })
    : t("Not recorded");
}

/** Count non-whitespace Unicode characters consistently with the backend. */
export function wordCount(value: string) {
  return Array.from(value).filter((char) => !/\s/.test(char)).length;
}

/** Render an unknown error while preserving a domain error's message. */
export function message(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}

/** Compute the same SHA-256 digest as the backend for content freshness checks. */
export async function contentHash(value: string) {
  const digest = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(value),
  );
  return [...new Uint8Array(digest)]
    .map((byte) => byte.toString(16).padStart(2, "0"))
    .join("");
}
