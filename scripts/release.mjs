import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { goEnv } from "./build.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const { version } = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"));

/** Build the requested platform and package its application with distribution notices. */
function release() {
  const target = process.argv[2] || goEnv("GOHOSTOS");
  const arch = process.argv[3] || (target === "windows" ? "amd64" : goEnv("GOHOSTARCH"));
  if (!["darwin", "windows"].includes(target) || !["amd64", "arm64"].includes(arch))
    throw new Error("Release archives support macOS and Windows on amd64 or arm64");
  if (!/^\d+\.\d+\.\d+$/.test(version))
    throw new Error("The release version must contain three numeric components");
  if (process.env.GITHUB_REF_TYPE === "tag" && process.env.GITHUB_REF_NAME !== `v${version}`)
    throw new Error("The release tag must match the version in package.json");
  for (const file of ["LICENSE", "THIRD_PARTY_NOTICES.txt"])
    if (!fs.statSync(path.join(root, file)).isFile()) throw new Error(`Missing ${file}`);

  execFileSync(process.execPath, [path.join(root, "scripts/build.mjs"), target, arch], {
    cwd: root,
    stdio: "inherit",
  });

  const platform = target === "darwin" ? "macos" : "windows";
  const name = `Quillweave-${version}-${platform}-${arch}`;
  const outputDirectory = path.join(root, "build", "releases");
  fs.mkdirSync(outputDirectory, { recursive: true });
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "quillweave-release-"));
  const pending = path.join(outputDirectory, `.${name}.zip.tmp`);
  try {
    const staging = path.join(temporary, name);
    fs.mkdirSync(staging);
    const binary = target === "darwin" ? "Quillweave.app" : `Quillweave-windows-${arch}.exe`;
    fs.cpSync(path.join(root, "build", "bin", binary), path.join(staging, target === "darwin" ? binary : "Quillweave.exe"), { recursive: true });
    for (const file of ["LICENSE", "THIRD_PARTY_NOTICES.txt"])
      fs.copyFileSync(path.join(root, file), path.join(staging, file));
    const archive = path.join(temporary, `${name}.zip`);
    if (process.platform === "darwin") {
      execFileSync("ditto", ["-c", "-k", "--sequesterRsrc", "--keepParent", staging, archive]);
    } else if (process.platform === "win32") {
      execFileSync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", "Compress-Archive -LiteralPath $env:QUILLWEAVE_RELEASE_SOURCE -DestinationPath $env:QUILLWEAVE_RELEASE_ARCHIVE -CompressionLevel Optimal"], {
        env: { ...process.env, QUILLWEAVE_RELEASE_SOURCE: staging, QUILLWEAVE_RELEASE_ARCHIVE: archive },
        stdio: "inherit",
      });
    } else {
      execFileSync("zip", ["-q", "-r", archive, name], { cwd: temporary });
    }
    fs.copyFileSync(archive, pending);
    fs.renameSync(pending, path.join(outputDirectory, `${name}.zip`));
    const archives = fs.readdirSync(outputDirectory)
      .filter((file) => file.startsWith(`Quillweave-${version}-`) && file.endsWith(".zip"))
      .sort();
    const checksums = archives.map((file) => {
      const digest = createHash("sha256").update(fs.readFileSync(path.join(outputDirectory, file))).digest("hex");
      return `${digest}  ${file}`;
    });
    fs.writeFileSync(path.join(outputDirectory, "SHA256SUMS.txt"), checksums.join("\n") + "\n");
    console.log(`Release archive: ${path.join(outputDirectory, `${name}.zip`)}`);
  } finally {
    fs.rmSync(pending, { force: true });
    fs.rmSync(temporary, { recursive: true, force: true });
  }
}

try {
  release();
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
