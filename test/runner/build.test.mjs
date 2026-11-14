import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { companionFilename } from "../../scripts/build.mjs";
import { checkReleaseVersion } from "../../scripts/release.mjs";

/** Reject artifact names that would replace the desktop executable on a case-insensitive volume. */
function assertDistinctArtifacts(target, desktop) {
  assert.notEqual(companionFilename(target).toLocaleLowerCase("en-US"), desktop.toLocaleLowerCase("en-US"));
  assert.match(companionFilename(target), /^quillweave-cli(?:\.exe)?$/);
}

test("macOS companion retains the desktop executable", () => assertDistinctArtifacts("darwin", "Quillweave"));
test("Windows companion retains the desktop executable", () => assertDistinctArtifacts("windows", "Quillweave.exe"));

/** Create consistent package metadata and register cleanup before writing any fixture files. */
function releaseFixture(t) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "quillweave-release-version-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 }));
  fs.mkdirSync(path.join(directory, "frontend"));
  fs.writeFileSync(path.join(directory, "package.json"), JSON.stringify({ version: "0.6.1" }));
  fs.writeFileSync(path.join(directory, "frontend", "package.json"), JSON.stringify({ version: "0.6.1" }));
  fs.writeFileSync(path.join(directory, "package-lock.json"), JSON.stringify({
    version: "0.6.1",
    packages: { "": { version: "0.6.1" }, frontend: { version: "0.6.1" } },
  }));
  return directory;
}

/** Change one isolated manifest without mutating the developer's repository or process environment. */
function updateManifest(directory, file, change) {
  const destination = path.join(directory, file);
  const value = JSON.parse(fs.readFileSync(destination, "utf8"));
  change(value);
  fs.writeFileSync(destination, JSON.stringify(value));
}

test("consistent local release versions pass", (t) => {
  assert.equal(checkReleaseVersion(releaseFixture(t), {}), "0.6.1");
});

test("a matching release tag passes", (t) => {
  assert.equal(checkReleaseVersion(releaseFixture(t), { GITHUB_REF_TYPE: "tag", GITHUB_REF_NAME: "v0.6.1" }), "0.6.1");
});

test("branch names do not become release versions", (t) => {
  assert.equal(checkReleaseVersion(releaseFixture(t), { GITHUB_REF_TYPE: "branch", GITHUB_REF_NAME: "main" }), "0.6.1");
});

test("tag mismatches report actual and expected versions", (t) => {
  assert.throws(() => checkReleaseVersion(releaseFixture(t), { GITHUB_REF_TYPE: "tag", GITHUB_REF_NAME: "v0.6.2" }), /Release tag v0\.6\.2.*version 0\.6\.1.*expected v0\.6\.1/);
});

test("release tags require the existing v prefix", (t) => {
  assert.throws(() => checkReleaseVersion(releaseFixture(t), { GITHUB_REF_TYPE: "tag", GITHUB_REF_NAME: "0.6.1" }), /expected v0\.6\.1/);
});

test("invalid package versions fail before building", (t) => {
  const directory = releaseFixture(t);
  for (const version of ["0.6", "v0.6.1", "0.6.1-beta", null, ["0.6.1"]]) {
    updateManifest(directory, "package.json", (value) => { value.version = version; });
    assert.throws(() => checkReleaseVersion(directory, {}), /three numeric components/);
  }
});

for (const [name, file, change, diagnostic] of [
  ["frontend", "frontend/package.json", (value) => { value.version = "0.6.0"; }, "frontend/package.json"],
  ["lockfile", "package-lock.json", (value) => { value.version = "0.6.0"; }, "package-lock.json version"],
  ["root lockfile package", "package-lock.json", (value) => { value.packages[""].version = "0.6.0"; }, 'packages[""]'],
  ["frontend lockfile package", "package-lock.json", (value) => { value.packages.frontend.version = "0.6.0"; }, 'packages["frontend"]'],
]) {
  test(`${name} version drift fails validation`, (t) => {
    const directory = releaseFixture(t);
    updateManifest(directory, file, change);
    assert.throws(() => checkReleaseVersion(directory, {}), (error) => error.message.includes(diagnostic) && error.message.includes("0.6.0") && error.message.includes("0.6.1"));
  });
}

test("missing workspace lockfile metadata fails validation", (t) => {
  const directory = releaseFixture(t);
  updateManifest(directory, "package-lock.json", (value) => { delete value.packages.frontend; });
  assert.throws(() => checkReleaseVersion(directory, {}), /frontend.*undefined/);
});

test("the preflight CLI runs without Go or npm on PATH", () => {
  const root = fileURLToPath(new URL("../../", import.meta.url));
  const version = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8")).version;
  const result = spawnSync(process.execPath, ["scripts/release.mjs", "--check"], {
    cwd: root,
    encoding: "utf8",
    env: {
      ...Object.fromEntries(Object.entries(process.env).filter(([key]) => key.toLowerCase() !== "path")),
      PATH: "",
      GITHUB_REF_TYPE: "tag",
      GITHUB_REF_NAME: `v${version}`,
    },
  });
  assert.equal(result.status, 0, result.stderr || result.error?.message);
  assert.equal(result.stdout.trim(), `Release version verified ${version}`);
});

test("both packaging entrypoints reject an inconsistent tag before launching a build", () => {
  const root = fileURLToPath(new URL("../../", import.meta.url));
  for (const [target, arch] of [["darwin", "arm64"], ["windows", "amd64"]]) {
    const result = spawnSync(process.execPath, ["scripts/release.mjs", target, arch], {
      cwd: root,
      encoding: "utf8",
      env: { ...process.env, GITHUB_REF_TYPE: "tag", GITHUB_REF_NAME: "v0.0.0" },
    });
    assert.equal(result.status, 1, result.error?.message);
    assert.match(result.stderr, /Release tag v0\.0\.0.*expected v/);
    assert.equal(result.stdout, "");
  }
});
