package ffmpeg

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// audioExtensions is what the app offers to open.
//
// ffmpeg decodes far more than this, and anything here can still be opened
// through the dialog's "All files" filter. The point of the list is to keep a
// folder import and a drag-and-drop from sweeping up cover art and cue sheets,
// and to give the file dialog something usable to filter on.
var audioExtensions = []string{
	"mp3", "wav", "flac", "ogg", "oga", "opus", "m4a", "m4b", "aac", "wma",
	"aiff", "aif", "alac", "ape", "wv", "mka", "mp4", "webm", "mkv",
}

// AudioExtensions returns the file extensions treated as playable audio,
// lowercase and without a leading dot.
func AudioExtensions() []string {
	out := make([]string, len(audioExtensions))
	copy(out, audioExtensions)
	return out
}

// AudioFilePattern renders the extensions as a file-dialog filter.
func AudioFilePattern() string {
	parts := make([]string, 0, len(audioExtensions))
	for _, ext := range audioExtensions {
		parts = append(parts, "*."+ext)
	}
	return strings.Join(parts, ";")
}

// IsAudioFile reports whether a path looks like audio this app should offer to
// play, judging only by its extension.
func IsAudioFile(path string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	if ext == "" {
		return false
	}
	for _, candidate := range audioExtensions {
		if ext == candidate {
			return true
		}
	}
	return false
}

// FindAudioFiles lists the playable files under dir, including subfolders, in
// a stable order. It stops after limit files, so pointing the app at a whole
// music library by accident yields something the UI can still render rather
// than exhausting memory.
//
// An unreadable subfolder costs that subfolder rather than the whole walk: a
// permission error part-way through a large import should not discard
// everything found before it. A folder the user actually chose is different —
// failing to read that is worth reporting rather than silently importing
// nothing.
func FindAudioFiles(dir string, limit int) ([]string, error) {
	var paths []string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			return nil
		}
		if d.IsDir() || !IsAudioFile(path) {
			return nil
		}

		paths = append(paths, path)
		if limit > 0 && len(paths) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(dir), err)
	}

	sort.Strings(paths)
	return paths, nil
}
