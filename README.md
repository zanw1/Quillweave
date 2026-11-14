# Quillweave

A local fiction-writing workspace for planning stories, writing chapters, and keeping characters and story knowledge consistent. Built with Go, Wails 3, React, and TypeScript. Supports English and 简体中文.

**0.6.1 · Development preview**

[Releases](https://github.com/zanw1/Quillweave/releases) · [MCP and skills](#mcp-and-skills) · [CLI](#cli) · [Build from source](#build-from-source)

## What it does

- Organize books, volumes, chapters, and scene cards, then write and revise in the chapter editor.
- Work with an AI creative partner using conversation-specific models, skills, search, and story context.
- Maintain characters, world settings, maps, and a source-linked Wiki with reviewed facts and chapter summaries.
- Review proposed changes, confirm saves, recover earlier versions, and resolve conflicts with later edits.
- Use external agents through MCP or the CLI for writing, Wiki compilation, and candidate confirmation.

## Screenshots

These screenshots use the bundled **Letters at Low Tide** demo in an isolated temporary workspace.

![Chapter editor and creative partner](docs/images/writing.png)

![Story settings and source-linked knowledge graph](docs/images/knowledge-graph.png)

## Install and start

Install a package from [Releases](https://github.com/zanw1/Quillweave/releases) when available, or build from source below.

| Platform | Package | Requirements |
| --- | --- | --- |
| macOS Apple Silicon | `Quillweave-<version>-macos-arm64.dmg` | macOS 13+ |
| Windows x64 | `Quillweave-<version>-windows-amd64.exe` | Windows 10/11 with WebView2 |

On macOS, open the DMG and drag the app into Applications. On Windows, open the EXE. ZIP packages include the console companion and agent skill. macOS preview packages use ad-hoc signing.

1. Create a story or open the built-in demo.
2. Set the story goals, add characters and world details, and create an outline.
3. Write offline, or configure a model provider in **Settings** for AI assistance.
4. Use the creative partner to discuss, draft, revise, or organize Wiki evidence. Review changes when the task uses confirmation mode.
5. Export chapters as Markdown or save a complete project backup.

The creative partner supports `/` commands, model selection, skills, and research controls. Writing tasks compose scenes, retrieve evidence, draft prose, and review the result. **Save by task** saves explicit creation requests and retains discussion suggestions for review. Wiki facts and summaries retain their source quotes and review decisions.

## MCP and skills

Connect **Codex, Claude Code, Cursor, or ZCode** to the same local workspace. Standard connections expose all **35 tools** for reading, writing, Wiki compilation, candidate review, confirmation, and recovery.

Set the console companion path on macOS.

```sh
QW="/Applications/Quillweave.app/Contents/MacOS/quillweave-cli"
# For a local build, use "$PWD/build/bin/Quillweave.app/Contents/MacOS/quillweave-cli".
```

Generate a configuration for your client, then merge its output into the matching configuration file.

```sh
"$QW" connect codex
"$QW" connect claude
"$QW" connect cursor
"$QW" connect zcode
```

| Client | MCP configuration | Skill directory |
| --- | --- | --- |
| Codex | `~/.codex/config.toml` | `~/.agents/skills/quillweave-novel/` |
| Claude Code | Project `.mcp.json`, or `claude mcp add` | `~/.claude/skills/quillweave-novel/` |
| Cursor | `~/.cursor/mcp.json` | `~/.cursor/skills/quillweave-novel/` |
| ZCode | `~/.zcode/cli/config.json` | `~/.zcode/skills/quillweave-novel/` |

Copy `agent-skills/quillweave-novel/` from the package into your client's skill directory, then reload the connection and skills. The source skill is in [integrations/skills/quillweave-novel/SKILL.md](integrations/skills/quillweave-novel/SKILL.md). Tool descriptions and the skill are in English. ZCode's generated configuration includes `protocolVersion=legacy` for its connection health checks.

Connections choose a work through `qw_list_projects`. Add `--project PROJECT_ID` when generating a configuration to bind one work, or `--read-only` for its 16 reading tools.

For example, ask your agent to continue a chapter, revise its pacing, or review the first three chapters' Wiki. The agent reads the source, performs the creative work, and submits resumable stages. It can apply candidates after your approval and recover applied changes through a reviewed recovery candidate. The skill documents pagination, evidence hashes, retries, and the complete workflow.

## CLI

The console companion provides human-readable commands and a discoverable JSON API. On Windows, extract the ZIP and run `.\quillweave-cli.exe` from its folder.

```sh
"$QW" projects --json
"$QW" status --project PROJECT_ID --json
"$QW" api list --json
"$QW" api describe wiki_begin --json
"$QW" api call project_overview --creator --project PROJECT_ID --json
```

Structured calls accept a JSON file or standard input with `--input -`. Use `--db /path/to/quillweave.sqlite3` for a different workspace, including browser-preview data.

```sh
printf '%s\n' '{"source_id":"CHAPTER_ID","request_id":"wiki-001"}' |
  "$QW" api call wiki_begin --creator --project PROJECT_ID --client codex --input - --json
```

`--creator` uses the external-agent contract shared with MCP. Session mutations carry a stable `request_id` and the latest session revision. `writing begin|compose|draft|review|status|refresh|finish|abandon` provides shortcuts for writing stages. Author-side `agent`, `job`, `change`, `read`, `export`, and `import` commands are listed by `"$QW" --help`.

## Build from source

Requires Go 1.27+ and Node.js 22+. Native macOS builds also require Xcode Command Line Tools.

```sh
git clone https://github.com/zanw1/Quillweave.git
cd Quillweave
npm ci
npm run desktop
```

For a browser preview, run `npm run build` followed by `npm run backend` and open `http://127.0.0.1:1421`.

```sh
npm run check
node scripts/release.mjs --check
npm test
npm run check:go
npm run test:ui
npm run bundle
npm run bundle:windows
```

Native apps and console companions are written to `build/bin/`. `npm run release -- darwin arm64` and `npm run release -- windows amd64` produce distribution packages in `build/releases/`. Commit matching root, frontend and lockfile versions before tagging `v0.6.1`. The release workflow checks these versions before installing dependencies, then builds packages and prepares a GitHub draft. Maintained tests and runner instructions are in [test/README.md](test/README.md).

## Data and license

Stories, versions, conversations, and Wiki sessions are stored locally in SQLite. macOS uses `~/Library/Application Support/studio.quillweave.desktop/`. Windows uses Roaming AppData. Browser preview uses `data/preview/`, with `QUILLWEAVE_DATA_DIR` available for a separate workspace. AI and research requests send selected context to the configured providers. Model and search credentials are encrypted locally.

The Wiki draws on the source-preserving knowledge-maintenance ideas in [nashsu/llm_wiki](https://github.com/nashsu/llm_wiki). Quillweave uses the [MIT License](LICENSE). Dependency notices are included in [THIRD_PARTY_NOTICES.txt](THIRD_PARTY_NOTICES.txt) and distribution packages.
