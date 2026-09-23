// Package mixer plays several sounds at once and sums them into one stream.
//
// It backs the soundboard: a small, fixed number of channels, each playing at
// most one file at a time with its own volume, pause and loop. The pipeline
// pulls one mixed frame at a time from [Mixer.Mix] on the pacer goroutine, so
// mixing happens at the last moment and a volume change or a new sound is heard
// on the very next frame instead of after a buffer drains.
//
// Every channel decodes on its own goroutine into its own ring buffer, exactly
// as the pipeline does for a single file, because ffmpeg decodes at an
// unrelated rate to the 20 ms wire cadence.
package mixer

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/ffmpeg"
)

// Channels is how many sounds can play at once.
const Channels = 4

// voiceFrames is each channel's buffer depth in 20 ms frames. It is deeper than
// the pipeline's file buffer because it also has to cover the restart of
// ffmpeg when a looping sound reaches its end: half a second is far longer than
// a spawn takes, so the loop is heard without a gap.
const voiceFrames = 25

// ErrNoChannel is returned for a channel index outside 0 to Channels-1.
var ErrNoChannel = errors.New("no such soundboard track")

// Opener starts decoding a file from its beginning.
type Opener func(path string) (ffmpeg.Source, error)

// ChannelState is what one channel is doing, for the UI.
type ChannelState struct {
	// Path is the file loaded on the channel, empty when it is idle.
	Path   string `json:"path"`
	Name   string `json:"name"`
	Paused bool   `json:"paused"`
	Loop   bool   `json:"loop"`
}

// ChannelStats are the fast-moving numbers behind a channel's meter.
type ChannelStats struct {
	RMS        float64 `json:"rms"`
	Peak       float64 `json:"peak"`
	PositionMs int64   `json:"positionMs"`
}

// Mixer sums up to [Channels] sounds into one stream of PCM frames.
type Mixer struct {
	logger *slog.Logger
	open   Opener

	channels [Channels]*channel

	// acc, sum and scratch are only touched from the goroutine calling Mix.
	acc     []int32
	sum     []int16
	scratch []int16

	underruns atomic.Uint64
	onEnd     atomic.Pointer[func(int)]
}

// New returns a Mixer with every channel idle at full volume.
func New(logger *slog.Logger, open Opener) *Mixer {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Mixer{
		logger:  logger.With(slog.String("component", "mixer")),
		open:    open,
		acc:     make([]int32, audio.SamplesPerFrame),
		sum:     make([]int16, audio.SamplesPerFrame),
		scratch: make([]int16, audio.SamplesPerFrame),
	}
	for i := range m.channels {
		m.channels[i] = &channel{
			gain:  audio.NewGain(100),
			meter: audio.NewMeter(),
		}
	}
	return m
}

// channel is one track of the soundboard.
type channel struct {
	mu sync.Mutex

	// current is what is playing. outgoing is what it replaced, kept for one
	// more frame so it can be faded out instead of cut off mid-waveform, which
	// would click.
	current  *voice
	outgoing *voice

	// gain and meter are applied on the mixing goroutine only; gain guards its
	// own target and the meter is atomic.
	gain  *audio.Gain
	meter *audio.Meter

	paused atomic.Bool
	loop   atomic.Bool
}

// voice is one file being decoded into a channel's buffer.
type voice struct {
	path string
	ring *audio.Ring

	// source is replaced each time a looping sound starts over, so it is
	// guarded for the closer, which may run at any moment.
	mu     sync.Mutex
	source ffmpeg.Source
	closed atomic.Bool

	sourceDone atomic.Bool
	framesOut  atomic.Uint64

	// loopFrames is the length of one pass through the file, learned when the
	// first pass ends. Position wraps on it, so a looping sound's clock goes
	// back to zero each time round instead of counting up forever.
	loopFrames atomic.Uint64

	done chan struct{}
}

// SetOnEnd registers a callback fired, on its own goroutine, when a channel's
// sound finishes by itself.
func (m *Mixer) SetOnEnd(fn func(channel int)) { m.onEnd.Store(&fn) }

// Play starts path on a channel, replacing whatever it was playing. A paused
// channel is resumed: tapping a sound is asking to hear it.
func (m *Mixer) Play(ch int, path string) error {
	c, err := m.channel(ch)
	if err != nil {
		return err
	}

	// ffmpeg is started before the swap, so the old sound keeps playing for as
	// long as the spawn takes rather than leaving a hole.
	src, err := m.open(path)
	if err != nil {
		return err
	}

	v := &voice{
		path:   path,
		ring:   audio.NewRing(voiceFrames, false),
		source: src,
		done:   make(chan struct{}),
	}
	go m.produce(c, v)

	c.mu.Lock()
	stale := c.retire(c.current)
	c.current = v
	c.paused.Store(false)
	c.mu.Unlock()

	if stale != nil {
		go closeVoice(stale)
	}
	return nil
}

// Stop fades a channel out and leaves it idle.
func (m *Mixer) Stop(ch int) error {
	c, err := m.channel(ch)
	if err != nil {
		return err
	}

	c.mu.Lock()
	stale := c.retire(c.current)
	c.current = nil
	c.paused.Store(false)
	c.mu.Unlock()

	if stale != nil {
		go closeVoice(stale)
	}
	return nil
}

// retire makes v the voice being faded out. If an earlier one was still
// waiting for its fade, that one is returned to be closed at once: two
// replacements inside one frame leave no time to fade both. The caller holds
// c.mu.
func (c *channel) retire(v *voice) (stale *voice) {
	if v == nil {
		return nil
	}
	stale = c.outgoing
	c.outgoing = v
	return stale
}

// StopAll silences every channel at once, without a fade. It is for leaving
// the soundboard, when the pipeline is about to stop pulling from it.
func (m *Mixer) StopAll() {
	var stale []*voice
	for _, c := range m.channels {
		c.mu.Lock()
		stale = append(stale, c.current, c.outgoing)
		c.current, c.outgoing = nil, nil
		c.paused.Store(false)
		c.mu.Unlock()
		c.meter.Reset()
	}
	for _, v := range stale {
		closeVoice(v)
	}
	m.underruns.Store(0)
}

// SetPaused holds or resumes one channel. A held channel is simply not read,
// so its decoder stalls against the full buffer of its own accord.
func (m *Mixer) SetPaused(ch int, paused bool) error {
	c, err := m.channel(ch)
	if err != nil {
		return err
	}
	c.paused.Store(paused)
	return nil
}

// SetVolume sets one channel's volume as a percentage.
func (m *Mixer) SetVolume(ch int, percent float64) error {
	c, err := m.channel(ch)
	if err != nil {
		return err
	}
	c.gain.SetPercent(percent)
	return nil
}

// SetLoop sets whether a channel repeats its sound. It applies to what is
// already playing, from the next time that sound reaches its end.
func (m *Mixer) SetLoop(ch int, on bool) error {
	c, err := m.channel(ch)
	if err != nil {
		return err
	}
	c.loop.Store(on)
	return nil
}

// Snapshot reports what every channel is doing.
func (m *Mixer) Snapshot() []ChannelState {
	out := make([]ChannelState, len(m.channels))
	for i, c := range m.channels {
		c.mu.Lock()
		v := c.current
		c.mu.Unlock()

		out[i] = ChannelState{Paused: c.paused.Load(), Loop: c.loop.Load()}
		if v != nil {
			out[i].Path = v.path
			out[i].Name = filepath.Base(v.path)
		}
	}
	return out
}

// Stats reports every channel's level and position.
func (m *Mixer) Stats() []ChannelStats {
	out := make([]ChannelStats, len(m.channels))
	for i, c := range m.channels {
		c.mu.Lock()
		v := c.current
		c.mu.Unlock()

		out[i].RMS, out[i].Peak = c.meter.Levels()
		if v != nil {
			out[i].PositionMs = v.position().Milliseconds()
		}
	}
	return out
}

// BufferStats sums buffer health across the playing channels, for the
// stream-health panel. Underruns count since the soundboard was last stopped.
func (m *Mixer) BufferStats() (buffered, capacity int, underruns uint64) {
	for _, c := range m.channels {
		c.mu.Lock()
		v := c.current
		c.mu.Unlock()

		if v != nil {
			buffered += v.ring.Len()
			capacity += v.ring.Cap()
		}
	}
	return buffered, capacity, m.underruns.Load()
}

// Mix fills dst with the sum of every channel and reports whether any channel
// is playing. When none is, dst is left alone and the caller should send
// nothing, so Discord stops showing the bot as speaking.
//
// It is called from the pacer goroutine once per frame and never blocks on a
// decoder.
func (m *Mixer) Mix(dst []int16) bool {
	clear(m.acc)
	active := false

	for i, c := range m.channels {
		if m.mixChannel(i, c) {
			active = true
			for j, s := range m.sum {
				m.acc[j] += int32(s)
			}
		}
	}

	if !active {
		return false
	}
	for j, s := range m.acc {
		dst[j] = saturate(s)
	}
	return true
}

// mixChannel renders one channel's frame into m.sum, after its volume, and
// reports whether it contributed anything.
func (m *Mixer) mixChannel(i int, c *channel) bool {
	c.mu.Lock()
	out := c.outgoing
	c.outgoing = nil
	cur := c.current
	paused := c.paused.Load()

	var ended *voice
	if cur != nil && !paused && cur.finished() {
		ended, cur = cur, nil
		c.current = nil
	}
	c.mu.Unlock()

	if ended != nil {
		go closeVoice(ended)
		if fn := m.onEnd.Load(); fn != nil && *fn != nil {
			go (*fn)(i)
		}
	}

	clear(m.sum)
	active := false

	// The replaced sound gets one last frame, ramped from full to nothing. At
	// 20 ms that is too short to hear as a fade, but long enough that the
	// waveform does not jump, which is what a click is.
	if out != nil {
		if out.ring.Read(m.scratch) {
			fade := audio.NewGain(100)
			fade.SetPercent(0)
			fade.Apply(m.scratch)
			copy(m.sum, m.scratch)
			active = true
		}
		go closeVoice(out)
	}

	if cur != nil && !paused {
		// A sound whose decoder has not caught up yet still counts as playing:
		// the gap is silence within the stream, not the end of it.
		active = true
		if cur.ring.Read(m.scratch) {
			cur.framesOut.Add(1)
			for j, s := range m.scratch {
				m.sum[j] = saturate(int32(m.sum[j]) + int32(s))
			}
		} else if !cur.ring.Closed() {
			m.underruns.Add(1)
		}
	}

	if !active {
		c.meter.Reset()
		return false
	}

	c.gain.Apply(m.sum)
	c.meter.Observe(m.sum)
	return true
}

// produce decodes a voice into its buffer, starting the file over each time it
// ends while the channel is set to loop.
func (m *Mixer) produce(c *channel, v *voice) {
	defer close(v.done)
	// Closing the ring when the decode is over keeps what is buffered readable
	// but lets the mixer tell a finished sound from a starved one.
	defer v.ring.Close()
	defer v.sourceDone.Store(true)

	src := v.currentSource()
	for {
		frames, err := v.fill(src)
		if err != nil {
			if !v.closed.Load() {
				m.logger.Error("sound read failed", slog.String("path", v.path), slog.Any("err", err))
			}
			return
		}
		if v.loopFrames.Load() == 0 {
			v.loopFrames.Store(frames)
		}

		// An empty file would otherwise be reopened as fast as ffmpeg can
		// start, forever.
		if frames == 0 || !c.loop.Load() || v.closed.Load() {
			return
		}

		_ = src.Close()
		next, err := m.open(v.path)
		if err != nil {
			m.logger.Error("could not restart a looping sound", slog.String("path", v.path), slog.Any("err", err))
			return
		}
		if !v.swapSource(next) {
			return
		}
		src = next
	}
}

// fill copies one pass of src into the ring and reports how many frames it
// held. The error is nil at the end of the file.
func (v *voice) fill(src io.Reader) (uint64, error) {
	framer := audio.NewFramer(src)
	var frames uint64
	for {
		frame, err := framer.ReadFrame()
		if errors.Is(err, io.EOF) {
			return frames, nil
		}
		if err != nil {
			return frames, err
		}
		if !v.ring.Write(frame) {
			return frames, errClosed
		}
		frames++
	}
}

var errClosed = errors.New("voice closed")

func (v *voice) currentSource() ffmpeg.Source {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.source
}

// swapSource installs the decoder for the next pass of a loop. It reports false,
// and closes next, if the voice was closed while that decoder was starting.
func (v *voice) swapSource(next ffmpeg.Source) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed.Load() {
		_ = next.Close()
		return false
	}
	v.source = next
	return true
}

// finished reports that the file has been decoded and every frame of it played.
func (v *voice) finished() bool {
	return v.sourceDone.Load() && v.ring.Len() == 0
}

func (v *voice) position() time.Duration {
	frames := v.framesOut.Load()
	if n := v.loopFrames.Load(); n > 0 {
		frames %= n
	}
	return time.Duration(frames) * audio.FrameDurationMs * time.Millisecond
}

func closeVoice(v *voice) {
	if v == nil || v.closed.Swap(true) {
		return
	}
	// Close the ring first so a blocked writer is released, then stop ffmpeg.
	v.ring.Close()
	v.mu.Lock()
	src := v.source
	v.mu.Unlock()
	_ = src.Close()
	<-v.done
}

func (m *Mixer) channel(i int) (*channel, error) {
	if i < 0 || i >= len(m.channels) {
		return nil, fmt.Errorf("%w: %d", ErrNoChannel, i+1)
	}
	return m.channels[i], nil
}

// saturate clips a summed sample to int16. Several loud sounds together will
// exceed full scale, and clipping is far less objectionable than letting the
// sum wrap around into a full-scale sign flip.
func saturate(v int32) int16 {
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < math.MinInt16 {
		return math.MinInt16
	}
	return int16(v)
}
