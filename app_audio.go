package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/config"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/ffmpeg"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/pipeline"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/playlist"
)

// ErrNotInVoice is returned when audio is requested with nowhere to send it.
var ErrNotInVoice = errors.New("join a voice channel first")

// skipBudget is how long the queue may spend skipping past tracks that will not
// play before it gives up and goes idle.
const skipBudget = 30 * time.Second

// ------------------------------------------------------------------- player

// Play starts, or restarts, the current queue entry from the beginning.
func (a *App) Play() error {
	track, ok := a.queue.Current()
	if !ok {
		return errors.New("the queue is empty")
	}

	// Current settles on the first entry when nothing has been chosen yet, so
	// that choice has to be written down like any other.
	a.saveQueue()
	return a.playTrack(track, 0, false)
}

// SeekTo jumps to a position in the playing track.
//
// A decode pipe runs one way with no way to ask it to jump, so seeking restarts
// ffmpeg at a new offset. That is why this and Play share an implementation.
func (a *App) SeekTo(positionMs int64) error {
	if positionMs < 0 {
		positionMs = 0
	}

	a.mu.Lock()
	track := a.track
	a.mu.Unlock()

	if track == nil {
		return errors.New("no track loaded")
	}
	return a.playTrack(*track, time.Duration(positionMs)*time.Millisecond, false)
}

// playTrack decodes a queue entry into the pipeline and starts it at offset.
//
// Every route into playback goes through here — pressing play, seeking, picking
// a row, skipping, and a track ending on its own — so there is one place that
// knows what "now playing" means.
//
// auto marks the queue advancing by itself rather than the user asking for
// something, which only changes how long a link is given to resolve.
func (a *App) playTrack(track playlist.Track, offset time.Duration, auto bool) error {
	a.mu.Lock()
	pipe := a.pipe
	a.mu.Unlock()

	if pipe == nil || !a.client.State().InVoice {
		return ErrNotInVoice
	}
	if track.Path == "" {
		return errors.New("no track loaded")
	}

	// Claim this attempt before doing anything slow. Working out where a link's
	// audio lives takes seconds, which is long enough for a second seek to be
	// requested and finish first; without a generation to compare against, the
	// earlier one would then overwrite the later and playback would jump back.
	seq := a.playSeq.Add(1)

	input, err := a.resolveInput(a.ctx, track, auto)
	if err != nil {
		return err
	}

	// ffmpeg is started before the source is swapped, so the gap between the
	// old source ending and the new one producing is as short as it can be.
	src, err := input.open(a.ctx, offset)
	if err != nil {
		return err
	}

	if a.playSeq.Load() != seq {
		// Overtaken while we were working. Whatever started since is the one
		// the user asked for, so this decode is closed rather than swapped in.
		_ = src.Close()
		return nil
	}

	pipe.SetFileSource(src, offset)
	a.silenceSoundboard()

	a.mu.Lock()
	a.mode = ModePlayer
	a.track = &track
	a.mu.Unlock()

	a.startTelemetry()
	a.emitStatus()
	return nil
}

// Pause holds playback without tearing down the decode, so resuming is instant
// and does not have to re-seek.
func (a *App) Pause() error { return a.setPaused(true) }

// Resume continues paused playback.
func (a *App) Resume() error { return a.setPaused(false) }

func (a *App) setPaused(paused bool) error {
	a.mu.Lock()
	pipe := a.pipe
	a.mu.Unlock()

	if pipe == nil {
		return ErrNotInVoice
	}
	pipe.SetPaused(paused)
	a.emitStatus()
	return nil
}

// Stop ends playback and returns to the start of the track.
func (a *App) Stop() error {
	a.stopSource()
	a.emitStatus()
	return nil
}

// onTrackEnd advances the queue when a track runs out.
//
// It is called from the pipeline's frame-provider goroutine, so it must not
// block that goroutine for long: starting the next track is an ffmpeg spawn,
// which is short enough that the buffered audio covers it.
func (a *App) onTrackEnd() {
	track, ok := a.queue.Next(true)

	// A track whose file has been moved or deleted since it was queued must not
	// stall everything behind it. A failure skips on, using the manual sense of
	// next so that repeat-one cannot retry the same dead file forever, and the
	// number of attempts is bounded by the queue length so a queue of entirely
	// broken paths stops rather than spinning.
	//
	// The attempt count alone stopped being enough once a track could be a
	// link: a dead file fails in milliseconds, but a dead link fails only when
	// yt-dlp gives up, so a queue full of them would grind on in silence for
	// long enough to look like a hang. The clock bounds that.
	deadline := time.Now().Add(skipBudget)

	for attempts := a.queue.Len(); ok && attempts > 0; attempts-- {
		a.saveQueue()

		err := a.playTrack(track, 0, true)
		if err == nil {
			return
		}
		a.reportSkip(track, err)

		if time.Now().After(deadline) {
			a.emit(eventError, "Stopped after several tracks in a row would not play.")
			break
		}
		track, ok = a.queue.Next(false)
	}

	a.idle()
}

// idle drops out of playback, leaving the queue as it is.
func (a *App) idle() {
	a.mu.Lock()
	a.mode = ModeIdle
	a.mu.Unlock()

	a.emitStatus()
	a.emit(eventTelemetry, a.telemetry())
}

func (a *App) reportSkip(track playlist.Track, err error) {
	// A link that failed to play may simply have been resolved too long ago, so
	// whatever was remembered about it is dropped and the next attempt asks
	// again rather than reusing a dead address.
	a.forgetResolved(track)

	a.logger.Error("skipping a track that would not play",
		slog.String("path", track.Path), slog.Any("err", err))
	a.emit(eventError, "Skipped "+track.Name+": "+err.Error())
}

// ----------------------------------------------------------------- streamer

// ListCaptureDevices enumerates the audio devices that can be streamed.
func (a *App) ListCaptureDevices() ([]ffmpeg.CaptureDevice, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()

	devices, err := ffmpeg.ListCaptureDevices(ctx)
	if err != nil {
		// An empty device list is the hardest thing to diagnose remotely, so
		// the reason for one goes in the log rather than only in a toast.
		a.logger.Error("could not list audio devices", slog.Any("err", err))
		return nil, err
	}

	a.logger.Info("listed audio devices", slog.Int("count", len(devices)))
	for _, d := range devices {
		a.logger.Debug("audio device",
			slog.String("name", d.Name),
			slog.Bool("output", d.IsOutput),
			slog.Bool("default", d.IsDefault),
			slog.String("id", d.ID))
	}
	return devices, nil
}

// StartCapture begins streaming a capture device into the voice channel.
func (a *App) StartCapture(deviceID string) error {
	a.mu.Lock()
	pipe := a.pipe
	a.mu.Unlock()

	if pipe == nil || !a.client.State().InVoice {
		return ErrNotInVoice
	}

	settings := a.store.Settings()
	src, err := ffmpeg.OpenCapture(a.ctx, deviceID, ffmpeg.DefaultCaptureBufferMs)
	if err != nil {
		return err
	}

	pipe.SetCaptureSource(src, settings.CaptureBufferFrames)
	a.silenceSoundboard()

	a.mu.Lock()
	a.mode, a.deviceID = ModeCapture, deviceID
	a.track = nil
	a.mu.Unlock()

	_ = a.store.Update(func(s *config.Settings) { s.LastCaptureDeviceID = deviceID })

	a.startTelemetry()
	a.emitStatus()
	return nil
}

// StopCapture ends a desktop stream.
func (a *App) StopCapture() error {
	a.stopSource()
	a.emitStatus()
	return nil
}

// stopSource detaches whatever is playing and goes idle. Every audio path
// shares it, which is what keeps them mutually exclusive: starting one always
// stops the others rather than leaving several feeding the same connection.
func (a *App) stopSource() {
	a.mu.Lock()
	pipe := a.pipe
	a.mode = ModeIdle
	a.mu.Unlock()

	if pipe != nil {
		pipe.Stop()
	}
	a.silenceSoundboard()
}

// ----------------------------------------------------------------- settings

// SetVolume sets the output volume as a percentage, taking effect on the next
// frame rather than when the current buffer drains.
func (a *App) SetVolume(percent float64) error {
	if percent < 0 {
		percent = 0
	}
	if percent > audio.MaxVolumePercent {
		percent = audio.MaxVolumePercent
	}

	a.mu.Lock()
	pipe := a.pipe
	a.mu.Unlock()

	if pipe != nil {
		pipe.SetVolume(percent)
	}
	err := a.store.Update(func(s *config.Settings) { s.VolumePercent = percent })

	// The slider reads its position back out of the status, so without this the
	// knob would jump to the previous value the moment the pointer is released.
	a.emitStatus()
	return err
}

// SetBitrate sets the Opus target bitrate in kilobits per second.
func (a *App) SetBitrate(kbps int) error {
	bps := kbps * 1000
	if bps < audio.MinBitrate {
		bps = audio.MinBitrate
	}
	if bps > audio.MaxBitrate {
		bps = audio.MaxBitrate
	}

	a.mu.Lock()
	pipe := a.pipe
	a.mu.Unlock()

	if pipe != nil {
		pipe.SetBitrate(bps)
	}
	return a.store.Update(func(s *config.Settings) { s.Bitrate = bps })
}

// SetCaptureBufferFrames sets the live-capture buffer depth in 20 ms frames.
// It applies to the next stream, since resizing the buffer under a running one
// would mean either dropping its contents or a discontinuity.
func (a *App) SetCaptureBufferFrames(frames int) error {
	if frames < pipeline.MinCaptureFrames {
		frames = pipeline.MinCaptureFrames
	}
	if frames > pipeline.MaxCaptureFrames {
		frames = pipeline.MaxCaptureFrames
	}
	return a.store.Update(func(s *config.Settings) { s.CaptureBufferFrames = frames })
}

// Settings returns the saved preferences.
func (a *App) Settings() config.Settings { return a.store.Settings() }

// ---------------------------------------------------------------- telemetry

func (a *App) startTelemetry() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.telemetryStop != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.telemetryStop = cancel

	go func() {
		ticker := time.NewTicker(telemetryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.emit(eventTelemetry, a.telemetry())
			}
		}
	}()
}

func (a *App) stopTelemetry() {
	a.mu.Lock()
	stop := a.telemetryStop
	a.telemetryStop = nil
	a.mu.Unlock()

	if stop != nil {
		stop()
	}
}

func (a *App) telemetry() Telemetry {
	a.mu.Lock()
	pipe := a.pipe
	a.mu.Unlock()

	t := Telemetry{EncryptionReady: a.client.EncryptionReady()}

	if pipe != nil {
		stats := pipe.Stats()
		t.PositionMs = pipe.Position().Milliseconds()
		t.RMS, t.Peak = stats.RMS, stats.Peak
		t.BufferedFrames, t.BufferCapacity = stats.BufferedFrames, stats.BufferCapacity
		t.DroppedFrames, t.Underruns = stats.DroppedFrames, stats.Underruns

		if err := pipe.Err(); err != nil {
			a.emit(eventError, err.Error())
		}
	}

	t.Soundboard = a.mixer.Stats()

	if stats, ok := a.client.PacerStats(); ok {
		t.FramesSent, t.FramesHeld, t.Resyncs = stats.FramesSent, stats.FramesHeld, stats.Resyncs
		t.LatenessMs, t.MaxLatenessMs = stats.LatenessMs, stats.MaxLatenessMs
	}

	return t
}

func (a *App) emitStatus() { a.emit(eventStatus, a.Status()) }

func (a *App) emit(name string, data any) {
	if a.ctx == nil {
		return
	}
	wruntime.EventsEmit(a.ctx, name, data)
}
