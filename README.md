# 📝 Portanote

**A portable, single-binary notes app.** No installation, no admin rights, no Electron. Portanote is one small executable that serves a local web UI to your browser — your notes are plain Markdown files sitting right next to it, so your data is always yours.

![Portanote — folder tree, tags, and a split Markdown editor rendering a table, code, a wiki-link, and a Mermaid diagram](docs/screenshot.png)

<sub>A dark theme is built in too — [dark screenshot](docs/screenshot-dark.png).</sub>

---

## Why Portanote

- **Truly portable.** A ~10 MB binary you can run from a USB stick, a locked-down work laptop, or your home PC — nothing is installed and nothing touches the registry. Delete the folder and every trace is gone.
- **Your data is just files.** Every note is a `.md` file with YAML frontmatter. Back it up, sync it, grep it, or edit it in any other editor. No database, no lock-in.
- **Private by default.** It binds to `127.0.0.1` and talks to nothing on the internet. Nothing you write leaves your machine.

## Features

- **Standard-Notes-style three-pane UI** — a collapsible nested-folder tree and tags in the sidebar, a searchable note list, and a Markdown editor with live preview (edit / split / preview), dark mode, starring, trash, and drag-resizable panes.
- **GitHub-Flavored Markdown editor** with a formatting toolbar (headings, bold/italic/strikethrough, lists, task lists, tables, code, links, images) and a built-in Markdown quick-reference (the `❔` button).
- **Rich rendering** — syntax highlighting for the common languages plus **PowerShell, Splunk SPL, Dockerfile, and nginx**, and **Mermaid diagrams** (```` ```mermaid ````) drawn live in the preview and in exports.
- **Wiki-links & backlinks** — `[[Note Title]]` (or `[[Title|alias]]`) links notes together, and each note shows a "Linked references" panel of what points to it.
- **Nested folders** — organize notes in a real `Work/Runbooks/AWS`-style tree, with drag-and-drop, subtree counts, and collapsible groups.
- **Fast search with operators** — an in-memory full-text index (search-as-you-type), plus `tag:`, `folder:`, `is:starred` / `is:untagged` / `is:trashed`, `after:`, and `before:`.
- **Auto-tag suggestions** — an offline pass over your note titles and headers proposes topic tags under the tag row; one click accepts. Nothing is sent anywhere.
- **A standalone To-Do list** — add / complete / reorder (drag) / delete tasks and clear completed ones. The `☑` button in a note's toolbar creates a task linked back to that note.
- **Note templates** — a `templates/` folder of reusable skeletons (Meeting Notes, Runbook, Daily Log to start), from the `▾` beside the new-note button.
- **Paste-to-attach** — paste a screenshot straight into a note; it's saved under `attachments/` and inserted as an image.
- **PDF export, two ways** — a built-in "Print / Save as PDF" that works everywhere, and a true **Eisvogel** LaTeX export (optional portable tools) with a toggleable title page and table of contents.
- **Automatic backups** — zips your whole notes folder on a schedule (default every 3 hours, keep the last 12), adjustable in the ⚙ settings with a "Back up now" button.
- **Multi-select bulk actions** — Ctrl/Shift-click notes to move, star, or trash them together.

---

## Download & run

Grab the binary for your machine from the **[Releases page](https://github.com/jake-kelley/portanote/releases/latest)** (direct links below), drop it anywhere, and run it. Your browser opens to the app, and a `notes/` folder is created next to the binary.

| Platform | Download |
|----------|----------|
| **Windows** (64-bit) | [`portanote-windows-amd64.exe`](https://github.com/jake-kelley/portanote/releases/latest/download/portanote-windows-amd64.exe) |
| **macOS** (Apple Silicon / M-series) | [`portanote-macos-arm64`](https://github.com/jake-kelley/portanote/releases/latest/download/portanote-macos-arm64) |

> This repository is private, so those download links require you to be signed in to GitHub. If you'd rather download without signing in, make the repo public in Settings → General → Danger Zone.

### Windows

Double-click `portanote-windows-amd64.exe`. Your browser opens at `http://127.0.0.1:8737`; notes live in a `notes\` folder beside the exe. SmartScreen may warn about an unrecognized app — choose **More info → Run anyway** (it's an unsigned binary you built/downloaded, not malware).

### macOS (Apple Silicon)

Copy the binary anywhere (USB stick, `~/Documents`, …), then in Terminal:

```sh
chmod +x portanote-macos-arm64
xattr -d com.apple.quarantine portanote-macos-arm64   # only if it was downloaded via a browser
./portanote-macos-arm64
```

If Gatekeeper still objects ("cannot be opened because it is from an unidentified developer"), go to **System Settings → Privacy & Security → Open Anyway**, or right-click the binary → Open. Nothing is installed.

### Command-line flags

| Flag | Purpose |
|------|---------|
| `-dir path/to/notes` | Use a specific notes folder (default: `notes/` next to the binary) |
| `-port 8737` | Port to listen on (walks upward if busy) |
| `-no-browser` | Don't open a browser on start |
| `-host 127.0.0.1` | Bind address. `127.0.0.1` = localhost only (default). `0.0.0.0` = whole network. `subnet` = whole network but only accept clients on this device's local subnet (auto-detected), everything else gets `403`. |

Reaching it from your phone: run with `-host subnet` and open the `http://<this-device-ip>:<port>` URL it prints, on the same Wi-Fi. There's no password on the served notes, so only do this on networks you trust — and note the OS firewall may still need to allow the port.

---

## True Eisvogel PDF export (optional)

The built-in **Print / Save as PDF** works with zero setup. For polished LaTeX PDFs, drop two portable tools next to the binary:

```powershell
# Windows
powershell -ExecutionPolicy Bypass -File scripts\get-tools.ps1
```
```sh
# macOS
sh scripts/get-tools.sh
```

This downloads [pandoc](https://github.com/jgm/pandoc) and [tectonic](https://tectonic-typesetting.github.io/) (a self-contained LaTeX engine) into a `tools/` folder — no installation. The app detects them (the sidebar badge flips to **PDF: eisvogel**) and the export menu's Eisvogel options enable, with checkboxes for a front title page and a table of contents. The first export downloads ~100 MB of LaTeX packages into `tools/tectonic-cache/` and takes a few minutes; after that it's fast and works offline.

---

## Using Portanote

### Markdown

Portanote renders GitHub-Flavored Markdown. The `❔` toolbar button opens a full cheat sheet; the highlights:

| Syntax | Result |
|--------|--------|
| `# H1` · `## H2` … | Headings |
| `**bold**` · `*italic*` · `~~strike~~` | Emphasis |
| `- [ ] todo` · `- [x] done` | Task-list checkboxes |
| `` `code` `` and fenced ```` ```lang ```` blocks | Inline / block code with syntax highlighting |
| `\| a \| b \|` + `\|---\|---\|` | Tables |
| `[[Note Title]]` | Wiki-link to another note |
| ```` ```mermaid ```` | A Mermaid diagram |
| `[text](url)` · `![alt](path)` | Links & images (or just paste a screenshot) |

### Folders

Folders are `/`-separated paths (`Work/Runbooks/AWS`) shown as a collapsible tree. The `+` by the *Folders* header makes a new one (use `/` to nest); hover a folder for a `＋` (add subfolder) and `✕` (delete). Double-click to rename. Drag notes from the list onto a folder to move them. Deleting a folder makes its notes uncategorized — it never deletes notes.

### Tags & suggestions

Add tags in the tag row under a note's title. As you write, a **✨ Suggested** row proposes tags derived from the note's title and headers — click one to accept. Suggestions are computed locally; nothing leaves your machine.

### Search

Type in the search box for instant full-text results. Combine free text with operators:

```
tag:aws folder:Work/Runbooks is:starred after:2026-06-01 firewall
```

Supported: `tag:`, `folder:`, `is:starred`, `is:untagged`, `is:trashed`, `after:YYYY-MM-DD`, `before:YYYY-MM-DD`.

### To-Do

Open the **To-Do** view for a standalone task list — independent of your notes. Type in the box and press Enter to add a task; check it off to complete; drag the `⠿` handle to reorder; hover for the `✕` to delete; and use **Clear completed** to wipe finished ones. From any note, the toolbar `☑` button creates a task titled after that note and linked to it — the task shows a 🔗 chip you can click to jump back to the note.

### Templates

Files in the `templates/` folder become reusable note skeletons, offered by the `▾` next to the new-note button. Three starters (Meeting Notes, Runbook, Daily Log) are created on first run — edit or delete them freely, or add your own `.md` files.

### Backups & settings (⚙)

The gear in the sidebar footer opens settings. Automatic backups zip the whole notes folder into `backups/` on your chosen interval, keeping the last *N* (the `backups/` folder itself is excluded). "Back up now" runs one immediately.

### Keyboard shortcuts

| Keys | Action |
|------|--------|
| `Ctrl/⌘ + Alt + N` | New note |
| `Ctrl/⌘ + K` | Focus search |
| `Ctrl/⌘ + S` | Save now (autosave runs anyway, ~0.6 s after you stop typing) |
| `Ctrl/⌘ + E` | Cycle edit / split / preview |
| `Ctrl/⌘ + B` · `I` · `` ` `` · `L` | Bold · italic · code · link (in the editor) |
| `Esc` | Clear search / close a dialog |

---

## Your data

Everything lives in your notes folder:

```
notes/
├── 05JULY2026-guardduty-ec2-quarantine.md   # one file per note
├── attachments/                              # pasted images
├── templates/                                # note templates
├── backups/                                  # automatic backup zips
├── .portanote-folders.json                   # folder tree (incl. empty folders)
├── .portanote-tasks.json                     # your to-do list
└── .portanote-settings.json                  # backup settings
```

Notes are named **`DDMONTHYYYY-title-slug.md`** — e.g. a note titled *Test Deployment* created on 5 July 2026 becomes `05JULY2026-test-deployment.md`. Rename the title and the file follows (the date stays as the created date); a stable `id` in the frontmatter keeps everything linked, so renames never break wiki-links or task links.

```markdown
---
id: "20260705-043521-2f04bc"
title: "AWS GuardDuty Runbook"
folder: "Work/Runbooks/AWS"
tags: [aws, security]
starred: true
trashed: false
created: 2026-07-05T04:35:21Z
updated: 2026-07-05T04:35:21Z
---

# Containment
...
```

Files without frontmatter are adopted as-is (the title comes from the first heading, timestamps from the file), so you can drop an existing Markdown folder in and it just works. Trash is a flag, not a folder — "Delete forever" in the Trash view is what actually removes a file.

---

## Build from source

Portanote is pure-stdlib Go (no dependencies to fetch); the UI and the Eisvogel template are embedded via `go:embed`, so the output is a single self-contained binary.

```powershell
# Windows — builds both targets into dist\
powershell -ExecutionPolicy Bypass -File scripts\build.ps1
```
```sh
# any platform
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/portanote-windows-amd64.exe .
GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w" -o dist/portanote-macos-arm64 .
```

Releases are built automatically: pushing a `vX.Y.Z` tag runs `.github/workflows/release.yml`, which cross-compiles the binaries and attaches them to a GitHub Release.

```
portanote/
├── main.go · api.go · notes.go · search.go · export.go · tasks.go · backups.go · templates.go
├── ui/            # embedded frontend (vanilla JS + marked / DOMPurify / highlight.js / mermaid)
├── pandoc/        # eisvogel.latex (embedded at build)
├── scripts/       # get-tools.ps1 · get-tools.sh · build.ps1
├── docs/          # screenshots
└── .github/workflows/release.yml
```

## Search: why a lexical index and not a vector DB

Semantic search would need an embedding model (~100 MB plus a native runtime per platform), which fights the single-binary, no-install promise — and for note lookup a well-tuned lexical index is usually *better*: exact terms, prefixes as you type, title/tag boosting, and zero latency. If semantic search ever becomes worth it, the API already returns scored results, so a local embedding sidecar can merge in without changing the UI.

---

> Not affiliated with or endorsed by any of the tools it interoperates with. Verify anything security-, pricing-, or compliance-related against primary sources.
