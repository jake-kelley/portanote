---
type: Project Documentation
title: Portanote
description: A portable, single-binary Markdown notes app — what it is, how to run it on Windows and macOS, and where the deeper guides live.
resource: https://github.com/jake-kelley/portanote
tags: [notes, markdown, go, portable, self-hosted, mcp, claude]
timestamp: 2026-07-20T20:15:00-06:00
---

<h1 align="center">
  <img src="docs/logo.png" alt="" width="80"><br>
  Portanote
</h1>

**A portable, single-binary Markdown notes app.** One ~11 MB executable serves a local web UI in your browser. Your notes are plain `.md` files in a folder next to it — no installation, no admin rights, no database, no cloud.

![Portanote — the sidebar's folder tree and tags, a split Markdown editor rendering a table, code, and a Mermaid diagram, and the Ask Claude panel summarizing the open note](docs/screenshot.png)

<sub>A dark theme is built in — [dark screenshot](docs/screenshot-dark.png).</sub>

## Why Portanote

- **Runs anywhere.** From a USB stick, a locked-down work laptop, your home PC. Nothing touches the registry; delete the folder and every trace is gone (the only thing the optional install script adds outside it is a login launcher, removed with `-Uninstall`).
- **Your notes are just files.** Markdown with YAML frontmatter, in real directories. Back them up, sync them, grep them, edit them in any other tool — Portanote picks up outside changes with one click. No lock-in, ever.
- **Private by default.** Binds to `127.0.0.1` and talks to nothing on the internet unless you ask it to (checking for updates, or the optional Ask Claude panel).

## What's in the box

| | |
|---|---|
| **Write** | GitHub-Flavored Markdown with live preview, syntax highlighting, Mermaid diagrams, and a formatting toolbar. Paste a screenshot straight into a note. |
| **Connect** | `[[Wiki-links]]` between notes, with a backlinks panel on every note. Note templates for recurring structures. |
| **Organize** | Real nested folders (they're directories on disk), tags with offline auto-suggestions, stars, trash, drag-and-drop, bulk actions. |
| **Find** | Instant full-text search with operators: `tag:aws folder:Work is:starred after:2026-06-01 firewall`. |
| **Track** | A standalone To-Do list; the `☑` button turns a note into a linked task. |
| **Export** | Print / Save as PDF built in; optional typeset LaTeX PDFs ([below](#nicer-pdfs-optional)). |
| **Protect** | Automatic zip backups of the whole notes folder, on your schedule. |
| **Automate** | A built-in [MCP server](docs/mcp.md) for AI tools, the optional [Ask Claude panel](docs/ask-claude.md), and one-click verified [in-app updates](docs/custom-updates.md). |

## Get started

One command installs everything — it downloads the latest release, verifies its sha256 checksum, deploys to `Documents/portanote/`, starts the app, and sets it to start quietly at every login:

```powershell
# Windows (PowerShell)
iwr -useb https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/install.ps1 | iex
```

```sh
# macOS (Terminal, Apple Silicon)
curl -fsSL https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/install.sh | sh
```

Your browser opens `http://127.0.0.1:8737` — bookmark it. Notes live in `Documents/portanote/notes/`. Already downloaded a binary? Run the command from its folder and that binary is used instead. Autostart is undoable anytime (`-Uninstall` / `--uninstall`) and every step is detected or overridable — [docs/autostart.md](docs/autostart.md) has the details.

### Prefer to run it by hand?

The binary is fully portable — no setup, run it from anywhere (USB stick included) and a `notes/` folder appears next to it. Download it from the **[latest release](https://github.com/jake-kelley/portanote/releases/latest)**:

- **Windows:** double-click [`portanote-windows-amd64.exe`](https://github.com/jake-kelley/portanote/releases/latest/download/portanote-windows-amd64.exe); your browser opens `http://127.0.0.1:8737`. If SmartScreen warns: **More info → Run anyway** (it's an unsigned binary, not malware).
- **macOS:** download [`portanote-macos-arm64`](https://github.com/jake-kelley/portanote/releases/latest/download/portanote-macos-arm64), then in Terminal, in the binary's folder: `chmod +x portanote-macos-arm64 && xattr -d com.apple.quarantine portanote-macos-arm64 && ./portanote-macos-arm64`. If Gatekeeper objects: **System Settings → Privacy & Security → Open Anyway**.

### After that

- **Stay current:** **⚙ Settings → Check for updates** downloads the new release, verifies its checksum, swaps the binary in place, and restarts — notes and settings untouched. (You can also [update from your own fork or GitLab](docs/custom-updates.md).)
- **Use it from your phone:** run with `-host subnet` and open the printed `http://<device-ip>:8737` on the same Wi-Fi. There's no password, so trusted networks only.

Flags, all optional:

| Flag | Purpose |
|------|---------|
| `-dir path` | Notes folder (default: `notes/` next to the binary) |
| `-port 8737` | Port (walks upward if busy) |
| `-no-browser` | Don't open a browser on start |
| `-host 127.0.0.1` | Bind address: `127.0.0.1` (default), `0.0.0.0`, or `subnet` |

## Nicer PDFs (optional)

**Export → Print / Save as PDF** works with zero setup. For typeset LaTeX PDFs — the [Eisvogel](https://github.com/Wandmalfarbe/pandoc-latex-template) template, with optional title page and table of contents — Portanote needs two portable tools, [pandoc](https://github.com/jgm/pandoc) and [tectonic](https://tectonic-typesetting.github.io/), in a `tools/` folder next to the binary. One command downloads them (no repo checkout, no installation) — run it **from the folder that holds the binary** (after the one-command install, that's `Documents/portanote`):

```powershell
# Windows
iwr -useb https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/get-tools.ps1 | iex
```

```sh
# macOS
curl -fsSL https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/get-tools.sh | sh
```

(Both scripts are also attached to each [release](https://github.com/jake-kelley/portanote/releases/latest) if you'd rather download than pipe from the web.) The sidebar badge flips to **PDF: eisvogel** and the Eisvogel export options enable. The first export downloads ~100 MB of LaTeX packages into `tools/tectonic-cache/` and takes a few minutes; after that it's fast and works offline.

## Everyday use

- **Markdown help** lives behind the `❔` toolbar button — a full cheat sheet, including `[[Note Title]]` wiki-links, task-list checkboxes, tables, and ` ```mermaid ` diagrams.
- **Folders** are `/`-separated paths (`Work/Runbooks/AWS`). `+` creates (use `/` to nest); hover a folder for rename (✎), add-subfolder (＋), and delete (✕); drag notes onto folders to move them. Deleting a folder never deletes notes — they become uncategorized.
- **Sync with disk (⟳):** reorganize notes with Finder, Explorer, git, whatever — then click `⟳` in the sidebar and Portanote re-indexes everything.
- **Tags:** add them under the note title; a ✨ row suggests tags from the note's own headings, computed locally. ([AI suggestions](docs/ask-claude.md) are opt-in.)
- **Search scoping:** with a folder selected, search covers that folder and its subfolders; a bar under the box flips the query to all notes.
- **Backups (⚙):** automatic zips into `backups/` (default every 3 h, keep 12), plus "Back up now".

| Keys | Action |
|------|--------|
| `Ctrl/⌘ + Alt + N` | New note |
| `Ctrl/⌘ + K` | Focus search |
| `Ctrl/⌘ + S` | Save now (autosave runs anyway) |
| `Ctrl/⌘ + E` | Cycle edit / split / preview |
| `Ctrl/⌘ + B` · `I` · `` ` `` · `L` | Bold · italic · code · link |
| `Esc` | Clear search / close dialog |

## Your data

The folder tree in the app **is** the directory tree on disk:

```
notes/
├── 05JULY2026-guardduty-ec2-quarantine.md   # one file per note
├── Work/
│   └── Runbooks/
│       └── 03JULY2026-aws-guardduty.md      # the note's folder = its directory
├── attachments/                 # pasted images
├── templates/                   # note templates
├── backups/                     # automatic backup zips
├── .portanote-tasks.json        # your to-do list
└── .portanote-settings.json     # app settings
```

Notes are named `DDMONTHYYYY-title-slug.md` from their creation date and title; rename or move a note and the file follows. A stable `id` in the frontmatter means renames and moves never break wiki-links or task links.

```markdown
---
id: "20260705-043521-2f04bc"
title: "AWS GuardDuty Runbook"
tags: [aws, security]
starred: true
trashed: false
created: 2026-07-05T04:35:21Z
updated: 2026-07-05T04:35:21Z
---

# Containment
...
```

Three things make the folder genuinely shareable with other tools:

- **Drop in existing Markdown and it just works.** Files without frontmatter are adopted as-is — title from the first heading, folder from the directory, timestamps from the file.
- **Frontmatter Portanote doesn't recognize is left alone.** An Obsidian property, a Hugo `draft:`, your own script's field — preserved word for word on every save.
- **Trash is a flag, not a folder.** Nothing is permanently deleted except "Delete forever" in the Trash view.

## AI, if you want it

Both are optional, local-first, and off until you connect them:

- **Ask Claude** — with the [Claude Code CLI](https://claude.com/claude-code) installed, a `✳` panel chats about the open note: summarize, improve, extract tasks into the To-Do list, suggest tags. It gets Portanote's note tools only — no shell, no file access. [Details, settings & privacy →](docs/ask-claude.md)
- **MCP server** — every running Portanote serves `http://127.0.0.1:8737/mcp`, so Claude Code, Claude Desktop, or any MCP client can search, read, and edit your notes. One command to connect. [Details & tool list →](docs/mcp.md)

## Build from source

Pure-stdlib Go; the UI and PDF template are embedded, so the output is a single self-contained binary:

```sh
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/portanote-windows-amd64.exe .
GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w" -o dist/portanote-macos-arm64 .
```

(or `powershell -File scripts\build.ps1` on Windows, which builds both). Pushing a `vX.Y.Z` tag makes `.github/workflows/release.yml` build and publish a release. Design rationale lives in [docs/design-notes.md](docs/design-notes.md); release history in [log.md](log.md).

---

> Not affiliated with or endorsed by any of the tools it interoperates with. Verify anything security-, pricing-, or compliance-related against primary sources.
