# 📝 Portanote

A portable, Standard Notes–style markdown notes app in a **single ~7 MB binary**. No installation, no admin rights, no Electron — the binary serves a local web UI to your default browser, and your notes are plain `.md` files sitting next to it.

- **Three-pane UI** like Standard Notes: views + tags sidebar · searchable note list · markdown editor with live preview (edit / split / preview), dark mode, starring, trash.
- **Fast search-as-you-type**: in-memory BM25 full-text index with prefix matching and title/tag boosting, plus substring fallback. Instant at personal-collection scale.
- **Eisvogel PDF export**, two ways:
  1. **Built-in (zero dependencies):** an Eisvogel-styled print view → browser *Save as PDF*. Works everywhere, immediately.
  2. **True Eisvogel (pandoc):** drop portable `pandoc` + `tectonic` into `tools/` (script provided) and export real LaTeX PDFs — title page with the signature rule, running headers, listings code blocks, optional TOC.
- **Your data is just files.** `notes/*.md` with YAML frontmatter. Sync/back up with anything; drop existing `.md` files in and they appear on restart.

## Run it

### Windows
Double-click `portanote.exe` (or `dist\portanote-windows-amd64.exe`). Your browser opens at `http://127.0.0.1:8737`. Notes live in `notes\` next to the exe.

### macOS (Apple Silicon)
Copy `dist/portanote-macos-arm64` anywhere (USB stick, `~/Documents`, …), then in Terminal:

```sh
chmod +x portanote-macos-arm64
xattr -d com.apple.quarantine portanote-macos-arm64   # only needed if it was downloaded via a browser
./portanote-macos-arm64
```

If Gatekeeper still complains ("cannot be opened because it is from an unidentified developer"), go to **System Settings → Privacy & Security → Open Anyway**, or launch it once with right-click → Open. Nothing is installed — deleting the folder removes every trace.

Flags: `-port 8737` (walks upward if busy) · `-dir path/to/notes` · `-no-browser` · `-host`. By default the server binds **127.0.0.1 only** — nothing is exposed to the network.

### Reaching it from your phone (`-host`)

- `-host subnet` — **recommended for phone access.** Binds all interfaces but only serves clients whose IP is on this device's own local subnet (auto-detected from the interface netmask, e.g. `10.10.10.100` → `10.10.10.0/24`); localhost is always allowed and anything off-subnet gets a `403`. Open the `http://<this-device-ip>:<port>` URL it prints on your phone.
- `-host 0.0.0.0` — binds all interfaces with **no** source restriction (any routable host can reach it).

Both still have **no password** for clients that are allowed through, and the OS firewall is a separate layer — Windows may still block the inbound port regardless of `-host` (see the security note below).

## Eisvogel PDF export (the real thing)

```powershell
# Windows
powershell -ExecutionPolicy Bypass -File scripts\get-tools.ps1
```
```sh
# macOS
sh scripts/get-tools.sh
```

This downloads two **portable** binaries into `tools/` (~280 MB, no installation): [pandoc](https://github.com/jgm/pandoc) and [tectonic](https://tectonic-typesetting.github.io/) — a self-contained LaTeX engine. The app detects them automatically (the sidebar badge flips to **PDF: eisvogel**) and the export menu items enable.

Notes:
- The **first export downloads ~100 MB of LaTeX packages** into `tools/tectonic-cache/` and takes a few minutes; after that, exports run in seconds and work offline.
- The [Eisvogel template](https://github.com/Wandmalfarbe/pandoc-latex-template) (v3.5.0) is embedded in the binary itself. One vendored tweak: `sourcesans` → `sourcesanspro` (same Source Sans font, but present in tectonic's package bundle).
- No `tools/`? The **Print / Save as PDF** menu item always works — it opens an Eisvogel-styled print view (title page, rule, headers/footers with page numbers via CSS `@page`) and triggers the browser's print dialog.

## Keyboard shortcuts

| Keys | Action |
|---|---|
| `Ctrl+Alt+N` | New note |
| `Ctrl+K` | Focus search |
| `Ctrl+S` | Save now (autosave runs anyway, 600 ms after you stop typing) |
| `Ctrl+E` | Cycle edit / split / preview |
| `Ctrl+B` / `Ctrl+I` / `` Ctrl+` `` / `Ctrl+L` | Bold / italic / code / link (in the editor) |
| `Esc` | Clear search |

## Note file format

```markdown
---
title: "AWS GuardDuty Runbook"
tags: [aws, security]
starred: true
trashed: false
created: 2026-07-03T04:35:21Z
updated: 2026-07-03T04:35:21Z
---

# Containment
...
```

Files without frontmatter are adopted as-is (title from the first heading, timestamps from the file). Trash is a flag, not a folder — "Delete forever" in the Trash view is what actually removes the file.

## On search: why BM25 and not a vector DB

Semantic/vector search needs an embedding model (~100 MB ONNX + a native runtime per platform), which fights the single-binary, no-install constraint — and for keyword-ish note lookup, a properly tuned lexical index is usually *better*: exact terms, prefixes while you type, title/tag boosting, zero latency. If semantic search becomes worth it, the clean upgrade path is an optional sidecar (e.g. a local Ollama embedding endpoint) feeding cosine scores into the same ranking — the API is already shaped for it (`/api/search` returns scored results).

## Build from source

```powershell
powershell -ExecutionPolicy Bypass -File scripts\build.ps1
```

Pure-stdlib Go (no dependencies to fetch), cross-compiles both targets from Windows. The UI (`ui/`) and the Eisvogel template are embedded via `go:embed`.

```
portanote/
├── main.go / api.go / notes.go / search.go / export.go
├── ui/            # embedded frontend (vanilla JS + marked/DOMPurify/highlight.js)
├── pandoc/        # eisvogel.latex (embedded at build)
├── scripts/       # get-tools.ps1 · get-tools.sh · build.ps1
├── dist/          # portanote-windows-amd64.exe · portanote-macos-arm64
├── tools/         # (optional, gitignored) pandoc + tectonic + LaTeX cache
└── notes/         # your notes (gitignored)
```
