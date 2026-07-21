#!/bin/sh
# Installs Portanote: downloads the latest release binary if none is present
# (verifying its sha256 checksum), deploys it to ~/Documents/portanote,
# starts it, and sets it to start in the background at every login (a
# LaunchAgent, no terminal window). Detects everything it needs. Run with
# --uninstall to remove the login launcher again (the deployed folder with
# your notes is left alone).
#
# One-liner, from anywhere:
#   curl -fsSL https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/install.sh | sh
# Already downloaded a binary? Run it from that folder and it is used instead.
#
# What it does:
#   1. finds the portanote binary in the current folder, or downloads the
#      latest release and verifies it against the release's sha256sums.txt
#   2. deploys to ~/Documents/portanote/ (plus notes/ and tools/ if they sit
#      next to a local binary and aren't already there) - pass --in-place to
#      run from the current folder instead
#   3. installs a LaunchAgent pointing there (starts now, and at every login)
set -e

repo="jake-kelley/portanote"
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
dest="$HOME/Documents/portanote"

# --- find or download the binary -------------------------------------------

bin=""
for name in portanote-macos-arm64 portanote; do
    if [ -f "$root/$name" ]; then bin="$root/$name"; break; fi
done

if [ -z "$bin" ]; then
    name="portanote-macos-arm64"
    if [ -n "$inplace" ]; then dldir="$root"; else dldir="$dest"; fi
    mkdir -p "$dldir"
    base="https://github.com/$repo/releases/latest/download"
    echo "Downloading the latest release from $repo ..."
    curl -fL --progress-bar -o "$dldir/$name" "$base/$name"
    # same integrity check the in-app updater performs
    expected="$(curl -fsSL "$base/sha256sums.txt" | grep "$name" | cut -d' ' -f1)"
    actual="$(shasum -a 256 "$dldir/$name" | cut -d' ' -f1)"
    if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
        rm -f "$dldir/$name"
        echo "Checksum verification failed for $name (expected '$expected', got '$actual') - download discarded." >&2
        exit 1
    fi
    echo "Verified sha256 checksum."
    bin="$dldir/$name"
fi

# --- deploy -----------------------------------------------------------------

bindir="$(cd "$(dirname "$bin")" && pwd)"
copied=""
if [ -z "$inplace" ] && [ "$bindir" != "$dest" ]; then
    mkdir -p "$dest"
    cp -f "$bin" "$dest/"
    copied="$(basename "$bin")"
    # bring along notes/tools sitting next to the downloaded binary, but never
    # overwrite ones already deployed
    for d in notes tools; do
        if [ -d "$bindir/$d" ] && [ ! -d "$dest/$d" ]; then
            cp -R "$bindir/$d" "$dest/"
            copied="$copied, $d/"
        fi
    done
    bin="$dest/$(basename "$bin")"
else
    dest="$bindir"
fi

notes="${notes_arg:-$dest/notes}"

# make it runnable and clear the quarantine bit so Gatekeeper allows a
# background launch
chmod +x "$bin"
xattr -d com.apple.quarantine "$bin" 2>/dev/null || true

# --- launcher ---------------------------------------------------------------

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
    echo "The copy in $bindir is no longer used - delete it when convenient."
fi
echo "Installed $plist"
echo "  binary: $bin"
echo "  notes:  $notes"
echo ""
echo "Portanote is running now and will start at every login - bookmark http://127.0.0.1:8737"
echo "Undo autostart anytime:  sh install.sh --uninstall"
