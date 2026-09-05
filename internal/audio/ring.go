package audio

import (
	"sync"
	"sync/atomic"
)

// Ring is a fixed-capacity queue of PCM frames decoupling the goroutine that
// reads from ffmpeg from the goroutine that paces frames onto the wire.
//
// The two audio paths need opposite behaviour when the buffer fills, so the
// policy is a constructor choice:
//
//   - File playback can be throttled. Blocking the writer back-pressures through
//     the pipe and simply makes ffmpeg wait, which is free and lossless.
//   - Live capture cannot. Blocking would stall the reader until the OS capture
//     buffer overflows and the driver drops samples for us, at an arbitrary
//     point and without telling us. Dropping the oldest frame here instead keeps
//     latency bounded and the loss accounted for.
type Ring struct {
	mu       sync.Mutex
	notEmpty *sync.Cond
	notFull  *sync.Cond

	frames [][]int16
	head   int // next slot to read
	count  int

	dropOldest bool
	closed     bool

	dropped   atomic.Uint64
	underruns atomic.Uint64
}

// NewRing returns a Ring holding up to capacity frames. If dropOldest is true a
// full buffer discards its oldest frame to make room; otherwise writers block
// until space is available.
func NewRing(capacity int, dropOldest bool) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	r := &Ring{
		frames:     make([][]int16, capacity),
		dropOldest: dropOldest,
	}
	for i := range r.frames {
		r.frames[i] = make([]int16, SamplesPerFrame)
	}
	r.notEmpty = sync.NewCond(&r.mu)
	r.notFull = sync.NewCond(&r.mu)
	return r
}

// Write copies frame into the buffer. It reports false once the Ring is closed.
//
// The frame is copied rather than retained because callers reuse their frame
// slice; see [Framer.ReadFrame].
func (r *Ring) Write(frame []int16) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for r.count == len(r.frames) && !r.closed {
		if r.dropOldest {
			r.head = (r.head + 1) % len(r.frames)
			r.count--
			r.dropped.Add(1)
			break
		}
		r.notFull.Wait()
	}
	if r.closed {
		return false
	}

	tail := (r.head + r.count) % len(r.frames)
	copy(r.frames[tail], frame)
	r.count++
	r.notEmpty.Signal()
	return true
}

// Read copies the oldest frame into dst and reports whether one was available.
//
// It never blocks: the pacer runs to a hard 20 ms deadline and must not be held
// up by a starved producer. An empty buffer is an underrun, which the caller
// answers with a silence frame.
func (r *Ring) Read(dst []int16) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.count == 0 {
		if !r.closed {
			r.underruns.Add(1)
		}
		return false
	}

	copy(dst, r.frames[r.head])
	r.head = (r.head + 1) % len(r.frames)
	r.count--
	r.notFull.Signal()
	return true
}

// Len returns the number of buffered frames.
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// Cap returns the capacity in frames.
func (r *Ring) Cap() int { return len(r.frames) }

// Dropped returns how many frames have been discarded on overflow.
func (r *Ring) Dropped() uint64 { return r.dropped.Load() }

// Underruns returns how many reads found the buffer empty.
func (r *Ring) Underruns() uint64 { return r.underruns.Load() }

// Drain discards all buffered frames without counting them as dropped. It is
// used when playback is stopped or the source is switched, so stale audio is not
// emitted after the change.
func (r *Ring) Drain() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.head = 0
	r.count = 0
	r.notFull.Broadcast()
}

// Close releases any blocked writer and makes subsequent writes fail. Frames
// already buffered stay readable so playback can finish cleanly.
func (r *Ring) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.notFull.Broadcast()
	r.notEmpty.Broadcast()
}

// Closed reports whether the Ring has been closed.
func (r *Ring) Closed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}
