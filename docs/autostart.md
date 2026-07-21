---
type: Guide
title: Start Portanote at login
description: Launchers that start Portanote in the background at login — a hidden VBS launcher on Windows, a LaunchAgent on macOS.
tags: [portanote, autostart, windows, macos]
timestamp: 2026-07-20T14:00:00-06:00
---

# Start Portanote at login

Running the binary normally keeps a console window open. The setup script for your OS gives Portanote a permanent home and makes it start quietly in the background at every login. It detects everything it needs — the downloaded binary, your username/home folder, the notes folder — and does two things:

1. **Deploys the app to your Documents folder** — `Documents\portanote\` on Windows, `~/Documents/portanote/` on macOS. The binary is copied there, along with any `notes/` or `tools/` folder sitting next to it (existing deployed folders are never overwritten, so re-running after an upgrade only refreshes the binary).
2. **Installs a background launcher** pointing there — a hidden Startup-folder launcher on Windows, a LaunchAgent on macOS.

Bookmark `http://127.0.0.1:8737` to open the app.

## The easy way: one script

Run it **from the folder that holds the downloaded portanote binary** (your Downloads folder, typically):

```powershell
# Windows
iwr -useb https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/autostart.ps1 | iex
```

```sh
# macOS (starts Portanote immediately, too)
curl -fsSL https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/autostart.sh | sh
```

(In a repo checkout: `powershell -ExecutionPolicy Bypass -File scripts\autostart.ps1` / `sh scripts/autostart.sh`. Both scripts are also attached to each [release](https://github.com/jake-kelley/portanote/releases/latest).)

To undo autostart (the deployed folder with your notes is left alone):

```powershell
powershell -ExecutionPolicy Bypass -File autostart.ps1 -Uninstall   # Windows (download the script first)
```

```sh
sh autostart.sh --uninstall                                          # macOS
```

Options that override the detection:

- **Windows:** `-Binary <exe>`, `-Dest <folder>` (instead of `Documents\portanote`), `-NotesDir <folder>`, `-InPlace` (don't copy anything; run the binary from where it is).
- **macOS:** `--in-place` (don't copy), or a notes-dir argument: `sh autostart.sh /path/to/notes`.

Prefer to see exactly what gets installed? The manual steps below are what the scripts automate (minus the copy to Documents).

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
