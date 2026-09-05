#!/usr/bin/env bash
# Fetch a Windows ffmpeg build and stage it for embedding into the executable.
#
# Release builds carry ffmpeg inside the exe so the person receiving the app has
# nothing to install. The asset is fetched here rather than committed, because a
# ~40 MB binary does not belong in a git history.
set -euo pipefail

DEST="${1:-internal/ffmpeg/assets/ffmpeg.gz}"

# gyan.dev publishes the builds the ffmpeg project itself links to for Windows.
# The "essentials" build is a fraction of the size of the full one and still
# decodes every audio format this app offers, and includes the DirectShow input
# that desktop capture depends on.
URL="${FFMPEG_URL:-https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip}"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "-> Downloading $URL"
curl -fsSL "$URL" -o "$WORK/ffmpeg.zip"

echo "-> Extracting ffmpeg.exe"
# The archive nests everything under a version-stamped directory, so the binary
# is located rather than assumed to be at a fixed path.
INNER="$(unzip -Z1 "$WORK/ffmpeg.zip" | grep -E 'bin/ffmpeg\.exe$' | head -n 1)"
if [ -z "$INNER" ]; then
    echo "Error: no bin/ffmpeg.exe inside the archive" >&2
    exit 1
fi
unzip -q -j "$WORK/ffmpeg.zip" "$INNER" -d "$WORK"

mkdir -p "$(dirname "$DEST")"
gzip -9 -c "$WORK/ffmpeg.exe" > "$DEST"

RAW=$(stat -c %s "$WORK/ffmpeg.exe" 2>/dev/null || stat -f %z "$WORK/ffmpeg.exe")
PACKED=$(stat -c %s "$DEST" 2>/dev/null || stat -f %z "$DEST")
echo "-> Staged $DEST ($((RAW / 1024 / 1024))MB -> $((PACKED / 1024 / 1024))MB)"
