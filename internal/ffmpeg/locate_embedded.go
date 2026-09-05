//go:build embedffmpeg

package ffmpeg

import (
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// The build fetches ffmpeg and gzips it into this directory. It is not
// committed, and this file only compiles under the embedffmpeg tag, so an
// ordinary build never needs the asset present.
//
//go:embed assets/ffmpeg.gz
var embeddedFFmpeg embed.FS

// resolve unpacks the bundled ffmpeg into the user's cache directory and
// returns its path.
//
// It is extracted rather than run from memory because ffmpeg has to be a real
// process, and it is cached under a hash of its contents so an app update
// replaces the binary instead of silently reusing the old one.
func resolve() (string, error) {
	compressed, err := embeddedFFmpeg.Open("assets/ffmpeg.gz")
	if err != nil {
		return "", &ErrNotFound{Reason: "the bundled copy is missing from this build"}
	}
	defer compressed.Close()

	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return "", fmt.Errorf("read bundled ffmpeg: %w", err)
	}
	defer gz.Close()

	payload, err := io.ReadAll(gz)
	if err != nil {
		return "", fmt.Errorf("decompress bundled ffmpeg: %w", err)
	}

	sum := sha256.Sum256(payload)
	name := "ffmpeg-" + hex.EncodeToString(sum[:8])
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate cache directory: %w", err)
	}
	dir := filepath.Join(cache, "DiscordAudioStreamer", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	target := filepath.Join(dir, name)

	if info, err := os.Stat(target); err == nil && info.Size() == int64(len(payload)) {
		return target, nil
	}

	// Write to a temporary name and rename into place, so a half-written binary
	// is never left behind for a later run to execute.
	tmp, err := os.CreateTemp(dir, "ffmpeg-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write ffmpeg: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return "", fmt.Errorf("make ffmpeg executable: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close ffmpeg: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return "", fmt.Errorf("install ffmpeg to %s: %w", target, err)
	}

	return target, nil
}
