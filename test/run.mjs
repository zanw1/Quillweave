import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { spawn, spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = await fs.realpath(
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), ".."),
);
const configuration = JSON.parse(
  await fs.readFile(path.join(root, "test/layout.json"), "utf8"),
);
const modules = configuration.go_modules;
const arguments_ = process.argv.slice(2);
const mode =
  arguments_[0] && !arguments_[0].startsWith("-") ? arguments_.shift() : "go";

/** Return maintained files without traversing dependencies, symlinks or archived failure evidence. */
async function filesIn(directory) {
  const files = [];
  for (const entry of await fs.readdir(directory, { withFileTypes: true })) {
    if (
      entry.isSymbolicLink() ||
      ["node_modules", "archive", "__pycache__"].includes(entry.name)
    )
      continue;
    const filename = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...(await filesIn(filename)));
    else files.push(filename);
  }
  return files;
}

/** Reject scattered sources, untagged white-box packages, empty modules and obsolete numbered suites. */
async function checkLayout() {
  if (!modules || Array.isArray(modules) || !Object.keys(modules).length)
    throw new Error("Test layout must declare nonempty Go modules");
  const overlay = {};
  let total = 0;
  for (const [name, target] of Object.entries(modules)) {
    const directory = path.join(root, "test/backend", name);
    const sources = (await filesIn(directory)).filter((f) =>
      f.endsWith("_test.go"),
    );
    if (!sources.length) throw new Error(`No test sources for ${name}`);
    for (const filename of sources) {
      const content = await fs.readFile(filename, "utf8");
      if (!content.startsWith("//go:build quillweave_whitebox\n"))
        throw new Error(`Missing test-source tag: ${filename}`);
      if (/core_suite_\d+_test\.go$/.test(filename))
        throw new Error(`Numbered suite remains: ${filename}`);
      const logical = path.join(root, target, path.basename(filename));
      if (overlay[logical])
        throw new Error(`Duplicate module filename: ${filename}`);
      overlay[logical] = filename;
      total++;
    }
  }
  for (const directory of ["internal", "cmd", "scripts", "frontend/src"]) {
    for (const filename of await filesIn(path.join(root, directory))) {
      if (
        /_test\.go$|(?:^|\/)test_[^/]+\.py$|(?:^|\/)[^/]*_ui_cases\.(?:js|mjs)$/.test(
          filename,
        )
      ) {
        throw new Error(
          `A maintained test is outside test/: ${path.relative(root, filename)}`,
        );
      }
    }
  }
  return { overlay, total };
}

/** Stop only a child process group created by this runner, including a timed-out Go test descendant. */
function stopGroup(child, signal = "SIGTERM") {
  if (!child.pid) return;
  try {
    if (process.platform === "win32") {
      if (child.exitCode === null)
        spawnSync("taskkill", ["/pid", String(child.pid), "/t", "/f"], {
          stdio: "ignore",
        });
    } else process.kill(-child.pid, signal);
  } catch (error) {
    if (error.code !== "ESRCH") throw error;
  }
}

/** Read real Go terminal events while distinguishing parent adapter tests from the compiled business suites. */
async function execute(
  command,
  args,
  environment,
  report,
  parseGo = false,
  raw = false,
) {
  const child = spawn(command, args, {
    cwd: root,
    env: environment,
    detached: process.platform !== "win32",
    stdio: parseGo ? ["inherit", "pipe", "pipe"] : "inherit",
  });
  let interrupted = false;
  const interrupt = () => {
    interrupted = true;
    stopGroup(child, "SIGINT");
  };
  process.once("SIGINT", interrupt);
  process.once("SIGTERM", interrupt);
  let pending = "";
  if (parseGo) {
    const outputs = new Map();
    child.stdout.setEncoding("utf8");
    child.stderr.on("data", (bytes) => process.stderr.write(bytes));
    child.stdout.on("data", (chunk) => {
      pending += chunk;
      const lines = pending.split("\n");
      pending = lines.pop();
      for (const line of lines) {
        if (raw) process.stdout.write(line + "\n");
        let item;
        try {
          item = JSON.parse(line);
        } catch {
          if (!raw) console.log(line);
          continue;
        }
        if (!item.Output) continue;
        // test2json can split one long log line across several Output events.
        const key = `${item.Package}::${item.Test || ""}`;
        const output = (outputs.get(key) || "") + item.Output;
        const completed = output.split("\n");
        outputs.set(key, completed.pop());
        for (const line of completed) {
          const match = line.match(/BACKEND_RESULT (\{.*\})/);
          if (match) {
            const result = JSON.parse(match[1]);
            report.modules.push(result);
            if (!raw)
              console.log(
                `${result.module}: ${result.passed_top_level} top-level / ${result.passed_all_test_nodes} test nodes passed${result.race ? " (race)" : ""}`,
              );
          } else if (!raw && /FAIL|Error:|panic:|failed:/.test(line)) {
            process.stderr.write(line + "\n");
          }
        }
      }
    });
  }
  try {
    const code = await new Promise((resolve, reject) => {
      child.once("error", reject);
      child.once("close", (code) => resolve(code ?? 1));
    });
    if (code || interrupted)
      throw new Error(
        `${command} exited ${code}${interrupted ? " after interruption" : ""}`,
      );
  } finally {
    stopGroup(child);
    process.removeListener("SIGINT", interrupt);
    process.removeListener("SIGTERM", interrupt);
  }
}

/** Dispatch current test modules and remove their entire temporary root on success, failure or interruption. */
async function main() {
  const layout = await checkLayout();
  if (mode === "layout") {
    console.log(
      `Test layout passed: ${layout.total} Go backing files; all maintained tests are under test/.`,
    );
    return;
  }
  if (mode === "ui" || mode === "midscene") {
    const file = mode === "ui" ? "test_studio_ui.py" : "test_midscene_ui.py";
    await execute(
      process.env.PYTHON || "python3",
      [path.join(root, "test/frontend", file), ...arguments_],
      { ...process.env, PYTHONDONTWRITEBYTECODE: "1" },
      {},
    );
    return;
  }
  if (!["go", "race", "vet", ...Object.keys(modules)].includes(mode))
    throw new Error(`Unknown test module: ${mode}`);
  const options = {
    race: mode === "race",
    short: false,
    raw: false,
    filter: "",
    report: "",
  };
  while (arguments_.length) {
    const flag = arguments_.shift();
    if (flag === "--race") options.race = true;
    else if (flag === "--short") options.short = true;
    else if (flag === "--json") options.raw = true;
    else if (flag === "--case") options.filter = arguments_.shift() || "";
    else if (flag === "--report") options.report = arguments_.shift() || "";
    else throw new Error(`Unknown test option: ${flag}`);
  }
  const temporary = await fs.mkdtemp(
    path.join(os.tmpdir(), "quillweave-tests-"),
  );
  const report = {
    result: "failed",
    mode,
    race: options.race,
    modules: [],
    started_at: new Date().toISOString(),
  };
  const environment = {
    ...process.env,
    TMPDIR: temporary,
    GOTMPDIR: temporary,
    ...(process.platform === "win32" ? { TEMP: temporary, TMP: temporary } : {}),
    QUILLWEAVE_API_KEY: "",
    QUILLWEAVE_GO_TEST_RUN: options.filter,
  };
  try {
    if (mode === "vet") {
      const overlay = path.join(temporary, "overlay.json");
      await fs.writeFile(overlay, JSON.stringify({ Replace: layout.overlay }));
      await execute("go", ["vet", "./..."], environment, report);
      await execute(
        "go",
        [
          "vet",
          "-overlay=" + overlay,
          "-tags=quillweave_whitebox",
          ...Object.values(modules).map((p) => "./" + p),
        ],
        environment,
        report,
      );
    } else {
      const args = ["test", "-json", "-count=1", "-timeout=20m"];
      if (options.race) args.push("-race");
      if (options.short) args.push("-short");
      if (modules[mode]) args.push("-run=^TestBackendModules/" + mode + "$");
      args.push("./test/backend");
      await execute("go", args, environment, report, true, options.raw);
      const expectedModules = modules[mode] ? [mode] : Object.keys(modules);
      const observedModules = report.modules.map((m) => m.module);
      if (
        observedModules.length !== expectedModules.length ||
        expectedModules.some((name) => !observedModules.includes(name))
      )
        throw new Error("Missing or duplicate backend module results");
      report.passed_top_level = report.modules.reduce(
        (n, m) => n + m.passed_top_level,
        0,
      );
      report.passed_all_test_nodes = report.modules.reduce(
        (n, m) => n + m.passed_all_test_nodes,
        0,
      );
      report.skipped = report.modules.flatMap((m) => m.skipped);
      if (!report.passed_top_level)
        throw new Error("No passing business tests executed");
      if (options.race && report.modules.some((m) => !m.race))
        throw new Error("A child suite omitted the requested race detector");
      if (!options.raw)
        console.log(
          `Executed ${report.passed_top_level} top-level business tests / ${report.passed_all_test_nodes} test nodes; ${report.skipped.length} skipped helper(s).`,
        );
    }
    report.result = "passed";
  } finally {
    await fs.rm(temporary, { recursive: true, force: true });
    report.temporary_files_cleaned = true;
    report.finished_at = new Date().toISOString();
    if (options.report) {
      const filename = path.resolve(options.report);
      await fs.mkdir(path.dirname(filename), { recursive: true });
      await fs.writeFile(filename, JSON.stringify(report, null, 2) + "\n", {
        flag: "wx",
      });
    }
  }
}

main().catch((error) => {
  console.error(error.message);
  process.exitCode = 1;
});
