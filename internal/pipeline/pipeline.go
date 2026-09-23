// Package pipeline joins an ffmpeg PCM source to the voice connection.
//
// It implements voice.OpusFrameProvider, so the pacer pulls frames from it at
// the wire cadence. Reading from ffmpeg happens on its own goroutine and is
// separated from that pull by a ring buffer, because the two run at unrelated
// rates: a file decodes far faster than real time, and a capture device
// delivers whenever the driver feels like it.
//
// One Pipeline lives for as long as the voice connection and swaps its source
// underneath, rather than being rebuilt per track. That keeps the Opus encoder
// and the pacer's clock stable across a track change, so switching tracks does
// not restart the schedule or flap the speaking indicator.
package pipeline

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/ffmpeg"
)

// Buffer depth, in 20 ms frames.
const (
	// filePlaybackFrames is generous because it costs nothing: decoding is far
	// ahead of playback and a blocking writer simply makes ffmpeg wait.
	filePlaybackFrames = 10

	// Live capture buffering trades latency against tolerance for jitter. Five
	// frames is 100 ms, which absorbs ordinary scheduling noise without being
	// audible as delay.
	MinCaptureFrames     = 3
	MaxCaptureFrames     = 15
	DefaultCaptureFrames = 5
)

// Stats describes what the pipeline is doing, for display as stream health.
type Stats struct {
	// BufferedFrames is the current ring occupancy.
	BufferedFrames int `json:"bufferedFrames"`

	// BufferCapacity is the ring size in frames.
	BufferCapacity int `json:"bufferCapacity"`

	// DroppedFrames counts frames discarded because capture outran the wire.
	DroppedFrames uint64 `json:"droppedFrames"`

	// Underruns counts frames the wire asked for that were not ready, each of
	// which was filled with silence.
	Underruns uint64 `json:"underruns"`

	// RMS and Peak are the levels of what is actually being sent, after gain.
	RMS  float64 `json:"rms"`
	Peak float64 `json:"peak"`
}

// Pipeline turns a PCM source into Opus frames on demand.
type Pipeline struct {
	logger *slog.Logger
	gain   *audio.Gain
	meter  *audio.Meter
	enc    *audio.Encoder

	// scratch and silence are only touched from the frame-provider goroutine.
	scratch []int16
	silence []int16

	mu      sync.RWMutex
	current *stream
	mixer   Mixer

	pendingBitrate atomic.Int64
	paused         atomic.Bool
	closed         atomic.Bool

	// onTrackEnd fires once when a finite source runs out.
	onTrackEnd atomic.Pointer[func()]
}

// New creates an idle Pipeline. It produces nothing until a source is set.
func New(logger *slog.Logger, volumePercent float64, bitrate int) (*Pipeline, error) {
	if logger == nil {
		logger = slog.Default()
	}

	enc, err := audio.NewEncoder(audio.EncoderConfig{
		Bitrate: bitrate,
		// Live capture and playback share one encoder, and a little forward
		// error correction costs almost nothing while making a lost packet
		// degrade rather than leave a hole.
		PacketLossPercent: 2,
	})
	if err != nil {
		return nil, err
	}

	return &Pipeline{
		logger:  logger.With(slog.String("component", "pipeline")),
		gain:    audio.NewGain(volumePercent),
		meter:   audio.NewMeter(),
		enc:     enc,
		scratch: make([]int16, audio.SamplesPerFrame),
		silence: make([]int16, audio.SamplesPerFrame),
	}, nil
}

// Mixer is a source that renders frames on demand rather than through a ring
// buffer, because it sums several buffered sources of its own. The soundboard
// is one.
type Mixer interface {
	// Mix fills dst with the next frame and reports whether there was one.
	// It is called from the pacer goroutine and must not block.
	Mix(dst []int16) bool

	// BufferStats sums the buffer health of whatever the mixer is playing.
	BufferStats() (buffered, capacity int, underruns uint64)
}

// stream is one source and the goroutine feeding its ring buffer.
type stream struct {
	source ffmpeg.Source
	ring   *audio.Ring
	live   bool
	offset time.Duration

	// drained marks the source as finished and the buffer as empty, which is
	// the point at which a track has genuinely ended.
	sourceDone atomic.Bool
	endFired   atomic.Bool

	framesOut atomic.Uint64
	readErr   atomic.Pointer[error]
	done      chan struct{}
}

// SetFileSource plays a decoded file, starting from offset.
func (p *Pipeline) SetFileSource(src ffmpeg.Source, offset time.Duration) {
	p.setSource(src, filePlaybackFrames, false, offset)
}

// SetCaptureSource streams a live capture device with the given buffer depth.
func (p *Pipeline) SetCaptureSource(src ffmpeg.Source, bufferFrames int) {
	p.setSource(src, clampCaptureFrames(bufferFrames), true, 0)
}

func (p *Pipeline) setSource(src ffmpeg.Source, frames int, live bool, offset time.Duration) {
	// A live source must never block its reader: stalling it would let the OS
	// capture buffer overflow and the driver drop samples silently. A file
	// source is throttled by blocking instead, which back-pressures through the
	// pipe and simply makes ffmpeg wait.
	s := &stream{
		source: src,
		ring:   audio.NewRing(frames, live),
		live:   live,
		offset: offset,
		done:   make(chan struct{}),
	}

	p.mu.Lock()
	previous := p.current
	p.current = s
	p.mixer = nil
	p.mu.Unlock()

	closeStream(previous)
	p.paused.Store(false)
	p.meter.Reset()

	go p.produce(s)
}

// SetMixer plays whatever m renders, replacing any file or capture source.
// The mixer's own sources are its owner's to stop; the pipeline only stops
// pulling from it.
func (p *Pipeline) SetMixer(m Mixer) {
	p.mu.Lock()
	previous := p.current
	p.current = nil
	p.mixer = m
	p.mu.Unlock()

	closeStream(previous)
	p.paused.Store(false)
	p.meter.Reset()
}

// Stop discards the current source, or detaches the mixer, and goes idle.
func (p *Pipeline) Stop() {
	p.mu.Lock()
	previous := p.current
	p.current = nil
	p.mixer = nil
	p.mu.Unlock()

	closeStream(previous)
	p.paused.Store(false)
	p.meter.Reset()
}

// produce reads the source and fills the ring until it ends or is closed.
func (p *Pipeline) produce(s *stream) {
	defer close(s.done)
	// Closing the ring once the source has nothing more to give keeps the
	// frames already buffered readable, but stops an empty read from counting
	// as an underrun. Otherwise a finished queue, which leaves its last stream
	// attached, would report fifty underruns a second for as long as it idles.
	defer s.ring.Close()
	defer s.sourceDone.Store(true)

	framer := audio.NewFramer(s.source)
	for {
		frame, err := framer.ReadFrame()
		if err != nil {
			if !errors.Is(err, io.EOF) && !s.ring.Closed() {
				p.logger.Error("source read failed", slog.Any("err", err))
				s.readErr.Store(&err)
			}
			return
		}
		if !s.ring.Write(frame) {
			return // the ring was closed under us
		}
	}
}

// ProvideOpusFrame is called by the pacer once per frame interval.
//
// Returning an empty frame tells the sender to stop speaking, which is what
// pause, stop and end-of-track should do. A gap in the middle of live audio is
// different: that is filled with silence so the stream stays continuous.
func (p *Pipeline) ProvideOpusFrame() ([]byte, error) {
	if p.closed.Load() || p.paused.Load() {
		return nil, nil
	}

	p.mu.RLock()
	s, mixer := p.current, p.mixer
	p.mu.RUnlock()

	if mixer != nil {
		p.applyPendingBitrate()
		// A mixer with nothing playing goes quiet the same way a finished
		// file does, so the speaking indicator goes out between sounds.
		if !mixer.Mix(p.scratch) {
			return nil, nil
		}
		return p.encode()
	}

	if s == nil {
		return nil, nil
	}

	p.applyPendingBitrate()

	if !s.ring.Read(p.scratch) {
		if s.sourceDone.Load() {
			p.fireTrackEnd(s)
			return nil, nil
		}
		// The source is still running but has nothing ready. Sending silence
		// keeps the stream continuous; sending nothing would make Discord think
		// speech had stopped and cut the tail off every hiccup.
		copy(p.scratch, p.silence)
	}

	s.framesOut.Add(1)
	return p.encode()
}

// encode applies the output volume to the frame in p.scratch and encodes it.
//
// Gain is applied here, at the last possible moment, rather than as frames
// enter the buffer. A volume change then takes effect on the very next frame
// instead of waiting for buffered audio to drain.
func (p *Pipeline) encode() ([]byte, error) {
	p.gain.Apply(p.scratch)
	p.meter.Observe(p.scratch)
	return p.enc.Encode(p.scratch)
}

// Close releases the encoder and the current source. The pacer calls this when
// the voice connection goes away.
func (p *Pipeline) Close() {
	if p.closed.Swap(true) {
		return
	}
	p.Stop()
	p.enc.Close()
}

// SetVolume sets the output volume as a percentage. Safe from any goroutine.
func (p *Pipeline) SetVolume(percent float64) { p.gain.SetPercent(percent) }

// Volume returns the current volume percentage.
func (p *Pipeline) Volume() float64 { return p.gain.Percent() }

// SetPaused holds or resumes output. While paused a file source stalls of its
// own accord, because nothing is draining the buffer it writes into.
func (p *Pipeline) SetPaused(paused bool) { p.paused.Store(paused) }

// Paused reports whether output is held.
func (p *Pipeline) Paused() bool { return p.paused.Load() }

// SetBitrate schedules an encoder bitrate change.
//
// It is not applied here: libopus encoder state belongs to the goroutine that
// encodes, and this is called from the UI. The change is picked up before the
// next frame instead.
func (p *Pipeline) SetBitrate(bps int) { p.pendingBitrate.Store(int64(bps)) }

func (p *Pipeline) applyPendingBitrate() {
	bps := p.pendingBitrate.Swap(0)
	if bps == 0 {
		return
	}
	if err := p.enc.SetBitrate(int(bps)); err != nil {
		p.logger.Error("could not change bitrate", slog.Any("err", err))
	}
}

// Position returns how far into the current source playback has reached.
func (p *Pipeline) Position() time.Duration {
	p.mu.RLock()
	s := p.current
	p.mu.RUnlock()

	if s == nil {
		return 0
	}
	return s.offset + time.Duration(s.framesOut.Load())*audio.FrameDurationMs*time.Millisecond
}

// Active reports whether a source or a mixer is attached.
func (p *Pipeline) Active() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.current != nil || p.mixer != nil
}

// Err returns the error that ended the current source, if it failed.
func (p *Pipeline) Err() error {
	p.mu.RLock()
	s := p.current
	p.mu.RUnlock()

	if s == nil {
		return nil
	}
	if err := s.readErr.Load(); err != nil {
		return *err
	}
	return nil
}

// SetOnTrackEnd registers a callback fired once when a finite source runs out.
// It is called from the frame-provider goroutine and must not block.
func (p *Pipeline) SetOnTrackEnd(fn func()) { p.onTrackEnd.Store(&fn) }

func (p *Pipeline) fireTrackEnd(s *stream) {
	if s.live || s.endFired.Swap(true) {
		return
	}
	if fn := p.onTrackEnd.Load(); fn != nil && *fn != nil {
		go (*fn)()
	}
}

// Stats reports buffer health and output level.
func (p *Pipeline) Stats() Stats {
	rms, peak := p.meter.Levels()
	stats := Stats{RMS: rms, Peak: peak}

	p.mu.RLock()
	s, mixer := p.current, p.mixer
	p.mu.RUnlock()

	if mixer != nil {
		stats.BufferedFrames, stats.BufferCapacity, stats.Underruns = mixer.BufferStats()
	}
	if s != nil {
		stats.BufferedFrames = s.ring.Len()
		stats.BufferCapacity = s.ring.Cap()
		stats.DroppedFrames = s.ring.Dropped()
		stats.Underruns = s.ring.Underruns()
	}
	return stats
}

func closeStream(s *stream) {
	if s == nil {
		return
	}
	// Close the ring first so a blocked writer is released, then stop ffmpeg.
	s.ring.Close()
	_ = s.source.Close()
	<-s.done
}

func clampCaptureFrames(n int) int {
	if n < MinCaptureFrames {
		return MinCaptureFrames
	}
	if n > MaxCaptureFrames {
		return MaxCaptureFrames
	}
	return n
}
