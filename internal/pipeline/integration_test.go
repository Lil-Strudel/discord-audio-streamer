package pipeline_test

import (
	"context"
	"log/slog"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/ffmpeg"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/pacer"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/pipeline"
)

// These exercise the seam the unit tests cannot: a real ffmpeg decode running
// through the pipeline, encoded to Opus, and paced onto a connection. Only
// Discord itself is stood in for.

type captureConn struct {
	voice.Conn
	udp *captureUDP
}

func (c *captureConn) UDP() voice.UDPConn                                     { return c.udp }
func (c *captureConn) DAVE() godave.Session                                   { return nil }
func (c *captureConn) SetSpeaking(context.Context, voice.SpeakingFlags) error { return nil }

type captureUDP struct {
	voice.UDPConn

	mu     sync.Mutex
	frames [][]byte
	at     []time.Time
}

func (u *captureUDP) Write(p []byte) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.frames = append(u.frames, append([]byte(nil), p...))
	u.at = append(u.at, time.Now())
	return len(p), nil
}

func (u *captureUDP) snapshot() ([][]byte, []time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([][]byte(nil), u.frames...), append([]time.Time(nil), u.at...)
}

func makeTone(t *testing.T, seconds float64) string {
	t.Helper()

	bin, err := ffmpeg.Resolve()
	if err != nil {
		t.Skipf("ffmpeg unavailable: %v", err)
	}

	path := filepath.Join(t.TempDir(), "tone.wav")
	out, err := exec.Command(bin,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "lavfi",
		"-i", "sine=frequency=440:duration="+time.Duration(seconds*float64(time.Second)).String(),
		path,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("generate tone: %v\n%s", err, out)
	}
	return path
}

// A file decoded by ffmpeg must reach the connection as Opus, at the wire
// cadence rather than as fast as it decodes.
func TestFilePlaysThroughToTheConnectionAtWireRate(t *testing.T) {
	path := makeTone(t, 10)

	pipe, err := pipeline.New(slog.New(slog.DiscardHandler), 100, audio.DefaultBitrate)
	if err != nil {
		t.Fatalf("pipeline.New: %v", err)
	}
	defer pipe.Close()

	src, err := ffmpeg.OpenFile(context.Background(), path, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	pipe.SetFileSource(src, 0)

	conn := &captureConn{udp: &captureUDP{}}
	sender := pacer.New(nil, pipe, conn)
	sender.Open()
	defer sender.Close()

	time.Sleep(2 * time.Second)
	frames, at := conn.udp.snapshot()

	// Two seconds at 20 ms per frame is 100 frames. Anything close to the
	// hundreds would mean the decode rate was leaking through to the wire.
	if len(frames) < 80 || len(frames) > 120 {
		t.Fatalf("sent %d frames in 2s, want about 100", len(frames))
	}

	elapsed := at[len(at)-1].Sub(at[0])
	ideal := time.Duration(len(at)-1) * 20 * time.Millisecond
	t.Logf("%d frames over %v (ideal %v)", len(frames), elapsed, ideal)
	if diff := (elapsed - ideal).Abs(); diff > 60*time.Millisecond {
		t.Errorf("cumulative timing error %v over %d frames", diff, len(frames))
	}

	// The first frames go out while the buffer is still filling, and are
	// encoded silence by design; a 440 Hz tone is much larger than that.
	const priming = 5
	for i, f := range frames {
		if len(f) > audio.MaxOpusFrameSize {
			t.Fatalf("frame %d is %d bytes, over the encoder's bound", i, len(f))
		}
		if i >= priming && len(f) < 10 {
			t.Fatalf("frame %d is %d bytes, too small to be encoded audio", i, len(f))
		}
	}

	if pos := pipe.Position(); pos < 1500*time.Millisecond || pos > 2500*time.Millisecond {
		t.Errorf("Position = %v after 2s of playback", pos)
	}
}

// Muting must reach the wire, not just the meter: a silent signal encodes to
// far fewer bytes than a tone.
func TestVolumeChangeReachesTheEncodedStream(t *testing.T) {
	path := makeTone(t, 10)

	pipe, err := pipeline.New(slog.New(slog.DiscardHandler), 100, audio.DefaultBitrate)
	if err != nil {
		t.Fatalf("pipeline.New: %v", err)
	}
	defer pipe.Close()

	src, err := ffmpeg.OpenFile(context.Background(), path, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	pipe.SetFileSource(src, 0)

	conn := &captureConn{udp: &captureUDP{}}
	sender := pacer.New(nil, pipe, conn)
	sender.Open()
	defer sender.Close()

	time.Sleep(600 * time.Millisecond)
	loudFrames, _ := conn.udp.snapshot()

	pipe.SetVolume(0)
	time.Sleep(600 * time.Millisecond)
	allFrames, _ := conn.udp.snapshot()

	if len(loudFrames) < 10 || len(allFrames) <= len(loudFrames)+10 {
		t.Fatalf("not enough frames to compare: %d then %d", len(loudFrames), len(allFrames))
	}

	// Skip a few frames either side of the change so the gain ramp and the
	// encoder settling are not counted.
	loud := meanSize(loudFrames[5 : len(loudFrames)-3])
	quiet := meanSize(allFrames[len(loudFrames)+5:])

	t.Logf("mean frame size: %.1f bytes at full volume, %.1f muted", loud, quiet)
	if quiet >= loud/2 {
		t.Fatalf("muting did not shrink the encoded frames: %.1f then %.1f", loud, quiet)
	}
}

func meanSize(frames [][]byte) float64 {
	if len(frames) == 0 {
		return 0
	}
	total := 0
	for _, f := range frames {
		total += len(f)
	}
	return float64(total) / float64(len(frames))
}
