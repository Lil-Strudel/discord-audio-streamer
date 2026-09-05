//go:build !windows && !linux

package ffmpeg

import (
	"context"
	"errors"
	"runtime"
)

// ErrCaptureUnsupported is returned on platforms where desktop capture has not
// been implemented. File playback still works everywhere.
var ErrCaptureUnsupported = errors.New("desktop audio capture is not supported on " + runtime.GOOS)

// ListCaptureDevices reports that capture is unavailable.
func ListCaptureDevices(context.Context) ([]CaptureDevice, error) {
	return nil, ErrCaptureUnsupported
}

// OpenCapture reports that capture is unavailable.
func OpenCapture(context.Context, string, int) (Source, error) {
	return nil, ErrCaptureUnsupported
}
