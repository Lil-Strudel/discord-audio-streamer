package ffmpeg

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
)

// Development-machine coverage for the Linux capture path. The Windows
// equivalent is covered by parsing tests in dshow_parse_test.go, since a
// DirectShow device cannot exist here.
func TestListCaptureDevicesOnPulse(t *testing.T) {
	requireFFmpeg(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	devices, err := ListCaptureDevices(ctx)
	if err != nil {
		t.Skipf("no PulseAudio available: %v", err)
	}
	if len(devices) == 0 {
		t.Skip("no capture devices on this machine")
	}

	for _, d := range devices {
		if d.ID == "" {
			t.Errorf("device %q has no ID", d.Name)
		}
		if d.Name == "" {
			t.Errorf("device %q has no display name", d.ID)
		}
		t.Logf("%-8v %-10v %s (%s)", d.IsDefault, d.IsLoopback, d.Name, d.ID)
	}
}

func TestPulseSourceParsing(t *testing.T) {
	line := "* alsa_output.usb-Focusrite.HiFi__Line__sink.monitor [Monitor of Scarlett Solo Headphones] (none)"
	m := pulseSourceRe.FindStringSubmatch(line)
	if m == nil {
		t.Fatal("no match")
	}
	if m[1] != "*" {
		t.Errorf("default marker = %q, want *", m[1])
	}
	if m[2] != "alsa_output.usb-Focusrite.HiFi__Line__sink.monitor" {
		t.Errorf("id = %q", m[2])
	}
	if m[3] != "Monitor of Scarlett Solo Headphones" {
		t.Errorf("description = %q", m[3])
	}
}

func TestClampBufferMs(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{0, MinCaptureBufferMs},
		{5, MinCaptureBufferMs},
		{20, 20},
		{100, 100},
		{5000, MaxCaptureBufferMs},
	} {
		if got := clampBufferMs(tc.in); got != tc.want {
			t.Errorf("clampBufferMs(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// Capture is the one path that cannot be checked by parsing alone: it has to
// actually produce PCM at the pipeline's format.
func TestOpenCaptureProducesPCM(t *testing.T) {
	requireFFmpeg(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	devices, err := ListCaptureDevices(ctx)
	if err != nil || len(devices) == 0 {
		t.Skip("no capture devices available")
	}

	device := devices[0]
	for _, d := range devices {
		if d.IsLoopback {
			device = d // a monitor source produces data without a live input
			break
		}
	}

	src, err := OpenCapture(ctx, device.ID, DefaultCaptureBufferMs)
	if err != nil {
		t.Skipf("cannot open %q: %v", device.Name, err)
	}
	defer src.Close()

	// A few frames is enough to prove the format and the plumbing.
	buf := make([]byte, audio.BytesPerFrame*5)
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(src, buf)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read from %q: %v", device.Name, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%q produced no audio within 10s", device.Name)
	}
}
