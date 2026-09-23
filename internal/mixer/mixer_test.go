package mixer

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/ffmpeg"
)

// fakeSource is a PCM source backed by a byte slice.
type fakeSource struct {
	mu     sync.Mutex
	data   []byte
	pos    int
	closed bool
}

func (f *fakeSource) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.pos >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.pos:])
	f.pos += n
	return n, nil
}

func (f *fakeSource) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeSource) Wait() error { return nil }

func (f *fakeSource) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// library hands out a fresh constant-amplitude source for each file name, and
// remembers every source it opened.
type library struct {
	mu     sync.Mutex
	files  map[string]fakeFile
	opened map[string][]*fakeSource
}

type fakeFile struct {
	frames    int
	amplitude int16
}

func newLibrary(files map[string]fakeFile) *library {
	return &library{files: files, opened: map[string][]*fakeSource{}}
}

func (l *library) open(path string) (ffmpeg.Source, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, ok := l.files[path]
	if !ok {
		return nil, errors.New("no such file")
	}
	src := &fakeSource{data: tonePCM(f.frames, f.amplitude)}
	l.opened[path] = append(l.opened[path], src)
	return src, nil
}

func (l *library) opens(path string) []*fakeSource {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*fakeSource(nil), l.opened[path]...)
}

func tonePCM(frames int, amplitude int16) []byte {
	buf := make([]byte, frames*audio.BytesPerFrame)
	for i := range frames * audio.SamplesPerFrame {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(amplitude))
	}
	return buf
}

func newMixer(t *testing.T, lib *library) *Mixer {
	t.Helper()
	m := New(nil, lib.open)
	t.Cleanup(m.StopAll)
	return m
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}

// buffered waits until channel ch has at least n frames decoded.
func buffered(t *testing.T, m *Mixer, ch, n int) {
	t.Helper()
	ok := waitFor(t, time.Second, func() bool {
		c := m.channels[ch]
		c.mu.Lock()
		v := c.current
		c.mu.Unlock()
		return v != nil && (v.ring.Len() >= n || v.sourceDone.Load())
	})
	if !ok {
		t.Fatalf("channel %d never buffered %d frames", ch, n)
	}
}

func TestIdleMixerProducesNothing(t *testing.T) {
	m := newMixer(t, newLibrary(nil))
	dst := make([]int16, audio.SamplesPerFrame)
	if m.Mix(dst) {
		t.Fatal("an idle mixer reported audio")
	}
}

func TestChannelsAreSummed(t *testing.T) {
	lib := newLibrary(map[string]fakeFile{
		"a": {frames: 10, amplitude: 1000},
		"b": {frames: 10, amplitude: 2000},
	})
	m := newMixer(t, lib)

	if err := m.Play(0, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Play(1, "b"); err != nil {
		t.Fatal(err)
	}
	buffered(t, m, 0, 5)
	buffered(t, m, 1, 5)

	dst := make([]int16, audio.SamplesPerFrame)
	if !m.Mix(dst) {
		t.Fatal("Mix reported silence with two channels playing")
	}
	if dst[0] != 3000 || dst[len(dst)-1] != 3000 {
		t.Fatalf("mixed sample = %d, want 3000", dst[0])
	}
}

func TestSumSaturatesInsteadOfWrapping(t *testing.T) {
	lib := newLibrary(map[string]fakeFile{
		"loud": {frames: 10, amplitude: 30000},
	})
	m := newMixer(t, lib)
	for ch := range 2 {
		if err := m.Play(ch, "loud"); err != nil {
			t.Fatal(err)
		}
		buffered(t, m, ch, 5)
	}

	dst := make([]int16, audio.SamplesPerFrame)
	m.Mix(dst)
	if dst[0] != math.MaxInt16 {
		t.Fatalf("mixed sample = %d, want %d", dst[0], math.MaxInt16)
	}
}

func TestChannelVolumeIsIndependent(t *testing.T) {
	lib := newLibrary(map[string]fakeFile{
		"a": {frames: 10, amplitude: 1000},
		"b": {frames: 10, amplitude: 1000},
	})
	m := newMixer(t, lib)
	_ = m.SetVolume(0, 0)
	m.Play(0, "a")
	m.Play(1, "b")
	buffered(t, m, 0, 5)
	buffered(t, m, 1, 5)

	dst := make([]int16, audio.SamplesPerFrame)
	m.Mix(dst) // the first frame ramps channel 0 down from its old gain
	m.Mix(dst)
	if dst[0] != 1000 {
		t.Fatalf("mixed sample = %d, want only channel 1's 1000", dst[0])
	}

	stats := m.Stats()
	if stats[0].RMS != 0 || stats[1].RMS == 0 {
		t.Fatalf("levels = %+v, want channel 0 silent and channel 1 audible", stats)
	}
}

func TestPausedChannelIsSilent(t *testing.T) {
	lib := newLibrary(map[string]fakeFile{"a": {frames: 10, amplitude: 1000}})
	m := newMixer(t, lib)
	m.Play(0, "a")
	buffered(t, m, 0, 5)

	_ = m.SetPaused(0, true)
	dst := make([]int16, audio.SamplesPerFrame)
	if m.Mix(dst) {
		t.Fatal("a paused channel produced audio")
	}

	_ = m.SetPaused(0, false)
	if !m.Mix(dst) {
		t.Fatal("a resumed channel produced nothing")
	}
}

func TestReplaceFadesOutAndClosesTheOldSound(t *testing.T) {
	lib := newLibrary(map[string]fakeFile{
		"old": {frames: 100, amplitude: 10000},
		"new": {frames: 100, amplitude: 10000},
	})
	m := newMixer(t, lib)
	m.Play(0, "old")
	buffered(t, m, 0, 5)

	m.Play(0, "new")
	buffered(t, m, 0, 5)

	dst := make([]int16, audio.SamplesPerFrame)
	m.Mix(dst)

	// The first frame is the old sound fading out plus the new one at full
	// level. At the very end of the frame the old one has ramped to nothing.
	if first, last := dst[0], dst[len(dst)-1]; first <= last {
		t.Fatalf("frame ran %d to %d, want the old sound fading out across it", first, last)
	}

	old := lib.opens("old")[0]
	if !waitFor(t, time.Second, old.isClosed) {
		t.Fatal("the replaced sound's decoder was never closed")
	}
	if snap := m.Snapshot(); snap[0].Path != "new" {
		t.Fatalf("channel 0 is playing %q, want new", snap[0].Path)
	}
}

func TestFinishedSoundFiresEndOnce(t *testing.T) {
	lib := newLibrary(map[string]fakeFile{"a": {frames: 3, amplitude: 1000}})
	m := newMixer(t, lib)

	var ends atomic.Int32
	m.SetOnEnd(func(ch int) {
		if ch == 2 {
			ends.Add(1)
		}
	})
	m.Play(2, "a")
	buffered(t, m, 2, 3)

	dst := make([]int16, audio.SamplesPerFrame)
	for range 3 {
		if !m.Mix(dst) {
			t.Fatal("a sound went quiet before its last frame")
		}
	}
	for range 5 {
		if m.Mix(dst) {
			t.Fatal("the mixer kept producing after the sound ended")
		}
	}

	if !waitFor(t, time.Second, func() bool { return ends.Load() == 1 }) {
		t.Fatalf("end fired %d times, want 1", ends.Load())
	}
	if snap := m.Snapshot(); snap[2].Path != "" {
		t.Fatalf("channel 2 still reports %q after finishing", snap[2].Path)
	}
	if _, _, underruns := m.BufferStats(); underruns != 0 {
		t.Fatalf("underruns = %d for a sound that was fully buffered", underruns)
	}
}

func TestLoopingSoundStartsOver(t *testing.T) {
	lib := newLibrary(map[string]fakeFile{"a": {frames: 2, amplitude: 1000}})
	m := newMixer(t, lib)
	_ = m.SetLoop(0, true)
	m.Play(0, "a")

	dst := make([]int16, audio.SamplesPerFrame)
	for i := range 12 {
		buffered(t, m, 0, 1)
		if !m.Mix(dst) {
			t.Fatalf("looping sound went quiet at frame %d", i)
		}
	}
	if got := len(lib.opens("a")); got < 6 {
		t.Fatalf("file opened %d times across 12 frames of a 2-frame loop", got)
	}

	// Turning the loop off lets the current pass finish, then stops.
	_ = m.SetLoop(0, false)
	if !waitFor(t, time.Second, func() bool {
		m.Mix(dst)
		return m.Snapshot()[0].Path == ""
	}) {
		t.Fatal("sound kept looping after loop was turned off")
	}
}

func TestStopAllClosesEverything(t *testing.T) {
	lib := newLibrary(map[string]fakeFile{"a": {frames: 100, amplitude: 1000}})
	m := newMixer(t, lib)
	for ch := range Channels {
		m.Play(ch, "a")
	}

	m.StopAll()

	for _, src := range lib.opens("a") {
		if !src.isClosed() {
			t.Fatal("StopAll left a decoder running")
		}
	}
	dst := make([]int16, audio.SamplesPerFrame)
	if m.Mix(dst) {
		t.Fatal("mixer produced audio after StopAll")
	}
}

func TestBadChannelIsRejected(t *testing.T) {
	m := newMixer(t, newLibrary(nil))
	if err := m.Play(Channels, "a"); !errors.Is(err, ErrNoChannel) {
		t.Fatalf("Play on channel %d: err = %v, want ErrNoChannel", Channels, err)
	}
}
