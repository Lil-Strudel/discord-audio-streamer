package ffmpeg

import (
	"context"
	"fmt"
	"strconv"
)

// ListCaptureDevices enumerates DirectShow audio inputs.
func ListCaptureDevices(ctx context.Context) ([]CaptureDevice, error) {
	bin, err := Resolve()
	if err != nil {
		return nil, err
	}

	// Listing always ends with ffmpeg complaining that "dummy" is not a real
	// input; the device list it prints first is what we are after.
	// -list_devices reports through the log, so the list arrives on stderr.
	_, lines, err := collect(ctx, bin,
		[]string{"-hide_banner", "-nostdin", "-list_devices", "true", "-f", "dshow", "-i", "dummy"},
		deviceListTimeout)
	if err != nil {
		return nil, fmt.Errorf("list DirectShow devices: %w", err)
	}

	return parseDshowDevices(lines), nil
}

// OpenCapture starts capturing from a DirectShow device.
func OpenCapture(ctx context.Context, deviceID string, bufferMs int) (Source, error) {
	bin, err := Resolve()
	if err != nil {
		return nil, err
	}
	if deviceID == "" {
		return nil, fmt.Errorf("no capture device selected")
	}

	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "dshow",
		// DirectShow buffers half a second by default, which would put the
		// stream noticeably behind the desktop for no benefit.
		"-audio_buffer_size", strconv.Itoa(clampBufferMs(bufferMs)),
		// Absorb scheduling hiccups without ffmpeg dropping the input outright.
		"-rtbufsize", "64M",
		"-i", "audio=" + deviceID,
	}
	args = append(args, pcmOutputArgs()...)

	proc, err := Start(ctx, bin, args, nil)
	if err != nil {
		return nil, fmt.Errorf("capture from %q: %w", deviceID, err)
	}
	return proc, nil
}
