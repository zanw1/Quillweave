import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";

const runner = fs.readFileSync(
  fileURLToPath(new URL("../run.mjs", import.meta.url)),
  "utf8",
);
const source = "//go:build quillweave_whitebox\n\npackage core\n";
const cases = [
  { name: "LF source", source, valid: true },
  { name: "CRLF source", source: source.replaceAll("\n", "\r\n"), valid: true },
  {
    name: "CRLF header and LF body",
    source: source.replace("\n", "\r\n"),
    valid: true,
  },
  {
    name: "missing tag",
    source: "package core\n",
    error: /Missing test-source tag/,
  },
  {
    name: "wrong tag",
    source: source.replace("quillweave_whitebox", "another_tag"),
    error: /Missing test-source tag/,
  },
  {
    name: "tag with extra suffix",
    source: source.replace("quillweave_whitebox", "quillweave_whitebox_extra"),
    error: /Missing test-source tag/,
  },
  {
    name: "unterminated tag",
    source: "//go:build quillweave_whitebox",
    error: /Missing test-source tag/,
  },
  {
    name: "obsolete numbered suite",
    source,
    filename: "core_suite_2_test.go",
    error: /Numbered suite remains/,
  },
  {
    name: "scattered Python test",
    source,
    outside: "frontend/src/example/test_parser.py",
    error: /maintained test is outside test\//,
  },
  {
    name: "scattered UI cases",
    source,
    outside: "frontend/src/example/example_ui_cases.js",
    error: /maintained test is outside test\//,
  },
];

/** Exercise the real CLI in a minimal disposable tree, preserving failure checks on every supported line ending. */
function checkFixture(candidate) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "quillweave-layout-"));
  try {
    for (const directory of [
      "test/backend/core",
      "internal/core",
      "cmd",
      "scripts",
      "frontend/src",
    ])
      fs.mkdirSync(path.join(root, directory), { recursive: true });
    fs.writeFileSync(path.join(root, "test/run.mjs"), runner);
    fs.writeFileSync(
      path.join(root, "test/layout.json"),
      JSON.stringify({ go_modules: { core: "internal/core" } }),
    );
    fs.writeFileSync(
      path.join(
        root,
        "test/backend/core",
        candidate.filename || "fixture_test.go",
      ),
      candidate.source,
    );
    if (candidate.outside) {
      const filename = path.join(root, candidate.outside);
      fs.mkdirSync(path.dirname(filename), { recursive: true });
      fs.writeFileSync(filename, "test fixture\n");
    }
    const result = spawnSync(
      process.execPath,
      [path.join(root, "test/run.mjs"), "layout"],
      {
        cwd: root,
        encoding: "utf8",
        timeout: 15000,
      },
    );
    assert.ifError(result.error);
    assert.equal(
      result.status,
      candidate.valid ? 0 : 1,
      result.stdout + result.stderr,
    );
    if (candidate.valid)
      assert.match(result.stdout, /Test layout passed: 1 Go backing files/);
    else assert.match(result.stderr, candidate.error);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

for (const candidate of cases) {
  test(candidate.name, () => checkFixture(candidate));
}
