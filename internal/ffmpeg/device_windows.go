package ffmpeg

import (
	"context"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/wasapi"
)

// Windows capture does not go through ffmpeg at all.
//
// ffmpeg's only audio input on Windows is DirectShow, which enumerates capture
// devices and nothing else — the speakers can never appear in it, which is why
// the DirectShow route needs a virtual audio cable before desktop audio can be
// streamed. Core Audio has no such restriction, so it is used directly. See the
// wasapi package.

// ListCaptureDevices enumerates every active audio endpoint, output and input.
func ListCaptureDevices(ctx context.Context) ([]CaptureDevice, error) {
	type result struct {
		devices []wasapi.Device
		err     error
	}

	done := make(chan result, 1)
	go func() {
		devices, err := wasapi.Devices()
		done <- result{devices, err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		return toCaptureDevices(r.devices), nil
	}
}

func toCaptureDevices(endpoints []wasapi.Device) []CaptureDevice {
	devices := make([]CaptureDevice, 0, len(endpoints))
	for _, e := range endpoints {
		devices = append(devices, CaptureDevice{
			ID:       e.ID,
			Name:     e.Name,
			IsOutput: e.IsOutput,
			// An output is recorded in loopback mode, so it carries desktop
			// audio by definition. An input only does when it is the receiving
			// end of a virtual cable.
			IsLoopback: e.IsOutput || looksLikeLoopback(e.Name),
			IsDefault:  e.IsDefault,
		})
	}
	return devices
}

// OpenCapture starts capturing from an audio endpoint.
func OpenCapture(ctx context.Context, deviceID string, bufferMs int) (Source, error) {
	return wasapi.Open(deviceID, clampBufferMs(bufferMs))
}
