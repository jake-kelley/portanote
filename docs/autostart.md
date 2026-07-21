---
type: Guide
title: Install & start at login
description: The one-command install scripts — download + checksum verification, deploy to Documents, background start at every login — and the manual launcher steps they automate.
tags: [portanote, install, autostart, windows, macos]
timestamp: 2026-07-20T14:00:00-06:00
---

# Install & start at login

The install script for your OS sets up everything with one command. It detects everything it needs — an already-downloaded binary, your username/home folder, the notes folder — and does three things:

1. **Gets the binary.** If there's a portanote binary in the current folder it is used; otherwise the latest release is downloaded and **verified against the release's `sha256sums.txt`** (the same integrity check the in-app updater performs).
2. **Deploys the app to your Documents folder** — `Documents\portanote\` on Windows, `~/Documents/portanote/` on macOS. Any `notes/` or `tools/` folder sitting next to a local binary comes along; existing deployed folders are never overwritten, so re-running after an upgrade only refreshes the binary.
3. **Starts it, and installs a background launcher** so it starts quietly at every login — a hidden Startup-folder launcher on Windows, a LaunchAgent on macOS. No console window either way.

## The one command

```powershell
# Windows
iwr -useb https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/install.ps1 | iex
```

```sh
# macOS
curl -fsSL https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/install.sh | sh
```

Run it from anywhere for a fresh download, or from the folder holding a binary you already downloaded. The Windows script opens your browser when done (pass `-NoStart` to skip); bookmark `http://127.0.0.1:8737`.

(In a repo checkout: `powershell -ExecutionPolicy Bypass -File scripts\install.ps1` / `sh scripts/install.sh`. Both scripts are also attached to each [release](https://github.com/jake-kelley/portanote/releases/latest).)

To undo autostart (the deployed folder with your notes is left alone):

```powershell
powershell -ExecutionPolicy Bypass -File install.ps1 -Uninstall   # Windows (download the script first)
```

```sh
sh install.sh --uninstall                                          # macOS
```

Options that override the detection:

- **Windows:** `-Binary <exe>`, `-Dest <folder>` (instead of `Documents\portanote`), `-NotesDir <folder>`, `-Repo <owner/repo>` (download from a fork), `-InPlace` (no copy; use/download in the current folder).
- **macOS:** `--in-place` (no copy), or a notes-dir argument: `sh install.sh /path/to/notes`.

Prefer to see exactly what gets installed? The manual steps below are what the scripts automate (minus the download and the copy to Documents).

## Windows — hidden launcher in the Startup folder

1. Next to the exe, create a file `portanote.vbs` containing one line (adjust the two paths; the `0` means hidden window):

   ```vbs
   CreateObject("WScript.Shell").Run """C:\Users\you\portanote\portanote-windows-amd64.exe"" -no-browser -dir ""C:\Users\you\portanote\notes""", 0, False
   ```

2. Press `Win + R`, type `shell:startup`, press Enter — your Startup folder opens.
3. Drop `portanote.vbs` (or a shortcut to it) into that folder.

It now starts hidden at every login.

- **Stop it:** Task Manager → end `portanote-windows-amd64.exe`.
- **Disable autostart:** remove the file from the Startup folder.

## macOS — a LaunchAgent

1. Make the binary runnable and clear Gatekeeper once by launching it manually (right-click → Open), then quit it:

   ```sh
   chmod +x portanote-macos-arm64
   xattr -d com.apple.quarantine portanote-macos-arm64
   ```

2. Create `~/Library/LaunchAgents/com.portanote.app.plist` (adjust the paths):

   ```xml
   <?xml version="1.0" encoding="UTF-8"?>
   <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
   <plist version="1.0">
   <dict>
     <key>Label</key><string>com.portanote.app</string>
     <key>ProgramArguments</key>
     <array>
       <string>/Users/you/portanote/portanote-macos-arm64</string>
       <string>-no-browser</string>
       <string>-dir</string>
       <string>/Users/you/portanote/notes</string>
     </array>
     <key>RunAtLoad</key><true/>
     <key>KeepAlive</key><true/>
   </dict>
   </plist>
   ```

3. Load it: `launchctl load ~/Library/LaunchAgents/com.portanote.app.plist`

It starts at login (and restarts if it ever crashes), with no terminal window.

- **Stop & disable:** `launchctl unload ~/Library/LaunchAgents/com.portanote.app.plist`, then delete the plist.
