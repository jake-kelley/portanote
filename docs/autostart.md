---
type: Guide
title: Start Portanote at login
description: Launchers that start Portanote in the background at login — a hidden VBS launcher on Windows, a LaunchAgent on macOS.
tags: [portanote, autostart, windows, macos]
timestamp: 2026-07-20T14:00:00-06:00
---

# Start Portanote at login

Running the binary normally keeps a console window open. To have Portanote start at login and run quietly in the background, use one of the launchers below with `-no-browser` (so it doesn't pop a browser every boot), and bookmark `http://127.0.0.1:8737`.

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
