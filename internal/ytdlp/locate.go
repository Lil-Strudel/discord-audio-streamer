// Package ytdlp drives yt-dlp to turn a YouTube link into something ffmpeg can
// decode.
//
// It does two things and no more: list what a link contains, so entries can be
// queued with real titles, and resolve one video to a direct media URL at the
// moment it is played. Nothing here downloads audio — yt-dlp is used only to
// find out where the audio is, and ffmpeg fetches and decodes it, which is what
// keeps seeking a range request rather than a re-download.
package ytdlp

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	resolveOnce sync.Once
	resolvedBin string
	resolveErr  error
)

// Resolve returns the path to the yt-dlp binary to run.
//
// Release builds carry yt-dlp inside the executable and unpack it on first use;
// development builds take it from PATH. Which of the two applies is chosen by
// the embedytdlp build tag, mirroring ffmpeg.
func Resolve() (string, error) {
	resolveOnce.Do(func() {
		resolvedBin, resolveErr = resolve()
	})
	return resolvedBin, resolveErr
}

// ErrNotFound is returned when no usable yt-dlp binary is available.
type ErrNotFound struct{ Reason string }

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("YouTube links are unavailable: yt-dlp %s", e.Reason)
}

// cacheDir is where yt-dlp is told to keep its own scratch data.
//
// Left to itself it writes into the user's home under its own name. Pointing it
// here keeps everything this app causes to exist in one place the user can
// delete, and stops a portable install from leaving traces behind.
func cacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "DiscordAudioStreamer", "yt-dlp")
}
