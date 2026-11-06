import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const metadata = JSON.parse(
  fs.readFileSync(path.join(root, "package.json"), "utf8"),
);

/** Run build commands with argument arrays and stop when a child fails. */
function run(command, args, env = process.env) {
  const result = spawnSync(command, args, {
    cwd: root,
    env,
    stdio: "inherit",
    shell: process.platform === "win32" && command.endsWith(".cmd"),
  });
  if (result.error) throw result.error;
  if (result.status !== 0)
    throw new Error(
      `${command} build failed (${result.status ?? result.signal})`,
    );
}

/** Read native Go toolchain properties independently of the target platform. */
export function goEnv(name) {
  const result = spawnSync("go", ["env", name], {
    cwd: root,
    encoding: "utf8",
  });
  if (result.status !== 0)
    throw new Error(result.stderr || "Unable to read the Go environment");
  return result.stdout.trim();
}

/** Use a consistent macOS deployment target for compilation and linking. */
export function nativeEnvironment(target, arch) {
  const env = {
    ...process.env,
    GOOS: target,
    GOARCH: arch,
    CGO_ENABLED: target === "windows" ? "0" : "1",
  };
  if (target === "darwin") {
    env.MACOSX_DEPLOYMENT_TARGET = "13.0";
    // Select the Go host architecture explicitly when a Rosetta parent launches the compiler.
    const compilerArch = goEnv("GOHOSTARCH") === "arm64" ? "-arm64" : "-x86_64";
    env.CC ||= `/usr/bin/arch ${compilerArch} /usr/bin/clang`;
    env.CXX ||= `/usr/bin/arch ${compilerArch} /usr/bin/clang++`;
    for (const variable of ["CGO_CFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS"])
      env[variable] =
        `${process.env[variable] || ""} -mmacosx-version-min=13.0`.trim();
  }
  return env;
}

/** Package a native application with the existing bundle identifier and data location. */
function packageMac(binary, temporary, output) {
  const bundle = path.join(temporary, "Quillweave.app");
  const contents = path.join(bundle, "Contents");
  fs.mkdirSync(path.join(contents, "MacOS"), { recursive: true });
  fs.copyFileSync(binary, path.join(contents, "MacOS", "Quillweave"));
  fs.chmodSync(path.join(contents, "MacOS", "Quillweave"), 0o755);
  fs.writeFileSync(
    path.join(contents, "Info.plist"),
    `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleName</key><string>Quillweave</string>
<key>CFBundleDisplayName</key><string>Quillweave</string>
<key>CFBundleExecutable</key><string>Quillweave</string>
<key>CFBundleIdentifier</key><string>studio.quillweave.desktop</string>
<key>CFBundleVersion</key><string>${metadata.version}</string>
<key>CFBundleShortVersionString</key><string>${metadata.version}</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>`,
  );
  run("codesign", ["--force", "--deep", "--sign", "-", bundle]);
  fs.rmSync(output, {
    recursive: true,
    force: true,
    maxRetries: 10,
    retryDelay: 100,
  });
  fs.cpSync(bundle, output, { recursive: true });
}

/** Build frontend and Go assets while cleaning temporary resources and package directories. */
function build() {
  const host = goEnv("GOHOSTOS");
  const target = process.argv[2] || host;
  const arch =
    process.argv[3] || (target === "windows" ? "amd64" : goEnv("GOHOSTARCH"));
  if (
    !["darwin", "windows", "linux"].includes(target) ||
    !["amd64", "arm64"].includes(arch)
  )
    throw new Error("Unsupported build target");
  if (target !== "windows" && target !== host)
    throw new Error(
      `Build the ${target} desktop app on that platform. Windows supports cross-compilation.`,
    );
  if (!/^\d+\.\d+\.\d+$/.test(metadata.version))
    throw new Error(
      "The application version must contain three numeric components",
    );
  run(process.platform === "win32" ? "npm.cmd" : "npm", ["run", "build"]);
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "quillweave-build-"));
  const resources = path.join(
    root,
    "cmd",
    "quillweave",
    `quillweave_windows_${arch}.syso`,
  );
  let createdResource = false;
  try {
    const outputDirectory = path.join(root, "build", "bin");
    fs.mkdirSync(outputDirectory, { recursive: true });
    if (target === "windows") {
      if (fs.existsSync(resources))
        throw new Error(
          "An existing Windows resource file prevents this build",
        );
      const generated = path.join(temporary, "resources.syso");
      run(
        "go",
        [
          "run",
          "./cmd/buildresources",
          "-out",
          generated,
          "-arch",
          arch,
          "-version",
          metadata.version,
        ],
        {
          ...process.env,
          GOOS: host,
          GOARCH: goEnv("GOHOSTARCH"),
          CGO_ENABLED: "0",
        },
      );
      fs.copyFileSync(generated, resources);
      createdResource = true;
    }
    const binary = path.join(
      temporary,
      target === "windows" ? "Quillweave.exe" : "Quillweave",
    );
    const flags = `-s -w -X main.production=true${target === "windows" ? " -H windowsgui" : ""}`;
    run(
      "go",
      [
        "build",
        "-trimpath",
        "-tags",
        "production",
        "-ldflags",
        flags,
        "-o",
        binary,
        "./cmd/quillweave",
      ],
      nativeEnvironment(target, arch),
    );
    const output = path.join(
      outputDirectory,
      target === "darwin"
        ? "Quillweave.app"
        : `Quillweave-${target}-${arch}${target === "windows" ? ".exe" : ""}`,
    );
    if (target === "darwin") packageMac(binary, temporary, output);
    else {
      fs.copyFileSync(binary, output);
      if (target !== "windows") fs.chmodSync(output, 0o755);
    }
    console.log(`Built ${output}`);
  } finally {
    if (createdResource)
      fs.rmSync(resources, { force: true, maxRetries: 10, retryDelay: 100 });
    fs.rmSync(temporary, {
      recursive: true,
      force: true,
      maxRetries: 10,
      retryDelay: 100,
    });
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    build();
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
