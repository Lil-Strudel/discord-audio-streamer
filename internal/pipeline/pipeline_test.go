package pipeline

import (
	"encoding/binary"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/voice"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
)

var _ voice.OpusFrameProvider = (*Pipeline)(nil)

// fakeSource is a PCM source backed by a byte slice, optionally one that never
// ends, so live-capture behaviour can be exercised without a device.
type fakeSource struct {
	mu     sync.Mutex
	data   []byte
	pos    int
	block  chan struct{} // when set, reads park here instead of returning EOF
	closed bool
}

func (f *fakeSource) Read(p []byte) (int, error) {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return 0, io.EOF
	}
	if f.pos >= len(f.data) {
		block := f.block
		f.mu.Unlock()
		if block == nil {
			return 0, io.EOF
		}
		<-block
		return 0, io.EOF
	}
	n := copy(p, f.data[f.pos:])
	f.pos += n
	f.mu.Unlock()
	return n, nil
}

func (f *fakeSource) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		if f.block != nil {
			close(f.block)
			f.block = nil
		}
	}
	return nil
}

func (f *fakeSource) Wait() error { return nil }

func tonePCM(frames int, amplitude int16) []byte {
	buf := make([]byte, frames*audio.BytesPerFrame)
	for i := range frames * audio.SamplesPerFrame {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(amplitude))
	}
	return buf
}

func newPipeline(t *testing.T) *Pipeline {
	t.Helper()
	p, err := New(nil, 100, audio.DefaultBitrate)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// drain pulls frames the way the pacer would, without the wait.
func drain(t *testing.T, p *Pipeline, n int) [][]byte {
	t.Helper()
	var out [][]byte
	for range n {
		frame, err := p.ProvideOpusFrame()
		if err != nil {
			t.Fatalf("ProvideOpusFrame: %v", err)
		}
		out = append(out, frame)
	}
	return out
}

func TestIdlePipelineProducesNothing(t *testing.T) {
	p := newPipeline(t)
	if p.Active() {
		t.Error("a fresh pipeline reports an active source")
	}
	for _, frame := range drain(t, p, 3) {
		if len(frame) != 0 {
			t.Fatalf("idle pipeline produced a %d byte frame", len(frame))
		}
	}
}

func TestFileSourceProducesFramesThenStops(t *testing.T) {
	p := newPipeline(t)
	const frames = 4
	p.SetFileSource(&fakeSource{data: tonePCM(frames, 8000)}, 0)

	if !waitFor(t, time.Second, func() bool { return p.Stats().BufferedFrames >= frames }) {
		t.Fatalf("source never filled the buffer: %+v", p.Stats())
	}

	for i := range frames {
		frame, err := p.ProvideOpusFrame()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if len(frame) == 0 {
			t.Fatalf("frame %d was empty while audio was still buffered", i)
		}
	}

	// Once the file runs out the pipeline must go quiet, so the sender stops
	// speaking rather than streaming silence forever.
	if !waitFor(t, time.Second, func() bool {
		frame, _ := p.ProvideOpusFrame()
		return len(frame) == 0
	}) {
		t.Fatal("pipeline kept producing after the source ended")
	}
}

// Stream health is shown during playback, so reads after a file has ended must
// not be counted as underruns: the source is finished, not starved.
func TestFinishedFileDoesNotCountUnderruns(t *testing.T) {
	p := newPipeline(t)
	p.SetFileSource(&fakeSource{data: tonePCM(2, 8000)}, 0)

	if !waitFor(t, time.Second, func() bool { return p.Stats().BufferedFrames >= 2 }) {
		t.Fatalf("source never filled the buffer: %+v", p.Stats())
	}
	if !waitFor(t, time.Second, func() bool { return p.current.sourceDone.Load() }) {
		t.Fatal("source never reported finishing")
	}

	drain(t, p, 50)
	if got := p.Stats().Underruns; got != 0 {
		t.Fatalf("Underruns = %d after the file ended, want 0", got)
	}
}

// A gap mid-stream is not the same as the end of a stream: it must be filled
// with silence so Discord does not cut the tail off every hiccup.
func TestLiveSourceFillsGapsWithSilence(t *testing.T) {
	p := newPipeline(t)
	src := &fakeSource{data: tonePCM(1, 8000), block: make(chan struct{})}
	p.SetCaptureSource(src, DefaultCaptureFrames)

	if !waitFor(t, time.Second, func() bool { return p.Stats().BufferedFrames >= 1 }) {
		t.Fatal("capture source never produced a frame")
	}

	drain(t, p, 1) // consume the one real frame

	// The source is now starved but still running.
	for i := range 3 {
		frame, err := p.ProvideOpusFrame()
		if err != nil {
			t.Fatalf("underrun frame %d: %v", i, err)
		}
		if len(frame) == 0 {
			t.Fatalf("underrun frame %d was empty, want encoded silence", i)
		}
	}

	if got := p.Stats().Underruns; got < 3 {
		t.Errorf("Underruns = %d, want at least 3", got)
	}
}

func TestPauseStopsOutputAndResumeContinues(t *testing.T) {
	p := newPipeline(t)
	p.SetFileSource(&fakeSource{data: tonePCM(20, 8000)}, 0)
	waitFor(t, time.Second, func() bool { return p.Stats().BufferedFrames > 0 })

	drain(t, p, 1)
	p.SetPaused(true)

	if !p.Paused() {
		t.Error("Paused reported false after SetPaused(true)")
	}
	for _, frame := range drain(t, p, 3) {
		if len(frame) != 0 {
			t.Fatal("pipeline produced audio while paused")
		}
	}

	positionWhilePaused := p.Position()
	p.SetPaused(false)
	drain(t, p, 2)

	if p.Position() <= positionWhilePaused {
		t.Error("position did not advance after resuming")
	}
}

// Position must not advance while paused, or the seek bar creeps forward during
// a pause.
func TestPositionTracksFramesSentFromOffset(t *testing.T) {
	p := newPipeline(t)
	p.SetFileSource(&fakeSource{data: tonePCM(20, 8000)}, 30*time.Second)
	waitFor(t, time.Second, func() bool { return p.Stats().BufferedFrames > 0 })

	if got := p.Position(); got != 30*time.Second {
		t.Fatalf("Position before any frame = %v, want the seek offset", got)
	}

	drain(t, p, 10)

	want := 30*time.Second + 10*audio.FrameDurationMs*time.Millisecond
	if got := p.Position(); got != want {
		t.Fatalf("Position = %v, want %v", got, want)
	}

	p.SetPaused(true)
	drain(t, p, 5)
	if got := p.Position(); got != want {
		t.Fatalf("Position advanced to %v while paused", got)
	}
}

// Gain is applied at the last moment rather than on the way into the buffer, so
// a volume change lands on the next frame instead of waiting for buffered audio
// to drain.
func TestVolumeChangeAppliesImmediatelyDespiteBuffering(t *testing.T) {
	p := newPipeline(t)
	p.SetFileSource(&fakeSource{data: tonePCM(20, math.MaxInt16/2)}, 0)

	if !waitFor(t, time.Second, func() bool { return p.Stats().BufferedFrames >= 5 }) {
		t.Fatal("buffer never filled")
	}

	drain(t, p, 2)
	loud, _ := p.Stats().RMS, 0
	if loud == 0 {
		t.Fatal("meter read silence for a loud source")
	}

	p.SetVolume(0)
	// One frame for the ramp to travel, one to measure at the new level.
	drain(t, p, 2)

	if quiet := p.Stats().RMS; quiet > loud/10 {
		t.Fatalf("RMS was %v after muting, was %v before; the change waited for the buffer to drain", quiet, loud)
	}
}

func TestVolumeRoundTrips(t *testing.T) {
	p := newPipeline(t)
	p.SetVolume(75)
	if got := p.Volume(); math.Abs(got-75) > 0.001 {
		t.Fatalf("Volume = %v, want 75", got)
	}
}

// Bitrate changes come from the UI thread but libopus encoder state belongs to
// the encoding goroutine, so they are deferred to the next frame.
func TestBitrateChangeIsAppliedOnTheEncodingGoroutine(t *testing.T) {
	p := newPipeline(t)
	p.SetFileSource(&fakeSource{data: tonePCM(20, 8000)}, 0)
	waitFor(t, time.Second, func() bool { return p.Stats().BufferedFrames > 0 })

	p.SetBitrate(audio.MaxBitrate)
	drain(t, p, 1)

	if got, err := p.enc.Bitrate(); err != nil {
		t.Fatalf("Bitrate: %v", err)
	} else if got != audio.MaxBitrate {
		t.Fatalf("Bitrate = %d, want %d", got, audio.MaxBitrate)
	}
}

func TestSwappingSourceClosesThePreviousOne(t *testing.T) {
	p := newPipeline(t)
	first := &fakeSource{data: tonePCM(50, 8000), block: make(chan struct{})}
	p.SetFileSource(first, 0)
	waitFor(t, time.Second, func() bool { return p.Stats().BufferedFrames > 0 })

	p.SetFileSource(&fakeSource{data: tonePCM(5, 4000)}, 0)

	first.mu.Lock()
	closed := first.closed
	first.mu.Unlock()
	if !closed {
		t.Fatal("the previous source was left running after a swap")
	}
}

func TestStopGoesIdle(t *testing.T) {
	p := newPipeline(t)
	src := &fakeSource{data: tonePCM(50, 8000)}
	p.SetFileSource(src, 0)
	waitFor(t, time.Second, func() bool { return p.Stats().BufferedFrames > 0 })

	p.Stop()

	if p.Active() {
		t.Error("Active reported true after Stop")
	}
	if p.Position() != 0 {
		t.Errorf("Position = %v after Stop, want 0", p.Position())
	}
	for _, frame := range drain(t, p, 2) {
		if len(frame) != 0 {
			t.Fatal("pipeline produced audio after Stop")
		}
	}
}

func TestTrackEndCallbackFiresOnceForFiles(t *testing.T) {
	p := newPipeline(t)

	var calls atomic.Int32
	p.SetOnTrackEnd(func() { calls.Add(1) })
	p.SetFileSource(&fakeSource{data: tonePCM(2, 8000)}, 0)

	waitFor(t, 2*time.Second, func() bool {
		p.ProvideOpusFrame()
		return calls.Load() > 0
	})
	if calls.Load() == 0 {
		t.Fatal("track end callback never fired")
	}

	for range 10 {
		p.ProvideOpusFrame()
	}
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("track end callback fired %d times, want once", got)
	}
}

// A live capture has no end, so a starved device must not be reported as a
// finished track.
func TestTrackEndDoesNotFireForCapture(t *testing.T) {
	p := newPipeline(t)

	var calls atomic.Int32
	p.SetOnTrackEnd(func() { calls.Add(1) })
	p.SetCaptureSource(&fakeSource{data: tonePCM(1, 8000)}, DefaultCaptureFrames)

	for range 20 {
		p.ProvideOpusFrame()
	}
	time.Sleep(50 * time.Millisecond)

	if got := calls.Load(); got != 0 {
		t.Fatalf("track end fired %d times for a capture source", got)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	p, err := New(nil, 100, audio.DefaultBitrate)
	if err != nil {
		t.Fatal(err)
	}
	p.SetFileSource(&fakeSource{data: tonePCM(5, 8000)}, 0)

	p.Close()
	p.Close()

	frame, err := p.ProvideOpusFrame()
	if err != nil || len(frame) != 0 {
		t.Fatalf("closed pipeline returned %d bytes, %v", len(frame), err)
	}
}

func TestClampCaptureFrames(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{0, MinCaptureFrames},
		{1, MinCaptureFrames},
		{5, 5},
		{100, MaxCaptureFrames},
	} {
		if got := clampCaptureFrames(tc.in); got != tc.want {
			t.Errorf("clampCaptureFrames(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// fakeMixer renders a constant level while on is set.
type fakeMixer struct {
	on    atomic.Bool
	level int16
}

func (m *fakeMixer) Mix(dst []int16) bool {
	if !m.on.Load() {
		return false
	}
	for i := range dst {
		dst[i] = m.level
	}
	return true
}

func (m *fakeMixer) BufferStats() (int, int, uint64) { return 3, 8, 1 }

func TestMixerFramesAreEncodedAndSilenceSendsNothing(t *testing.T) {
	p := newPipeline(t)
	m := &fakeMixer{level: 8000}
	p.SetMixer(m)

	if !p.Active() {
		t.Error("a pipeline with a mixer reports no source")
	}
	for _, frame := range drain(t, p, 2) {
		if len(frame) != 0 {
			t.Fatal("a mixer with nothing playing produced a frame")
		}
	}

	m.on.Store(true)
	for _, frame := range drain(t, p, 2) {
		if len(frame) == 0 {
			t.Fatal("a playing mixer produced an empty frame")
		}
	}
	if stats := p.Stats(); stats.BufferedFrames != 3 || stats.BufferCapacity != 8 || stats.Underruns != 1 {
		t.Fatalf("stats = %+v, want the mixer's buffer figures", stats)
	}
}

func TestFileSourceAndMixerReplaceEachOther(t *testing.T) {
	p := newPipeline(t)
	src := &fakeSource{data: tonePCM(50, 8000)}
	p.SetFileSource(src, 0)

	m := &fakeMixer{}
	p.SetMixer(m)
	if !src.closed {
		t.Fatal("attaching a mixer left the file source running")
	}

	// The mixer is silent, so anything produced now would be the old file.
	for _, frame := range drain(t, p, 3) {
		if len(frame) != 0 {
			t.Fatal("the replaced file source is still being played")
		}
	}

	m.on.Store(true)
	p.SetFileSource(&fakeSource{data: tonePCM(1, 0)}, 0)
	p.Stop()
	if p.Active() {
		t.Fatal("Stop left the pipeline active")
	}
	for _, frame := range drain(t, p, 3) {
		if len(frame) != 0 {
			t.Fatal("a detached mixer is still being played")
		}
	}
}
