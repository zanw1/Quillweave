import test from "node:test";
import assert from "node:assert/strict";
import { companionFilename } from "../../scripts/build.mjs";

/** Reject artifact names that would replace the desktop executable on a case-insensitive volume. */
function assertDistinctArtifacts(target, desktop) {
  assert.notEqual(companionFilename(target).toLocaleLowerCase("en-US"), desktop.toLocaleLowerCase("en-US"));
  assert.match(companionFilename(target), /^quillweave-cli(?:\.exe)?$/);
}

test("macOS companion retains the desktop executable", () => assertDistinctArtifacts("darwin", "Quillweave"));
test("Windows companion retains the desktop executable", () => assertDistinctArtifacts("windows", "Quillweave.exe"));
