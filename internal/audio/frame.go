// Package audio implements the PCM side of the streaming pipeline: turning a byte
// stream into fixed-size frames, scaling their amplitude, buffering them against
// timing jitter, and encoding them to Opus.
//
// Every stage operates on the one frame geometry Discord accepts, so the constants
// below are the single source of truth for the whole package.
package audio

// The audio format Discord's voice gateway expects. ffmpeg is always asked for
// exactly this, so no resampling or channel conversion happens in Go.
const (
	// SampleRate is the sample rate in Hz.
	SampleRate = 48000

	// Channels is the channel count. Discord voice is always stereo.
	Channels = 2

	// FrameDurationMs is the length of one Opus frame in milliseconds.
	FrameDurationMs = 20

	// SamplesPerChannel is the number of samples per channel in one frame: 960.
	SamplesPerChannel = SampleRate / 1000 * FrameDurationMs

	// SamplesPerFrame is the total number of int16 samples in one interleaved
	// frame: 1920.
	SamplesPerFrame = SamplesPerChannel * Channels

	// BytesPerFrame is the size of one frame as little-endian int16 bytes: 3840.
	BytesPerFrame = SamplesPerFrame * 2

	// MaxOpusFrameSize is a safe upper bound for one encoded Opus packet. The
	// theoretical maximum for a 20 ms stereo frame is well under this.
	MaxOpusFrameSize = 4000
)
