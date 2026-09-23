package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/config"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/ffmpeg"
)

// maxTrackName bounds a soundboard track's name, which is a label, not a note.
const maxTrackName = 40

// SoundboardTrack is one soundboard track as the UI draws it: its saved setup
// and what it is playing right now.
type SoundboardTrack struct {
	Name          string  `json:"name"`
	Folder        string  `json:"folder"`
	VolumePercent float64 `json:"volumePercent"`
	Loop          bool    `json:"loop"`
	View          string  `json:"view"`

	// Path is the file playing on the track, empty when it is idle.
	Path   string `json:"path"`
	Paused bool   `json:"paused"`
}

// SoundboardState is the whole soundboard.
type SoundboardState struct {
	Tracks []SoundboardTrack `json:"tracks"`
}

// SoundEntry is one item in a soundboard folder.
type SoundEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

// ------------------------------------------------------------------- reading

// Soundboard returns every track's setup and what it is playing. The frontend
// calls this once at startup; after that it follows the soundboard event.
func (a *App) Soundboard() SoundboardState {
	saved := a.store.Settings().Soundboard.Tracks
	live := a.mixer.Snapshot()

	state := SoundboardState{Tracks: make([]SoundboardTrack, len(saved))}
	for i, t := range saved {
		state.Tracks[i] = SoundboardTrack{
			Name:          t.Name,
			Folder:        t.Folder,
			VolumePercent: t.VolumePercent,
			Loop:          t.Loop,
			View:          t.View,
		}
		if i < len(live) {
			state.Tracks[i].Path = live[i].Path
			state.Tracks[i].Paused = live[i].Paused
		}
	}
	return state
}

// ListSoundFolder lists one folder's subfolders and audio files, not their
// contents. The soundboard browses a folder a level at a time, so a large
// sound library costs only what is actually opened.
func (a *App) ListSoundFolder(dir string) ([]SoundEntry, error) {
	if dir == "" {
		return nil, errors.New("no folder selected")
	}

	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(dir), err)
	}

	entries := make([]SoundEntry, 0, len(items))
	for _, item := range items {
		name := item.Name()
		// Hidden files are the operating system's and the editor's, not sounds.
		if strings.HasPrefix(name, ".") {
			continue
		}

		path := filepath.Join(dir, name)
		isDir := item.IsDir()
		if item.Type()&os.ModeSymlink != 0 {
			// A linked folder is still a folder to the person who made the link.
			if info, err := os.Stat(path); err == nil {
				isDir = info.IsDir()
			}
		}
		if !isDir && !ffmpeg.IsAudioFile(name) {
			continue
		}
		entries = append(entries, SoundEntry{Name: name, Path: path, IsDir: isDir})
	}

	// Folders first, as every file manager does, then by name ignoring case.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

// ------------------------------------------------------------------- playing

// PlaySound plays a file on one soundboard track, replacing whatever that track
// was playing. The first sound takes the voice connection over from the
// playlist or a desktop stream.
func (a *App) PlaySound(track int, path string) error {
	if err := checkTrack(track); err != nil {
		return err
	}
	if path == "" {
		return errors.New("no sound selected")
	}

	a.mu.Lock()
	pipe, mode := a.pipe, a.mode
	a.mu.Unlock()

	if pipe == nil || !a.client.State().InVoice {
		return ErrNotInVoice
	}

	if mode != ModeSoundboard {
		// A playlist link still resolving would otherwise finish afterwards
		// and take the connection back from the soundboard.
		a.playSeq.Add(1)
		pipe.SetMixer(a.mixer)

		a.mu.Lock()
		a.mode = ModeSoundboard
		a.track = nil
		a.mu.Unlock()
	}

	err := a.mixer.Play(track, path)
	if err != nil {
		a.logger.Error("could not play a sound", slog.String("path", path), slog.Any("err", err))
	}

	a.startTelemetry()
	a.emitStatus()
	a.emitSoundboard()
	return err
}

// PauseSound holds one track where it is.
func (a *App) PauseSound(track int) error { return a.setSoundPaused(track, true) }

// ResumeSound continues a held track.
func (a *App) ResumeSound(track int) error { return a.setSoundPaused(track, false) }

func (a *App) setSoundPaused(track int, paused bool) error {
	if err := a.mixer.SetPaused(track, paused); err != nil {
		return err
	}
	a.emitSoundboard()
	return nil
}

// StopSound silences one track.
func (a *App) StopSound(track int) error {
	if err := a.mixer.Stop(track); err != nil {
		return err
	}
	a.emitSoundboard()
	return nil
}

// StopAllSounds silences every track at once.
func (a *App) StopAllSounds() error {
	a.mixer.StopAll()
	a.emitSoundboard()
	return nil
}

// silenceSoundboard stops every soundboard track, for when another audio path
// takes the connection or it goes away. The pipeline has already stopped
// pulling from the mixer by then; this ends the decoders behind it.
func (a *App) silenceSoundboard() {
	for _, ch := range a.mixer.Snapshot() {
		if ch.Path != "" {
			a.mixer.StopAll()
			a.emitSoundboard()
			return
		}
	}
}

// ------------------------------------------------------------------- setting

// ChooseSoundFolder asks for a track's folder. Whatever the track is playing
// carries on: changing where the next sound comes from is not a reason to cut
// off the current one.
func (a *App) ChooseSoundFolder(track int) error {
	if err := checkTrack(track); err != nil {
		return err
	}

	dir, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Choose a folder of sounds",
	})
	if err != nil || dir == "" {
		return err
	}
	return a.updateSoundTrack(track, func(t *config.SoundboardTrack) { t.Folder = dir })
}

// SetSoundVolume sets one track's volume as a percentage.
func (a *App) SetSoundVolume(track int, percent float64) error {
	percent = max(0, min(audio.MaxVolumePercent, percent))
	if err := a.mixer.SetVolume(track, percent); err != nil {
		return err
	}
	return a.updateSoundTrack(track, func(t *config.SoundboardTrack) { t.VolumePercent = percent })
}

// SetSoundLoop sets whether a track repeats what it plays.
func (a *App) SetSoundLoop(track int, on bool) error {
	if err := a.mixer.SetLoop(track, on); err != nil {
		return err
	}
	return a.updateSoundTrack(track, func(t *config.SoundboardTrack) { t.Loop = on })
}

// SetSoundView sets how a track shows its folder: "grid" or "list".
func (a *App) SetSoundView(track int, view string) error {
	if view != config.ViewGrid && view != config.ViewList {
		return fmt.Errorf("unknown view %q", view)
	}
	return a.updateSoundTrack(track, func(t *config.SoundboardTrack) { t.View = view })
}

// RenameSoundTrack sets a track's label. An empty name restores the default.
func (a *App) RenameSoundTrack(track int, name string) error {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > maxTrackName {
		name = string(r[:maxTrackName])
	}
	return a.updateSoundTrack(track, func(t *config.SoundboardTrack) { t.Name = name })
}

// ------------------------------------------------------------------ internal

// updateSoundTrack changes one track's saved setup and tells the UI.
func (a *App) updateSoundTrack(track int, fn func(*config.SoundboardTrack)) error {
	if err := checkTrack(track); err != nil {
		return err
	}
	err := a.store.Update(func(s *config.Settings) { fn(&s.Soundboard.Tracks[track]) })

	// Sent even if saving failed: the change has taken effect, and the UI
	// reads its sliders and toggles back out of this.
	a.emitSoundboard()
	return err
}

// applySoundboardSettings gives the mixer the saved volumes and loop settings.
func (a *App) applySoundboardSettings() {
	for i, t := range a.store.Settings().Soundboard.Tracks {
		_ = a.mixer.SetVolume(i, t.VolumePercent)
		_ = a.mixer.SetLoop(i, t.Loop)
	}
}

func (a *App) emitSoundboard() {
	if a.store == nil {
		return
	}
	a.emit(eventSoundboard, a.Soundboard())
}

func checkTrack(track int) error {
	if track < 0 || track >= config.SoundboardTracks {
		return fmt.Errorf("there is no soundboard track %d", track+1)
	}
	return nil
}
