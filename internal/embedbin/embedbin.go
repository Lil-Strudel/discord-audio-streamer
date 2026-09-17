// Package embedbin unpacks a helper binary that was compressed into the
// executable so it can be run as a real process.
//
// Release builds carry ffmpeg and yt-dlp inside the exe, because the person
// receiving the app should have nothing to install. Neither can be run from
// memory — both have to be actual processes — so they are written out to the
// user's cache directory on first use.
//
// The logic lives here, in a package with no build tag, rather than beside each
// //go:embed directive. Those directives only compile under a release tag, so a
// copy of this code next to each of them would never be reached by `go vet` or
// `go test` and the atomic-rename handling would go unchecked until a release
// misbehaved on someone else's machine.
package embedbin

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// Dir is where unpacked binaries are kept.
func Dir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate cache directory: %w", err)
	}
	return filepath.Join(cache, "DiscordAudioStreamer", "bin"), nil
}

// Unpack decompresses the gzipped binary at assetPath within assets and returns
// the path it can be run from.
//
// The file is named for a hash of its contents, so an app update installs its
// own copy rather than silently reusing the previous release's, and an existing
// file of the right size is reused rather than rewritten on every run.
func Unpack(assets fs.FS, assetPath, name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return UnpackInto(dir, assets, assetPath, name)
}

// UnpackInto is Unpack with the destination given explicitly, which is what
// makes this package testable without writing into the real cache directory.
func UnpackInto(dir string, assets fs.FS, assetPath, name string) (string, error) {
	payload, err := decompress(assets, assetPath)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(payload)
	filename := name + "-" + hex.EncodeToString(sum[:8])
	if runtime.GOOS == "windows" {
		filename += ".exe"
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	target := filepath.Join(dir, filename)

	if installed(target, len(payload)) {
		return target, nil
	}

	// Write to a temporary name and rename into place, so a half-written binary
	// is never left behind for a later run to execute.
	tmp, err := os.CreateTemp(dir, name+"-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return "", fmt.Errorf("make %s executable: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close %s: %w", name, err)
	}

	if err := os.Rename(tmpName, target); err != nil {
		// Windows refuses to replace a file that is currently executing, which
		// is exactly what a second instance of the app will be doing with this
		// binary. Losing that race is not a failure: the file already there was
		// named after the same hash, so it is the binary we were about to write.
		if installed(target, len(payload)) {
			return target, nil
		}
		return "", fmt.Errorf("install %s to %s: %w", name, target, err)
	}
	return target, nil
}

// installed reports whether target is already the binary we were going to
// write. The name carries a hash of the contents, so matching the size is
// enough to tell a complete copy from a truncated one.
func installed(target string, size int) bool {
	info, err := os.Stat(target)
	return err == nil && info.Size() == int64(size)
}

func decompress(assets fs.FS, assetPath string) ([]byte, error) {
	compressed, err := assets.Open(assetPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", assetPath, err)
	}
	defer compressed.Close()

	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", assetPath, err)
	}
	defer gz.Close()

	payload, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("decompress %s: %w", assetPath, err)
	}
	return payload, nil
}
