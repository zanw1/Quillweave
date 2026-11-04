# Quillweave

A local fiction-writing workspace with AI story planning, chapter editing, a source-linked Character Wiki, and an interactive knowledge graph.

Quillweave uses Go, Wails 3, React, and TypeScript. The interface supports English and Simplified Chinese, with English selected by default.

[Download releases](https://github.com/zanw1/Quillweave/releases) · [Build from source](#build-from-source) · [Publish a release](#publish-a-release) · [MIT license](LICENSE)

## What you can do

- Write chapters offline, set chapter length goals, and restore earlier versions.
- Discuss a story with AI, generate an outline from the conversation, and review it before applying it to a draft.
- Maintain characters, places, factions, items, relationships, and clues with quotes linked to their source passages.
- Explore a knowledge graph, filter by chapter or entry type, and inspect the evidence behind a connection.
- Compile source evidence into story memory, review the proposed knowledge, and check for stale sources or unresolved relationships.
- Assign independent model profiles to planning, writing, review, Wiki extraction, and memory compilation.
- Import older Quillweave projects and UTF-8 TXT or Markdown sources, and export Markdown or a complete JSON project backup.

## Interface

These screenshots show the built-in **Letters at Low Tide** sample. Its story and demonstration evidence are supplied with the app. Model settings show a credential-free example configuration.

### Chapter writing

Chapter navigation, the prose editor, version controls, and a writing assistant share one workspace.

![Chapter editor showing the sample manuscript, chapter navigation, and writing assistant](docs/images/writing.png)

### Story knowledge graph

Select a character or connection to inspect its description, related entries, and quoted evidence. Solid and dashed relationships distinguish confirmed and pending evidence.

![Character Wiki knowledge graph with Lin Wu selected and source evidence visible](docs/images/knowledge-graph.png)

<details>
<summary>Project home and model settings</summary>

### Project home

Open a project, import an older work, or explore the built-in sample.

![Project home showing the built-in sample and project import controls](docs/images/workspace.png)

### Model settings

Choose a provider preset, edit its endpoint and model, and assign profiles to individual tasks.

![Model profile settings showing an OpenAI preset and editable connection parameters](docs/images/model-settings.png)

</details>

## Download and run

Open [Releases](https://github.com/zanw1/Quillweave/releases) and choose the download for your platform.

| Platform | Primary download | Requirements |
|---|---|---|
| macOS Apple Silicon | `Quillweave-<version>-macos-arm64.dmg` | macOS 13 or later |
| Windows x64 | `Quillweave-<version>-windows-amd64.exe` | Windows 10/11 and [Microsoft Edge WebView2 Runtime](https://developer.microsoft.com/en-us/microsoft-edge/webview2) |

On Windows, download the EXE and open it directly. On macOS, open the DMG and drag `Quillweave.app` to the Applications shortcut. A macOS `.app` is a bundle directory, so the disk image carries its complete structure in one downloadable file.

Optional ZIP packages are also available. The DMG and ZIP packages include the project license and third-party notices. The standalone Windows EXE embeds those notices as resources. `LICENSE.txt`, `THIRD_PARTY_NOTICES.txt`, and `SHA256SUMS.txt` are also attached to the release.

The current desktop packages are preview builds. macOS uses ad-hoc signing. Developer ID signing and Apple notarization are pending. For first-run approval of a downloaded preview, follow [Apple's app-opening instructions](https://support.apple.com/en-us/102445). Local Windows package verification covers cross-compilation. The release workflow is configured to build on a Windows runner, with interactive Windows acceptance still pending. Linux builds require their own GTK/WebKit toolchain.

## First steps

1. Open the sample, or select **Start a new story**.
2. Set the premise, writing style, viewpoint, and default chapter length in **Story planning**.
3. Write offline, or open **Model settings** to add a provider preset, enter an API key when required by the provider, and choose a default model or task-specific profiles. Local Ollama can use an empty key.
4. In Story planning, discuss your ideas and choose **Generate outline from discussion**. Edit the proposal in **Review outline**, use it as an outline draft, and save the story bible.
5. In a chapter, use **Compile Wiki** to extract evidence and compile story memory. Review the quotes and their interpretation before confirming them.

Use the language selector in the top bar to switch between **English** and **中文**. The selection is saved for this browser or desktop WebView. Switching languages retains the current page and unsaved drafts. Application-owned AI instructions use English, and an explicit author language request controls creative output.

### Models and privacy

The preset catalog covers OpenAI, Anthropic, Google Gemini, DeepSeek, Alibaba Cloud Bailian, Volcengine Ark, Zhipu, Moonshot/Kimi, MiniMax, xAI, and local Ollama. The app supports Chat Completions, Responses, Anthropic Messages, Gemini Interactions, Gemini Generate Content, and Ollama Chat.

Model selection follows **project override → global task route → default profile**. Wiki extraction and memory compilation can use different models. Connection tests send a short request and may incur provider charges.

API keys are held in the current app process. Enter them again after restart. `QUILLWEAVE_API_KEY` can supply the default profile's key at startup. Persistent settings and exported project archives redact credentials. AI requests send the selected story context to the configured provider. Some protocols also retain private continuation context locally for compatible follow-up requests.

### Story knowledge

Wiki compilation extracts evidence from the complete source in chunks, then compiles story memory in a second stage. Both stages must succeed before publication to the local project. Evidence and memory have separate author review decisions. Changes to the source, tracking focus, models, or evidence determine cache reuse and knowledge freshness.

A matching quote establishes that a passage exists. Authors review the interpretation, entity attribution, and narrative meaning.

## Build from source

Use Go 1.27 or later and Node.js 22 or later. Native macOS builds also require Xcode Command Line Tools. Windows runtime requires WebView2.

```sh
git clone https://github.com/zanw1/Quillweave.git
cd Quillweave
npm ci
npm run desktop
```

The desktop development command builds frontend assets, starts Vite, and restarts the Go window after backend edits.

For a browser preview, run the following commands and open `http://127.0.0.1:1421`.

```sh
npm run build
npm run backend
```

For frontend hot reload, run `npm run dev` in another terminal and open `http://127.0.0.1:1420`.

### Checks and local packages

```sh
npm run check
npm test
npm run test:race
npm run check:go
npm run bundle
npm run bundle:windows
```

Native applications are written to `build/bin/`. To create the DMG, standalone Windows EXE, optional ZIP packages, and checksums, run these commands on macOS.

```sh
npm run release -- darwin arm64
npm run release -- windows amd64
```

On Windows, run `npm run release -- windows amd64`. macOS applications are built on macOS. Release files are written to `build/releases/`. Temporary packaging files are cleaned on success or failure.

## Publish a release

<details>
<summary>First-time GitHub repository setup</summary>

After reviewing the license and files selected for publication, run the following commands from the project root with GitHub CLI signed in to the repository owner account.

```sh
git init -b main
git add .
git diff --cached --stat
git commit -m "Initial open-source release"
gh repo create zanw1/Quillweave --public --source=. --remote=origin --push
```

</details>

The release workflow builds macOS arm64 and Windows amd64 packages after a version tag is pushed. It runs the configured checks, creates the DMG and standalone EXE, calculates SHA-256 checksums, and creates a **draft prerelease** with these files, optional ZIP packages, and license notices attached.

The tag must match `package.json`. The current version uses `v0.3.0`.

```sh
git tag -a v0.3.0 -m "Quillweave v0.3.0"
git push origin v0.3.0
```

Open the repository's **Actions** tab and wait for **Build release** to finish. Open **Releases**, review the draft's notes and assets, test the downloaded applications, and select **Publish release**. A workflow rerun can replace assets on the draft. Published releases require a new version.

For subsequent versions, update the root and frontend package versions and the lockfile together. Refresh `THIRD_PARTY_NOTICES.txt` when dependencies change, commit the reviewed changes, and push the matching version tag. The workflow uses the repository's automatically supplied `GITHUB_TOKEN` with release-write permission confined to the final upload job. [GitHub release documentation](https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository)

Git ignores `build/bin/`, `build/releases/`, `data/`, `node_modules/`, `frontend/dist/`, `.env`, `.env.*`, and `参考/`. The `.env.example` template is allowed. Review staged files before pushing, including any manuscripts or exported backups saved elsewhere. Commit source, manifests, license notices, the workflow, and the README screenshots.

### Release acceptance

| Input or check | Expected result |
|---|---|
| Clean checkout and `npm ci` | Dependencies install from the lockfile |
| `npm run build` | Production frontend is available for Go embedding |
| Type checks, Go tests, and vet | All configured checks pass |
| macOS arm64 packaging | DMG contains the signed app, Applications shortcut, and license notices, with a ZIP also provided |
| Windows amd64 packaging | Standalone GUI EXE embeds license notices, with a ZIP also provided |
| `SHA256SUMS.txt` verification | Every listed release file matches its digest |
| Tag differs from the package version | Packaging stops before building |
| A platform build fails | Draft release job waits for both successful builds |
| Rerun an existing draft tag | Draft assets are replaced with the successful build |
| Rerun a published tag | Existing published assets are protected |

## Project structure

```text
frontend/               React interface, language pack, embedded assets
internal/core/          Models, SQLite storage, AI jobs, Wiki, graph
internal/desktop/       Wails command bridge and native file dialogs
internal/preview/       Loopback HTTP service
internal/platform/      Desktop data locations
cmd/                    Desktop, preview, and Windows resource entry points
scripts/                Development, build, and release packaging
docs/images/            README screenshots
.github/workflows/      Release automation
```

## Local data and backups

Desktop data uses the identifier `studio.quillweave.desktop`. macOS stores projects in `~/Library/Application Support/studio.quillweave.desktop/quillweave.sqlite3`. Windows uses Roaming AppData, and Linux uses its XDG data location. Browser preview defaults to `data/preview/`. `QUILLWEAVE_DATA_DIR` and `QUILLWEAVE_PORT` override the preview data directory and port.

Project backups retain prose, setup, evidence, versions, writing parameters, and project model references. Global profiles, credentials, uncommitted drafts, and AI history are managed separately. Older project imports receive compatible field defaults.

Chapter targets use non-whitespace Unicode characters. Context budgets use Unicode characters, and provider output budgets use tokens. The historical `*_words` JSON field names retain their established character-count unit.

## Acknowledgments and license

The embedded Wiki draws on the source-preserving knowledge-maintenance ideas in [nashsu/llm_wiki](https://github.com/nashsu/llm_wiki). The writing-method cards include links to their original sources.

Quillweave is released under the [MIT License](LICENSE). Dependency copyright and license texts are collected in [THIRD_PARTY_NOTICES.txt](THIRD_PARTY_NOTICES.txt) and included in packaged releases.
