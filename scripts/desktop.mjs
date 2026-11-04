import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { goEnv, nativeEnvironment } from "./build.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const children = new Set();
const watchers = [];
let closing = false;
let restarting = false;
let desktop;
let timer;

/** Create an owned process group for complete development-process cleanup. */
function start(command, args, env = process.env) {
  const child = spawn(command, args, {
    cwd: root,
    env,
    stdio: "inherit",
    detached: process.platform !== "win32",
    shell: process.platform === "win32" && command.endsWith(".cmd"),
  });
  children.add(child);
  child.on("error", (error) => {
    console.error(error.message);
    shutdown(1);
  });
  child.on("exit", () => children.delete(child));
  return child;
}

/** Stop only process groups created by this script. */
function stop(child) {
  if (!child?.pid) return;
  try {
    if (process.platform === "win32")
      spawnSync("taskkill", ["/PID", String(child.pid), "/T", "/F"], {
        stdio: "ignore",
      });
    else process.kill(-child.pid, "SIGTERM");
  } catch {
    /* The process may have exited normally. */
  }
}

/** Clean up Vite and source watchers after window closure, signals, or startup failures. */
function shutdown(code = 0) {
  if (closing) return;
  closing = true;
  clearTimeout(timer);
  for (const watcher of watchers) watcher.close();
  for (const child of children) stop(child);
  process.exitCode = code;
}

/** Start the native development window with Vite hot reload. */
function launchDesktop() {
  const env = {
    ...nativeEnvironment(goEnv("GOHOSTOS"), goEnv("GOHOSTARCH")),
    FRONTEND_DEVSERVER_URL: "http://127.0.0.1:1420",
  };
  desktop = start("go", ["run", "./cmd/quillweave"], env);
  desktop.on("exit", (code) => {
    if (!restarting) shutdown(code || 0);
  });
}

/** Debounce Go source edits and restart the backend after the previous window exits. */
function reloadDesktop() {
  clearTimeout(timer);
  timer = setTimeout(() => {
    if (closing || restarting) return;
    restarting = true;
    const old = desktop;
    old.once("exit", () => {
      restarting = false;
      if (!closing) launchDesktop();
    });
    stop(old);
  }, 300);
}

process.on("SIGINT", () => shutdown(0));
process.on("SIGTERM", () => shutdown(0));
const npm = process.platform === "win32" ? "npm.cmd" : "npm";
const compiled = spawnSync(npm, ["run", "build"], {
  cwd: root,
  stdio: "inherit",
  shell: process.platform === "win32",
});
if (compiled.status !== 0) process.exitCode = 1;
else {
  const vite = start(npm, ["run", "dev"]);
  vite.on("exit", (code) => {
    if (!closing) shutdown(code || 1);
  });
  launchDesktop();
  for (const directory of ["internal", "cmd"])
    watchers.push(
      fs.watch(
        path.join(root, directory),
        { recursive: true },
        (_event, filename) => {
          if (filename?.endsWith(".go")) reloadDesktop();
        },
      ),
    );
}
