---
name: verify
description: Build and drive Portanote end-to-end to verify a change — launch the real binary against a scratch notes dir and exercise the HTTP API + on-disk layout.
---

# Verifying Portanote changes

Go toolchain: `go` on PATH, or the portable one at `$env:USERPROFILE\.toolchains\go\bin\go.exe` (what `scripts/build.ps1` falls back to).

## Build & launch (PowerShell)

```powershell
& "$env:USERPROFILE\.toolchains\go\bin\go.exe" build -o "$scratch\portanote-test.exe" .
Start-Process "$scratch\portanote-test.exe" -ArgumentList "-dir","$scratch\notes-verify","-port","9737","-no-browser" -RedirectStandardError "$scratch\server.log"
```

- Always pass `-dir` (scratch dir) and `-no-browser`. Never point a test build at a real notes dir — the user's live instance usually runs on 8737; use 9737.
- Startup log goes to stderr: "portanote vX — N notes loaded from …" confirms the scan.
- Stop with `Stop-Process` on the captured PID.

## Surfaces to drive

- REST: `/api/notes` (GET/POST), `/api/notes/{id}` (GET/PUT/DELETE), `/api/folders` (+ `/rename`, `/delete` via POST), `/api/search?q=`, `/api/tasks`.
- MCP endpoint at `/mcp` (JSON-RPC 2.0 over POST) — native clients send no Origin; browser-ish Origins must be loopback.
- Notes are real `.md` files under the notes dir; folders are real subdirectories — assert disk layout with `Get-ChildItem -Recurse`, and read frontmatter with `Get-Content -TotalCount`.

## Self-update e2e

Build an "old" binary pointed at a stub GitHub API and a "new" one as the release asset (`version` and `updateAPIBase` are ldflags-settable vars):

```powershell
go build -ldflags "-X main.version=9.9.9" -o new.exe .
go build -ldflags "-X main.version=1.0.0 -X main.updateAPIBase=http://127.0.0.1:9990" -o old.exe .
node fake-github.js 9990 new.exe portanote-windows-amd64.exe   # serves release JSON + /bin + /sums
```

Run old.exe, `POST /api/update/apply`, then poll the SAME port for the new version. For UI flows, puppeteer-core drives the installed Edge (no browser download): `npm i puppeteer-core`, `executablePath` = `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`. Gotcha: clear the search box via `e.value=""` + dispatched `input` event — triple-click-select doesn't work headless.

## Gotchas

- `templates/`, `backups/`, `attachments/` inside the notes dir are app-owned (not note folders); dot-dirs are skipped by the scan.
- A backup zip is written to `backups/` on startup (StartBackups) — expected noise in scratch dirs.
- Restart the server against the same dir as a persistence check — the disk is the only state.
