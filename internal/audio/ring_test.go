package audio

import (
	"testing"
	"time"
)

func marker(v int16) []int16 { return constFrame(v) }

func TestRingRoundTripsFramesInOrder(t *testing.T) {
	r := NewRing(4, false)
	for i := range int16(3) {
		if !r.Write(marker(i)) {
			t.Fatalf("write %d rejected", i)
		}
	}
	if got := r.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}

	dst := make([]int16, SamplesPerFrame)
	for i := range int16(3) {
		if !r.Read(dst) {
			t.Fatalf("read %d found the buffer empty", i)
		}
		if dst[0] != i {
			t.Fatalf("read %d got marker %d", i, dst[0])
		}
	}
}

// Live capture cannot be throttled, so a full buffer must discard its oldest
// frame and keep the newest audio rather than block the reader.
func TestRingDropsOldestWhenFull(t *testing.T) {
	r := NewRing(2, true)
	for i := range int16(4) {
		if !r.Write(marker(i)) {
			t.Fatalf("write %d rejected", i)
		}
	}

	if got := r.Dropped(); got != 2 {
		t.Fatalf("Dropped = %d, want 2", got)
	}

	dst := make([]int16, SamplesPerFrame)
	for _, want := range []int16{2, 3} {
		if !r.Read(dst) {
			t.Fatal("buffer unexpectedly empty")
		}
		if dst[0] != want {
			t.Fatalf("got marker %d, want %d", dst[0], want)
		}
	}
}

// File playback can be throttled, so a full buffer must block the writer, which
// back-pressures through the pipe and makes ffmpeg wait.
func TestRingBlocksWriterWhenFull(t *testing.T) {
	r := NewRing(1, false)
	if !r.Write(marker(1)) {
		t.Fatal("first write rejected")
	}

	blocked := make(chan struct{})
	go func() {
		r.Write(marker(2))
		close(blocked)
	}()

	select {
	case <-blocked:
		t.Fatal("write returned while the buffer was full")
	case <-time.After(50 * time.Millisecond):
	}

	dst := make([]int16, SamplesPerFrame)
	r.Read(dst)

	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("write stayed blocked after a frame was consumed")
	}

	if got := r.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d, want 0 for a blocking ring", got)
	}
}

func TestRingCountsUnderruns(t *testing.T) {
	r := NewRing(2, true)
	dst := make([]int16, SamplesPerFrame)
	for range 3 {
		if r.Read(dst) {
			t.Fatal("read reported a frame from an empty buffer")
		}
	}
	if got := r.Underruns(); got != 3 {
		t.Fatalf("Underruns = %d, want 3", got)
	}
}

func TestRingDrainDiscardsWithoutCountingDrops(t *testing.T) {
	r := NewRing(4, true)
	r.Write(marker(1))
	r.Write(marker(2))
	r.Drain()

	if got := r.Len(); got != 0 {
		t.Fatalf("Len after Drain = %d, want 0", got)
	}
	if got := r.Dropped(); got != 0 {
		t.Fatalf("Dropped after Drain = %d, want 0", got)
	}
}

func TestRingCloseReleasesBlockedWriter(t *testing.T) {
	r := NewRing(1, false)
	r.Write(marker(1))

	done := make(chan bool, 1)
	go func() { done <- r.Write(marker(2)) }()

	time.Sleep(20 * time.Millisecond)
	r.Close()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("write after Close reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not release the blocked writer")
	}

	if !r.Closed() {
		t.Fatal("Closed reported false after Close")
	}
}

// Frames already buffered stay readable after Close so a track can finish.
func TestRingDrainsAfterClose(t *testing.T) {
	r := NewRing(2, false)
	r.Write(marker(7))
	r.Close()

	dst := make([]int16, SamplesPerFrame)
	if !r.Read(dst) || dst[0] != 7 {
		t.Fatal("buffered frame was lost on Close")
	}
	if r.Read(dst) {
		t.Fatal("read succeeded on a drained, closed ring")
	}
	if got := r.Underruns(); got != 0 {
		t.Fatalf("Underruns = %d, want 0 (a closed ring is not underrunning)", got)
	}
}
