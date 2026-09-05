package ffmpeg

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
)

// These exercise the real ffmpeg binary, since the whole point of this package
// is the exact shape of its command lines and output.

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := Resolve(); err != nil {
		t.Skipf("ffmpeg unavailable: %v", err)
	}
}

// makeTone writes a test file of the given length using ffmpeg's own signal
// generator, so the tests need no committed audio fixtures.
func makeTone(t *testing.T, name string, seconds float64, extraArgs ...string) string {
	t.Helper()
	requireFFmpeg(t)

	bin, _ := Resolve()
	path := filepath.Join(t.TempDir(), name)

	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi",
		"-i", "sine=frequency=440:duration=" + formatSeekTime(time.Duration(seconds*float64(time.Second))),
	}
	args = append(args, extraArgs...)
	args = append(args, path)

	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("generate %s: %v\n%s", name, err, out)
	}
	return path
}

func TestProbeReadsDuration(t *testing.T) {
	path := makeTone(t, "tone.wav", 2)

	meta, err := Probe(context.Background(), path)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if diff := (meta.Duration - 2*time.Second).Abs(); diff > 50*time.Millisecond {
		t.Fatalf("Duration = %v, want about 2s", meta.Duration)
	}
	if meta.Codec == "" {
		t.Error("Codec was not detected")
	}
}

func TestProbeReadsTags(t *testing.T) {
	path := makeTone(t, "tagged.mp3", 1,
		"-metadata", "title=Test Track",
		"-metadata", "artist=Test Artist")

	meta, err := Probe(context.Background(), path)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if meta.Title != "Test Track" {
		t.Errorf("Title = %q", meta.Title)
	}
	if meta.Artist != "Test Artist" {
		t.Errorf("Artist = %q", meta.Artist)
	}
	if got, want := meta.DisplayName(), "Test Artist — Test Track"; got != want {
		t.Errorf("DisplayName = %q, want %q", got, want)
	}
}

func TestProbeFallsBackToFilename(t *testing.T) {
	path := makeTone(t, "untagged.wav", 1)

	meta, err := Probe(context.Background(), path)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got := meta.DisplayName(); got != "untagged.wav" {
		t.Errorf("DisplayName = %q, want the filename", got)
	}
}

func TestProbeRejectsNonAudioFile(t *testing.T) {
	requireFFmpeg(t)

	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("this is not audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Probe(context.Background(), path); err == nil {
		t.Fatal("probing a text file succeeded, want an error")
	}
}

// One second of the pipeline format is exactly 192000 bytes, so a decode of a
// known-length file is checkable to the byte.
func TestOpenFileDecodesExpectedAmount(t *testing.T) {
	path := makeTone(t, "tone.wav", 1)

	src, err := OpenFile(context.Background(), path, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer src.Close()

	n, err := io.Copy(io.Discard, src)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	want := int64(audio.SampleRate * audio.Channels * 2)
	if diff := n - want; diff < -int64(audio.BytesPerFrame) || diff > int64(audio.BytesPerFrame) {
		t.Fatalf("decoded %d bytes, want about %d", n, want)
	}
	if err := src.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

// Seeking is implemented by restarting ffmpeg at a new offset, so the check is
// that the restarted decode is shorter by the amount skipped.
func TestOpenFileSeeksByRestarting(t *testing.T) {
	path := makeTone(t, "tone.wav", 4)

	src, err := OpenFile(context.Background(), path, 3*time.Second)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer src.Close()

	n, err := io.Copy(io.Discard, src)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if got := src.Offset(); got != 3*time.Second {
		t.Errorf("Offset = %v, want 3s", got)
	}

	oneSecond := int64(audio.SampleRate * audio.Channels * 2)
	if n > oneSecond+oneSecond/10 || n < oneSecond-oneSecond/10 {
		t.Fatalf("decoded %d bytes after seeking to 3s of a 4s file, want about %d", n, oneSecond)
	}
}

func TestOpenFileReportsMissingFile(t *testing.T) {
	requireFFmpeg(t)

	src, err := OpenFile(context.Background(), filepath.Join(t.TempDir(), "nope.mp3"), 0)
	if err != nil {
		return // failing at start is fine too
	}
	defer src.Close()

	_, _ = io.Copy(io.Discard, src)
	if err := src.Wait(); err == nil {
		t.Fatal("decoding a missing file reported success")
	}
}

// Closing mid-stream is the normal way playback stops, and must not surface as
// an error.
func TestCloseMidStreamIsNotAnError(t *testing.T) {
	path := makeTone(t, "long.wav", 30)

	src, err := OpenFile(context.Background(), path, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}

	buf := make([]byte, audio.BytesPerFrame)
	if _, err := io.ReadFull(src, buf); err != nil {
		t.Fatalf("read one frame: %v", err)
	}

	if err := src.Close(); err != nil {
		t.Fatalf("Close during playback returned %v, want nil", err)
	}
}

func TestFormatSeekTime(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{0, "0.000"},
		{1500 * time.Millisecond, "1.500"},
		{90 * time.Second, "90.000"},
		{3*time.Minute + 21*time.Second + 300*time.Millisecond, "201.300"},
	} {
		if got := formatSeekTime(tc.in); got != tc.want {
			t.Errorf("formatSeekTime(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
