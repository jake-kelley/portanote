---
type: Guide
title: macOS permissions & troubleshooting
description: Why macOS blocks a portable app in ~/Documents, how to fix the Eisvogel export failing with "Operation not permitted", clearing Gatekeeper quarantine, and managing the LaunchAgent.
tags: [portanote, macos, permissions, tcc, gatekeeper, troubleshooting, launchagent]
timestamp: 2026-08-17T13:00:00-06:00
---

# macOS permissions & troubleshooting

Portanote is a bare, unsigned binary, and the install script puts it in `~/Documents/portanote/`. Those two facts put it on the wrong side of two separate macOS gatekeeping systems, and both produce errors that don't say what's actually wrong. This page is the map.

- **Gatekeeper** decides whether a downloaded binary may run at all. Symptom: the app won't start, or macOS says it can't be verified.
- **TCC** — the privacy system behind System Settings → Privacy & Security — decides whether a running binary may read your files. Symptom: the app runs fine, but something it does fails with `Operation not permitted`.

## "Operation not permitted" when exporting a PDF

The full error looks like this, and mentions nothing about permissions:

```
thread 'main' panicked at src/config.rs:156:18: called `Result::unwrap()` on an
`Err` value: Operation not permitted (os error 1)
```

That's [tectonic](https://tectonic-typesetting.github.io/), the LaTeX engine behind **Export → Eisvogel PDF**, hitting a wall and panicking instead of reporting the problem. `Operation not permitted` — errno 1 — is macOS's TCC signature. (Ordinary file-permission trouble says `Permission denied`, errno 13. The distinction is the fastest way to tell the two apart.)

`~/Documents` is a protected folder. Portanote may have been granted access to it, but the PDF export shells out to two *other* unsigned binaries in `tools/` — pandoc, which in turn runs tectonic — and **macOS evaluates each binary on its own identity**. A grant to Portanote does not cover them. That's the whole bug.

As of v1.6.12 Portanote keeps tectonic's package cache in `~/Library/Caches/Portanote/tectonic`, outside the protected area, so tectonic no longer needs a grant. pandoc still does whenever a note contains images, because it reads them from your notes folder.

### Fix: grant access explicitly

Clear any stale decision first. A *denied* grant is sticky — macOS will not prompt again while one exists, so a single misclicked "Don't Allow" leaves the feature permanently broken:

```sh
tccutil reset SystemPolicyDocumentsFolder
```

This is global: it re-prompts for every app that wants your Documents folder, not just Portanote. There is no per-app form for a bare binary — `tccutil` needs a bundle identifier and Portanote doesn't have one.

Then open Full Disk Access:

```sh
open "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"
```

Click **+**, and in the file picker press **⌘⇧G** to type a path. Add all three, one at a time:

```
~/Documents/portanote/portanote-macos-arm64
~/Documents/portanote/tools/pandoc
~/Documents/portanote/tools/tectonic
```

Then restart the app:

```sh
launchctl kickstart -k gui/$(id -u)/com.portanote.app
```

Use **Full Disk Access**, not the Files and Folders pane. Files and Folders only lists apps macOS has already prompted about and has no **+** button, so a bare binary can't be added there.

### Why it can come back after an update

An in-app update replaces the binary in place. The new file is a different binary as far as macOS is concerned, and the grant you gave the old one may not carry over. Portanote runs as a LaunchAgent with no terminal and no window, so the re-prompt has nowhere to appear — the export simply starts failing again.

If Eisvogel breaks right after an update, re-add `portanote-macos-arm64` in Full Disk Access before investigating anything else.

### Simplest way to avoid all of it

Keep the folder somewhere unprotected. `~/Applications/portanote` or `~/portanote` are outside TCC's scope entirely, and nothing about Portanote assumes a particular location — pass the folder to the install script, or move it and re-run:

```sh
mv ~/Documents/portanote ~/portanote
cd ~/portanote && curl -fsSL https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/install.sh | sh
```

The install script rewrites the LaunchAgent to point at the new location. Your notes move with the folder.

## "Cannot be opened because it is from an unidentified developer"

Gatekeeper, not TCC — this one stops the binary from running at all. The install script clears it for you (`xattr -d com.apple.quarantine`); do it by hand for a binary you downloaded yourself:

```sh
cd ~/Documents/portanote
chmod +x portanote-macos-arm64
xattr -d com.apple.quarantine portanote-macos-arm64
```

If macOS has already blocked it once, **System Settings → Privacy & Security** shows an **Open Anyway** button for a few minutes afterward. The same applies to `tools/pandoc` and `tools/tectonic` if you downloaded them outside `scripts/get-tools.sh`.

## Managing the LaunchAgent

The login launcher is `~/Library/LaunchAgents/com.portanote.app.plist`, label `com.portanote.app`.

| Task | Command |
|------|---------|
| Restart | `launchctl kickstart -k gui/$(id -u)/com.portanote.app` |
| Stop until next login | `launchctl bootout gui/$(id -u)/com.portanote.app` |
| Start again | `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.portanote.app.plist` |
| Is it running? | `pgrep -fl portanote` |
| Remove the launcher | `curl -fsSL https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/install.sh \| sh -s -- --uninstall` |

`KeepAlive` is set, so the agent restarts itself if it exits — use `bootout` rather than `kill` to stop it properly. Uninstalling the launcher leaves your notes and the deployed folder alone.

After editing the plist, `bootout` then `bootstrap`; `kickstart` reuses the loaded copy and won't pick up your changes.

## Getting more out of a failed export

Portanote returns the exporter's output to the browser but keeps only the **last 2000 characters**, so a long LaTeX log gets truncated from the top. To see a Rust backtrace, add it to the agent's environment:

```sh
/usr/libexec/PlistBuddy \
  -c "Add :EnvironmentVariables dict" \
  -c "Add :EnvironmentVariables:RUST_BACKTRACE string full" \
  ~/Library/LaunchAgents/com.portanote.app.plist
launchctl bootout gui/$(id -u)/com.portanote.app
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.portanote.app.plist
```

One warning about testing by hand. Running `tools/tectonic` from Terminal to reproduce a failure **will succeed even when the app is broken**, because Terminal has its own Documents grant and the tools inherit the attribution. A permissions failure of this kind only exists under launchd. Test by using the app.

Note also that `tectonic --version` returns before it ever opens its configuration, so it cannot reproduce this error under any circumstances. Only a real compile reaches the failing code.
