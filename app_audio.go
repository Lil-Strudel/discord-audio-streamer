package main

import (
	"context"
	"errors"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/config"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/ffmpeg"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/pipeline"
)

// ErrNotInVoice is returned when audio is requested with nowhere to send it.
var ErrNotInVoice = errors.New("join a voice channel first")

// ------------------------------------------------------------------- player

// PickAudioFile opens a native file chooser and returns the chosen path.
func (a *App) PickAudioFile() (string, error) {
	return wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Choose an audio file",
		Filters: []wruntime.FileFilter{
			{
				DisplayName: "Audio files",
				// ffmpeg decodes far more than this, but a filter listing every
				// container it understands is not a usable dialog. Users can
				// still pick anything through "All files".
				Pattern: "*.mp3;*.wav;*.flac;*.ogg;*.opus;*.m4a;*.aac;*.wma;*.aiff;*.alac;*.mp4;*.webm;*.mkv",
			},
			{DisplayName: "All files", Pattern: "*.*"},
		},
	})
}

// LoadTrack reads a file's metadata and makes it the current track. It does not
// start playback, so the user can see what they picked before anyone hears it.
func (a *App) LoadTrack(path string) (TrackInfo, error) {
	if path == "" {
		return TrackInfo{}, errors.New("no file selected")
	}

	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()

	meta, err := ffmpeg.Probe(ctx, path)
	if err != nil {
		return TrackInfo{}, err
	}

	track := TrackInfo{
		Path:       meta.Path,
		Name:       meta.DisplayName(),
		Codec:      meta.Codec,
		DurationMs: meta.Duration.Milliseconds(),
	}

	a.stopSource()

	a.mu.Lock()
	a.track, a.trackPath, a.mode = &track, path, ModePlayer
	a.mu.Unlock()

	a.emitStatus()
	return track, nil
}

// Play starts, or restarts, the loaded track from the beginning.
func (a *App) Play() error { return a.playFrom(0) }

// SeekTo jumps to a position in the loaded track.
//
// A decode pipe runs one way with no way to ask it to jump, so seeking restarts
// ffmpeg at a new offset. That is why this and Play share an implementation.
func (a *App) SeekTo(positionMs int64) error {
	if positionMs < 0 {
		positionMs = 0
	}
	return a.playFrom(time.Duration(positionMs) * time.Millisecond)
}

func (a *App) playFrom(offset time.Duration) error {
	a.mu.Lock()
	pipe, path := a.pipe, a.trackPath
	a.mu.Unlock()

	if pipe == nil || !a.client.State().InVoice {
		return ErrNotInVoice
	}
	if path == "" {
		return errors.New("no track loaded")
	}

	src, err := ffmpeg.OpenFile(a.ctx, path, offset)
	if err != nil {
		return err
	}

	pipe.SetFileSource(src, offset)

	a.mu.Lock()
	a.mode = ModePlayer
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

func (a *App) onTrackEnd() {
	a.mu.Lock()
	a.mode = ModeIdle
	a.mu.Unlock()

	a.emitStatus()
	a.emit(eventTelemetry, a.telemetry())
}

// ----------------------------------------------------------------- streamer

// ListCaptureDevices enumerates audio inputs that can be streamed.
func (a *App) ListCaptureDevices() ([]ffmpeg.CaptureDevice, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	return ffmpeg.ListCaptureDevices(ctx)
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

	a.mu.Lock()
	a.mode, a.deviceID = ModeCapture, deviceID
	a.track, a.trackPath = nil, ""
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

// stopSource detaches whatever is playing and goes idle. Both audio paths share
// it, which is what keeps them mutually exclusive: starting one always stops
// the other rather than leaving both feeding the same connection.
func (a *App) stopSource() {
	a.mu.Lock()
	pipe := a.pipe
	a.mode = ModeIdle
	a.mu.Unlock()

	if pipe != nil {
		pipe.Stop()
	}
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
	return a.store.Update(func(s *config.Settings) { s.VolumePercent = percent })
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
