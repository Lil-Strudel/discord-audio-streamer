// Package pacer sends Opus frames to Discord on a schedule that cannot drift.
//
// It replaces disgo's built-in audio sender, which measures time with
// millisecond-resolution wall-clock arithmetic and sleeps for the remainder of
// each frame. Sleeping for a computed remainder accumulates every scheduling
// overshoot, so the stream slowly falls behind real time. Here each frame is
// given an absolute deadline measured from a single anchor, so an overshoot on
// one frame is absorbed by the next rather than added to a running total.
package pacer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/voice"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
)

const (
	// frameDuration is the wire cadence: one Opus frame every 20 ms.
	frameDuration = audio.FrameDurationMs * time.Millisecond

	// maxLateness is how far behind schedule the loop may fall before it stops
	// trying to catch up and re-anchors. Machines suspend, and after a resume
	// the loop would otherwise try to send hours of backlog as fast as it can.
	maxLateness = 3 * frameDuration

	// trailingSilenceFrames is how many silence frames follow the last real
	// frame. Discord uses them to reset decoder interpolation, so without them
	// the tail of a track can smear into whatever is played next.
	trailingSilenceFrames = 5

	// speakingTimeout bounds a speaking-state update. It runs on the pacing
	// goroutine to keep it ordered against the audio, so it must not be allowed
	// to stall the clock for long.
	speakingTimeout = 2 * time.Second
)

// Stats is a snapshot of how the sender is keeping time, surfaced in the UI as
// stream health.
type Stats struct {
	// FramesSent counts frames written to the voice connection.
	FramesSent uint64 `json:"framesSent"`

	// FramesHeld counts frames withheld because end-to-end encryption was not
	// yet established.
	FramesHeld uint64 `json:"framesHeld"`

	// Resyncs counts how often the loop fell so far behind that it abandoned
	// its schedule and started a new one.
	Resyncs uint64 `json:"resyncs"`

	// LatenessMs is how late the most recent frame was against its deadline.
	LatenessMs float64 `json:"latenessMs"`

	// MaxLatenessMs is the worst lateness seen since the sender opened.
	MaxLatenessMs float64 `json:"maxLatenessMs"`
}

// Sender is a voice.AudioSender that paces frames against a monotonic clock.
type Sender struct {
	logger   *slog.Logger
	provider voice.OpusFrameProvider
	conn     voice.Conn

	openOnce  sync.Once
	closeOnce sync.Once
	cancel    context.CancelFunc
	done      chan struct{}

	// Speaking state is touched only by the pacing goroutine.
	speaking        bool
	silenceLeft     int
	sentSpeakingOff bool

	framesSent    atomic.Uint64
	framesHeld    atomic.Uint64
	resyncs       atomic.Uint64
	latenessMs    atomic.Uint64 // float64 bits
	maxLatenessMs atomic.Uint64 // float64 bits
}

// New creates a Sender. It satisfies voice.AudioSenderCreateFunc, so it can be
// installed with voice.WithConnAudioSenderCreateFunc.
func New(logger *slog.Logger, provider voice.OpusFrameProvider, conn voice.Conn) voice.AudioSender {
	if logger == nil {
		logger = slog.Default()
	}
	return &Sender{
		logger:   logger.With(slog.String("component", "pacer")),
		provider: provider,
		conn:     conn,
		done:     make(chan struct{}),
	}
}

// Open starts sending. Further calls have no effect.
func (s *Sender) Open() {
	s.openOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		go s.run(ctx)
	})
}

// Close stops sending and releases the frame provider.
func (s *Sender) Close() {
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
			<-s.done
		}
		if s.provider != nil {
			s.provider.Close()
		}
	})
}

// Stats returns a snapshot of the sender's timing behaviour.
func (s *Sender) Stats() Stats {
	return Stats{
		FramesSent:    s.framesSent.Load(),
		FramesHeld:    s.framesHeld.Load(),
		Resyncs:       s.resyncs.Load(),
		LatenessMs:    loadFloat(&s.latenessMs),
		MaxLatenessMs: loadFloat(&s.maxLatenessMs),
	}
}

func (s *Sender) run(ctx context.Context) {
	defer close(s.done)
	defer s.logger.Debug("pacer stopped")

	timer := time.NewTimer(frameDuration)
	defer timer.Stop()

	// anchor plus a frame count defines every deadline. Deriving deadlines from
	// a fixed origin rather than from the previous wake-up is what keeps error
	// from accumulating: a frame that wakes 3 ms late does not push the next
	// one 3 ms late as well.
	anchor := time.Now()
	var count int64

	for {
		count++
		deadline := anchor.Add(time.Duration(count) * frameDuration)

		if delay := time.Until(deadline); delay > 0 {
			timer.Reset(delay)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		} else {
			// Already past the deadline. Yield so a starved scheduler cannot
			// let this loop spin without ever observing cancellation.
			select {
			case <-ctx.Done():
				return
			default:
			}
		}

		lateness := time.Since(deadline)
		s.recordLateness(lateness)

		if lateness > maxLateness {
			// Too far behind to catch up. Sending the backlog as fast as
			// possible would flood the channel, so the schedule is abandoned
			// and restarted from now. This is what a suspended laptop looks
			// like from in here.
			s.resyncs.Add(1)
			s.logger.Debug("re-anchoring clock", slog.Duration("behind", lateness))
			anchor, count = time.Now(), 0
		}

		s.sendFrame()
	}
}

func (s *Sender) sendFrame() {
	if s.provider == nil {
		return
	}

	// Hold frames until end-to-end encryption is established. During the MLS
	// handshake, after joining or moving channel, the DAVE session encrypts by
	// passing frames through unchanged, so sending now would put audio on the
	// wire in the clear.
	if dave := s.conn.DAVE(); dave != nil && !dave.Ready() {
		s.framesHeld.Add(1)
		return
	}

	frame, err := s.provider.ProvideOpusFrame()
	if err != nil && !errors.Is(err, io.EOF) {
		s.logger.Error("could not read an opus frame", slog.Any("err", err))
		return
	}

	if len(frame) == 0 {
		s.sendTrailingSilence()
		return
	}

	if !s.speaking {
		s.setSpeaking(voice.SpeakingFlagMicrophone)
		s.speaking = true
		s.sentSpeakingOff = false
		s.silenceLeft = trailingSilenceFrames
	}

	if _, err := s.conn.UDP().Write(frame); err != nil {
		s.handleWriteErr(err)
		return
	}
	s.framesSent.Add(1)
}

// sendTrailingSilence runs when the source has nothing to give: playback is
// paused, stopped, or finished.
func (s *Sender) sendTrailingSilence() {
	if s.silenceLeft > 0 {
		if _, err := s.conn.UDP().Write(voice.SilenceAudioFrame); err != nil {
			s.handleWriteErr(err)
			return
		}
		s.silenceLeft--
		return
	}

	if !s.sentSpeakingOff {
		s.setSpeaking(voice.SpeakingFlagNone)
		s.sentSpeakingOff = true
		s.speaking = false
	}
}

func (s *Sender) setSpeaking(flags voice.SpeakingFlags) {
	ctx, cancel := context.WithTimeout(context.Background(), speakingTimeout)
	defer cancel()

	if err := s.conn.SetSpeaking(ctx, flags); err != nil {
		s.handleWriteErr(err)
	}
}

func (s *Sender) handleWriteErr(err error) {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, voice.ErrGatewayNotConnected) {
		// The connection is gone; there is nothing to recover and every
		// subsequent frame would log the same thing fifty times a second.
		s.logger.Debug("voice connection closed, stopping", slog.Any("err", err))
		go s.Close()
		return
	}
	s.logger.Error("could not send audio", slog.Any("err", err))
}

func (s *Sender) recordLateness(d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	if ms < 0 {
		ms = 0
	}
	storeFloat(&s.latenessMs, ms)

	for {
		current := loadFloat(&s.maxLatenessMs)
		if ms <= current {
			return
		}
		if s.maxLatenessMs.CompareAndSwap(floatBits(current), floatBits(ms)) {
			return
		}
	}
}
