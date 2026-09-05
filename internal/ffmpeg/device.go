package ffmpeg

import "time"

// CaptureDevice is an audio input the app can stream from.
type CaptureDevice struct {
	// ID is the identifier passed to ffmpeg. It is not shown to the user, and
	// on Windows it is deliberately not the friendly name.
	ID string `json:"id"`

	// Name is the label shown in the UI.
	Name string `json:"name"`

	// IsLoopback marks devices that carry desktop audio rather than a
	// microphone, so the UI can point the user at the right one.
	IsLoopback bool `json:"isLoopback"`

	// IsDefault marks the system default input.
	IsDefault bool `json:"isDefault"`
}

// Capture buffering bounds, in milliseconds. This is the buffer inside ffmpeg,
// separate from the frame ring buffer in the audio package. Small values keep
// latency down; too small and the device driver cannot keep up.
const (
	MinCaptureBufferMs     = 20
	MaxCaptureBufferMs     = 200
	DefaultCaptureBufferMs = 20
)

func clampBufferMs(ms int) int {
	if ms < MinCaptureBufferMs {
		return MinCaptureBufferMs
	}
	if ms > MaxCaptureBufferMs {
		return MaxCaptureBufferMs
	}
	return ms
}

// deviceListTimeout bounds how long enumerating devices may take. A wedged
// audio driver must not hang the UI thread that asked for the list.
const deviceListTimeout = 15 * time.Second
