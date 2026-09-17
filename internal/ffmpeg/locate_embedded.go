//go:build embedffmpeg

package ffmpeg

import (
	"embed"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/embedbin"
)

// The build fetches ffmpeg and gzips it into this directory. It is not
// committed, and this file only compiles under the embedffmpeg tag, so an
// ordinary build never needs the asset present.
//
//go:embed assets/ffmpeg.gz
var embeddedFFmpeg embed.FS

// resolve unpacks the bundled ffmpeg into the user's cache directory and
// returns its path. See internal/embedbin for why it is extracted rather than
// run from memory, and how an app update avoids reusing the previous copy.
func resolve() (string, error) {
	path, err := embedbin.Unpack(embeddedFFmpeg, "assets/ffmpeg.gz", "ffmpeg")
	if err != nil {
		return "", err
	}
	return path, nil
}
