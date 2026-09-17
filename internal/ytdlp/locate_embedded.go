//go:build embedytdlp

package ytdlp

import (
	"embed"
	"os"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/embedbin"
)

// The build fetches yt-dlp and gzips it into this directory. It is not
// committed, and this file only compiles under the embedytdlp tag, so an
// ordinary build never needs the asset present.
//
//go:embed assets/ytdlp.gz
var embeddedYtdlp embed.FS

// overrideEnv names a yt-dlp to use instead of the bundled one.
//
// The bundled copy is frozen at release time, and YouTube changes break
// extraction every few weeks — long before the next build of this app. Rather
// than let the binary update itself, which would invalidate the content hash
// its filename is built from, this is the escape hatch: point the app at a
// current yt-dlp and it uses that instead. It is also the whole answer to
// "links stopped working" without shipping a release.
const overrideEnv = "DAS_YTDLP"

// resolve unpacks the bundled yt-dlp into the user's cache directory and
// returns its path, unless the override names one to use instead.
func resolve() (string, error) {
	if override := os.Getenv(overrideEnv); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", &ErrNotFound{Reason: overrideEnv + " names " + override + ", which cannot be read"}
		}
		return override, nil
	}

	path, err := embedbin.Unpack(embeddedYtdlp, "assets/ytdlp.gz", "yt-dlp")
	if err != nil {
		return "", err
	}
	return path, nil
}
