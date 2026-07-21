#!/bin/sh
# Downloads portable pandoc + tectonic into ./tools/ (no installation).
# These enable the true Eisvogel PDF export.
#
# Run it FROM THE FOLDER THAT HOLDS THE PORTANOTE BINARY (repo root works too):
#   sh scripts/get-tools.sh
# or with no repo checkout at all:
#   curl -fsSL https://raw.githubusercontent.com/jake-kelley/portanote/main/scripts/get-tools.sh | sh
set -e

root="$(pwd)"
tools="$root/tools"
tmp="$(mktemp -d)"
mkdir -p "$tools"

arch="$(uname -m)"   # arm64 on Apple Silicon, x86_64 on Intel
case "$arch" in
  arm64|aarch64) pandoc_pat="arm64-macOS.zip";  tect_pat="aarch64-apple-darwin.tar.gz" ;;
  *)             pandoc_pat="x86_64-macOS.zip"; tect_pat="x86_64-apple-darwin.tar.gz" ;;
esac

asset_url() { # repo, pattern
  curl -s "https://api.github.com/repos/$1/releases/latest" |
    grep -o '"browser_download_url": *"[^"]*"' | cut -d'"' -f4 | grep "$2" | head -1
}

echo "downloading pandoc..."
curl -sL -o "$tmp/pandoc.zip" "$(asset_url jgm/pandoc "$pandoc_pat")"
unzip -qo "$tmp/pandoc.zip" -d "$tmp/pandoc"
find "$tmp/pandoc" -type f -name pandoc -exec cp {} "$tools/pandoc" \;
chmod +x "$tools/pandoc"
echo "tools/pandoc ready"

echo "downloading tectonic..."
curl -sL -o "$tmp/tectonic.tar.gz" "$(asset_url tectonic-typesetting/tectonic "$tect_pat")"
tar -xzf "$tmp/tectonic.tar.gz" -C "$tmp"
find "$tmp" -maxdepth 2 -type f -name tectonic -exec cp {} "$tools/tectonic" \;
chmod +x "$tools/tectonic"
echo "tools/tectonic ready"

# clear the quarantine bit so Gatekeeper lets the binaries run
xattr -dr com.apple.quarantine "$tools" 2>/dev/null || true
rm -rf "$tmp"
echo
echo "Done. First PDF export downloads LaTeX packages (~100 MB) into tools/tectonic-cache — allow a few minutes."
