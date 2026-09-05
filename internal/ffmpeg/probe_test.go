package ffmpeg

import (
	"context"
	"strings"
	"testing"
	"time"
)

func probeLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

const mp3Header = `
Input #0, mp3, from 't.mp3':
  Metadata:
    title           : Test Track
    artist          : Test Artist
    encoder         : Lavf63.1.101
  Duration: 00:00:02.00, start: 0.025057, bitrate: 66 kb/s
  Stream #0:0: Audio: mp3 (mp3float), 44100 Hz, mono, fltp, 64 kb/s, start 0.025057
At least one output file must be specified
`

func TestParseProbeOutput(t *testing.T) {
	meta, ok := parseProbeOutput("t.mp3", probeLines(mp3Header))
	if !ok {
		t.Fatal("parse reported no audio stream")
	}
	if meta.Title != "Test Track" || meta.Artist != "Test Artist" {
		t.Errorf("tags = %q / %q", meta.Title, meta.Artist)
	}
	if meta.Duration != 2*time.Second {
		t.Errorf("Duration = %v, want 2s", meta.Duration)
	}
	if meta.Codec != "mp3" {
		t.Errorf("Codec = %q, want mp3", meta.Codec)
	}
}

// A file ffmpeg cannot open must be distinguishable from one that simply has no
// tags, or the UI would happily "load" a corrupt file and then fail at play.
func TestParseProbeOutputRejectsUnopenableInput(t *testing.T) {
	lines := probeLines(`
notes.txt: Invalid data found when processing input
`)
	if _, ok := parseProbeOutput("notes.txt", lines); ok {
		t.Fatal("parse accepted input ffmpeg could not open")
	}
}

// A video container with no audio track is not something we can play.
func TestParseProbeOutputRejectsVideoOnlyInput(t *testing.T) {
	lines := probeLines(`
Input #0, mov,mp4,m4a, from 'clip.mp4':
  Duration: 00:00:05.00, start: 0.000000, bitrate: 1000 kb/s
  Stream #0:0: Video: h264 (High), yuv420p, 1920x1080, 30 fps
`)
	if _, ok := parseProbeOutput("clip.mp4", lines); ok {
		t.Fatal("parse accepted a file with no audio stream")
	}
}

func TestParseProbeOutputWithoutTags(t *testing.T) {
	lines := probeLines(`
Input #0, wav, from 'tone.wav':
  Duration: 00:03:21.30, start: 0.000000, bitrate: 1536 kb/s
  Stream #0:0: Audio: pcm_s16le ([1][0][0][0] / 0x0001), 48000 Hz, stereo, s16, 1536 kb/s
`)
	meta, ok := parseProbeOutput("/music/tone.wav", lines)
	if !ok {
		t.Fatal("parse reported no audio stream")
	}
	if want := 3*time.Minute + 21*time.Second + 300*time.Millisecond; meta.Duration != want {
		t.Errorf("Duration = %v, want %v", meta.Duration, want)
	}
	if got := meta.DisplayName(); got != "tone.wav" {
		t.Errorf("DisplayName = %q, want the filename", got)
	}
}

// Live inputs report no duration; that must parse as zero, not as a failure.
func TestParseProbeOutputHandlesUnknownDuration(t *testing.T) {
	lines := probeLines(`
Input #0, ogg, from 'stream.ogg':
  Duration: N/A, bitrate: N/A
  Stream #0:0: Audio: vorbis, 48000 Hz, stereo, fltp
`)
	meta, ok := parseProbeOutput("stream.ogg", lines)
	if !ok {
		t.Fatal("parse reported no audio stream")
	}
	if meta.Duration != 0 {
		t.Errorf("Duration = %v, want 0", meta.Duration)
	}
}

// ffmpeg prints however many fractional digits it likes, so the scale is taken
// from the field's own width.
func TestParseDurationFractionalPrecision(t *testing.T) {
	for _, tc := range []struct {
		line string
		want time.Duration
	}{
		{"  Duration: 00:00:01.5", 1500 * time.Millisecond},
		{"  Duration: 00:00:01.50", 1500 * time.Millisecond},
		{"  Duration: 00:00:01.500", 1500 * time.Millisecond},
		{"  Duration: 01:02:03.25", time.Hour + 2*time.Minute + 3*time.Second + 250*time.Millisecond},
	} {
		m := durationRe.FindStringSubmatch(tc.line)
		if m == nil {
			t.Fatalf("no match for %q", tc.line)
		}
		if got := parseDuration(m); got != tc.want {
			t.Errorf("%q gave %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestDisplayNamePrefersTags(t *testing.T) {
	for _, tc := range []struct {
		meta Metadata
		want string
	}{
		{Metadata{Path: "/m/a.mp3", Artist: "A", Title: "T"}, "A — T"},
		{Metadata{Path: "/m/a.mp3", Title: "T"}, "T"},
		{Metadata{Path: "/m/a.mp3", Artist: "A"}, "a.mp3"},
		{Metadata{Path: "/m/a.mp3"}, "a.mp3"},
	} {
		if got := tc.meta.DisplayName(); got != tc.want {
			t.Errorf("DisplayName = %q, want %q", got, tc.want)
		}
	}
}

func TestProbeRespectsContextCancellation(t *testing.T) {
	requireFFmpeg(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Probe(ctx, "anything.mp3"); err == nil {
		t.Fatal("Probe with a cancelled context succeeded")
	}
}
