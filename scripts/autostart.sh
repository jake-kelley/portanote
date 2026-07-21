#!/bin/sh
# Deploys Portanote to ~/Documents/portanote and sets it to start in the
# background at every login (a LaunchAgent, no terminal window). Detects
# everything it needs: the binary in the current folder, your home folder,
# and the notes folder. Run with --uninstall to remove the login launcher.
#
# Run it FROM THE FOLDER THAT HOLDS THE DOWNLOADED PORTANOTE BINARY:
#   sh autostart.sh [--in-place] [notes-dir]
# or with no downloads at all beyond the binary:
#   curl -fsSL https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/autostart.sh | sh
#
# What it does:
#   1. copies the binary into ~/Documents/portanote/ (plus notes/ and tools/
#      if they sit next to the binary and aren't already there) - pass
#      --in-place to skip this and run the binary from where it is
#   2. installs a LaunchAgent pointing there (starts now, and at every login)
set -e

plist="$HOME/Library/LaunchAgents/com.portanote.app.plist"

inplace=""
notes_arg=""
for a in "$@"; do
    case "$a" in
        --uninstall)
            launchctl unload "$plist" 2>/dev/null || true
            rm -f "$plist"
            echo "Removed $plist - Portanote no longer starts at login (and was stopped if it was running)."
            echo "Your deployed folder (binary + notes) is untouched."
            exit 0
            ;;
        --in-place) inplace=1 ;;
        *) notes_arg="$a" ;;
    esac
done

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

dest="$HOME/Documents/portanote"

# deploy unless asked not to (or the binary already lives in the destination)
copied=""
if [ -z "$inplace" ] && [ "$root" != "$dest" ]; then
    mkdir -p "$dest"
    cp -f "$bin" "$dest/"
    copied="$(basename "$bin")"
    # bring along notes/tools sitting next to the downloaded binary, but never
    # overwrite ones already deployed
    for d in notes tools; do
        if [ -d "$root/$d" ] && [ ! -d "$dest/$d" ]; then
            cp -R "$root/$d" "$dest/"
            copied="$copied, $d/"
        fi
    done
    bin="$dest/$(basename "$bin")"
else
    dest="$root"
fi

notes="${notes_arg:-$dest/notes}"

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

if [ -n "$copied" ]; then
    echo "Deployed to $dest  ($copied)"
    echo "The copy in $root is no longer used - delete it when convenient."
fi
echo "Installed $plist"
echo "  binary: $bin"
echo "  notes:  $notes"
echo ""
echo "Portanote is running now and will start at every login - bookmark http://127.0.0.1:8737"
echo "Undo autostart anytime:  sh autostart.sh --uninstall"
