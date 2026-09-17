#!/usr/bin/env bash
# Fetch a Windows yt-dlp build and stage it for embedding into the executable.
#
# Release builds carry yt-dlp inside the exe so the person receiving the app has
# nothing to install, the same way ffmpeg is handled. The asset is fetched here
# rather than committed, because a ~17 MB binary does not belong in a git
# history.
#
# Note what this means for support: the copy that ships is frozen at build time,
# and YouTube changes break extraction every few weeks. The app honours DAS_YTDLP
# as an escape hatch so a stale copy can be replaced without a new release; see
# internal/ytdlp/locate_embedded.go.
set -euo pipefail

DEST="${1:-internal/ytdlp/assets/ytdlp.gz}"

# Pinned rather than tracking latest, so a build is reproducible and a release
# is never quietly built against a yt-dlp nobody tested.
VERSION="${YTDLP_VERSION:-2026.08.19}"
BASE="${YTDLP_BASE:-https://github.com/yt-dlp/yt-dlp/releases/download/$VERSION}"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "-> Downloading yt-dlp $VERSION"
curl -fsSL "$BASE/yt-dlp.exe" -o "$WORK/yt-dlp.exe"

# This is an executable being curled straight into a release zip, so the
# published checksum is verified rather than trusted to the transport.
echo "-> Verifying checksum"
curl -fsSL "$BASE/SHA2-256SUMS" -o "$WORK/SHA2-256SUMS"

EXPECTED="$(grep -E '[[:space:]]yt-dlp\.exe$' "$WORK/SHA2-256SUMS" | head -n 1 | awk '{print $1}')"
if [ -z "$EXPECTED" ]; then
    echo "Error: no yt-dlp.exe entry in the published checksums" >&2
    exit 1
fi

ACTUAL="$(sha256sum "$WORK/yt-dlp.exe" | awk '{print $1}')"
if [ "$EXPECTED" != "$ACTUAL" ]; then
    echo "Error: checksum mismatch for yt-dlp.exe" >&2
    echo "  expected $EXPECTED" >&2
    echo "  actual   $ACTUAL" >&2
    exit 1
fi

mkdir -p "$(dirname "$DEST")"
gzip -9 -c "$WORK/yt-dlp.exe" > "$DEST"

RAW=$(stat -c %s "$WORK/yt-dlp.exe" 2>/dev/null || stat -f %z "$WORK/yt-dlp.exe")
PACKED=$(stat -c %s "$DEST" 2>/dev/null || stat -f %z "$DEST")
echo "-> Staged $DEST ($((RAW / 1024 / 1024))MB -> $((PACKED / 1024 / 1024))MB)"
