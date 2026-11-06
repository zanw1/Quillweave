import fs from "node:fs";
import path from "node:path";

/** Enforce the author's module-size limit on maintained sources without inspecting dependencies or artifacts. */
function inspect(directory) {
  if (path.normalize(directory) === path.normalize("test/archive")) return [];
  const failures = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const filename = path.join(directory, entry.name);
    if (entry.isDirectory()) failures.push(...inspect(filename));
    else if (/\.(?:go|[cm]?jsx?|tsx?|css|py|html|sh|rs)$/.test(filename)) {
      const content = fs.readFileSync(filename, "utf8");
      const count = content.split("\n").length - Number(content.endsWith("\n"));
      if (count > 1000) failures.push(`${filename}: ${count} lines`);
    }
  }
  return failures;
}

const failures = ["frontend/src", "internal", "cmd", "scripts", "test"].flatMap(inspect);
if (failures.length) {
  process.stderr.write(`Source files must not exceed 1000 lines:\n${failures.join("\n")}\n`);
  process.exitCode = 1;
} else process.stdout.write("Source-size check passed (1000-line limit).\n");
