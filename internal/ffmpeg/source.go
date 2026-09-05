package ffmpeg

import (
	"io"
	"strconv"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
)

// Source is a running producer of raw PCM in the pipeline's fixed format:
// signed 16-bit little-endian samples, 48 kHz, stereo, interleaved.
type Source interface {
	io.Reader

	// Close terminates the source. It is safe to call while a read is blocked.
	Close() error

	// Wait blocks until the source has finished and reports why it stopped.
	Wait() error
}

// pcmOutputArgs are the output flags every source shares. Resampling and
// channel conversion are left to ffmpeg here so that no Go code ever has to
// deal with a format other than the one in the audio package.
func pcmOutputArgs() []string {
	return []string{
		"-vn",
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-ar", strconv.Itoa(audio.SampleRate),
		"-ac", strconv.Itoa(audio.Channels),
		"pipe:1",
	}
}
