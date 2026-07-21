#!/bin/sh
# Sets Portanote to start in the background at every login (a LaunchAgent,
# no terminal window). Detects the binary, your home folder, and the notes
# folder automatically. Run with --uninstall to remove it again.
#
# Run it FROM THE FOLDER THAT HOLDS THE PORTANOTE BINARY:
#   sh scripts/autostart.sh [notes-dir]
# or with no repo checkout at all:
#   curl -fsSL https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/autostart.sh | sh
set -e

plist="$HOME/Library/LaunchAgents/com.portanote.app.plist"

if [ "$1" = "--uninstall" ]; then
    launchctl unload "$plist" 2>/dev/null || true
    rm -f "$plist"
    echo "Removed $plist — Portanote no longer starts at login (and was stopped if it was running)."
    exit 0
fi

root="$(pwd)"

bin=""
for name in portanote-macos-arm64 portanote; do
    if [ -f "$root/$name" ]; then bin="$root/$name"; break; fi
done
if [ -z "$bin" ]; then
    echo "No portanote binary found in $root." >&2
    echo "Run this from the folder that holds the binary." >&2
    exit 1
fi

notes="${1:-$root/notes}"

# make it runnable and clear the quarantine bit so Gatekeeper allows a
# background launch
chmod +x "$bin"
xattr -d com.apple.quarantine "$bin" 2>/dev/null || true

mkdir -p "$HOME/Library/LaunchAgents"
cat > "$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.portanote.app</string>
  <key>ProgramArguments</key>
  <array>
    <string>$bin</string>
    <string>-no-browser</string>
    <string>-dir</string>
    <string>$notes</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
EOF

launchctl unload "$plist" 2>/dev/null || true
launchctl load "$plist"

echo "Installed $plist"
echo "  binary: $bin"
echo "  notes:  $notes"
echo ""
echo "Portanote is running now and will start at every login - bookmark http://127.0.0.1:8737"
echo "Undo anytime:  sh scripts/autostart.sh --uninstall"
