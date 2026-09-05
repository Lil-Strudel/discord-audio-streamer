package ffmpeg

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Linux capture exists for development only; the release target is Windows.
// PulseAudio does expose monitor sources, so unlike Windows no virtual cable is
// needed to capture desktop audio here.
var pulseSourceRe = regexp.MustCompile(`^(\*)?\s*(\S+)\s+\[(.*)\]`)

// ListCaptureDevices enumerates PulseAudio sources.
func ListCaptureDevices(ctx context.Context) ([]CaptureDevice, error) {
	bin, err := Resolve()
	if err != nil {
		return nil, err
	}

	// Unlike -list_devices, -sources prints its list on stdout.
	lines, _, err := collect(ctx, bin,
		[]string{"-hide_banner", "-nostdin", "-sources", "pulse"}, deviceListTimeout)
	if err != nil {
		return nil, fmt.Errorf("list pulse sources: %w", err)
	}

	return parsePulseDevices(lines), nil
}

// OpenCapture starts capturing from a PulseAudio source.
func OpenCapture(ctx context.Context, deviceID string, bufferMs int) (Source, error) {
	bin, err := Resolve()
	if err != nil {
		return nil, err
	}
	if deviceID == "" {
		deviceID = "default"
	}

	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "pulse",
		"-fragment_size", strconv.Itoa(clampBufferMs(bufferMs) * bytesPerMs),
		"-i", deviceID,
	}
	args = append(args, pcmOutputArgs()...)

	proc, err := Start(ctx, bin, args, nil)
	if err != nil {
		return nil, fmt.Errorf("capture from %q: %w", deviceID, err)
	}
	return proc, nil
}

// parsePulseDevices reads the source list ffmpeg prints for -sources pulse.
func parsePulseDevices(lines []string) []CaptureDevice {
	var devices []CaptureDevice
	for _, line := range lines {
		if strings.HasPrefix(line, "Auto-detected sources") {
			continue
		}
		m := pulseSourceRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		devices = append(devices, CaptureDevice{
			ID:   m[2],
			Name: m[3],
			// A monitor source is the Pulse equivalent of desktop audio, which
			// is why Linux needs no virtual cable and Windows does.
			IsLoopback: strings.HasSuffix(m[2], ".monitor"),
			IsDefault:  m[1] == "*",
		})
	}
	return devices
}

// bytesPerMs at the capture format: 48 samples per ms, 2 channels, 2 bytes.
const bytesPerMs = 48 * 2 * 2
