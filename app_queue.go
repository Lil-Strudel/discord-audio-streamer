package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/config"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/ffmpeg"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/playlist"
)

// maxImportTracks bounds what a single import may add, whether that is a folder
// or a playlist link. Pointing the app at a whole music library, or at someone's
// thousand-video playlist, should stop at something the UI can still render
// rather than at the point the machine runs out of memory.
const maxImportTracks = 2000

// ------------------------------------------------------------------- reading

// Queue returns the whole playlist. The frontend calls this once at startup;
// after that it follows the queue event instead.
func (a *App) Queue() playlist.State { return a.queue.Snapshot() }

// ------------------------------------------------------------------- picking

// PickAudioFiles opens a native file chooser that accepts several files.
func (a *App) PickAudioFiles() ([]string, error) {
	return wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Choose audio files",
		Filters: []wruntime.FileFilter{
			// ffmpeg decodes far more than this, but a filter listing every
			// container it understands is not a usable dialog. Users can still
			// pick anything through "All files".
			{DisplayName: "Audio files", Pattern: ffmpeg.AudioFilePattern()},
			{DisplayName: "All files", Pattern: "*.*"},
		},
	})
}

// PickFolder opens a native directory chooser.
func (a *App) PickFolder() (string, error) {
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Choose a folder of audio",
	})
}

// ------------------------------------------------------------------- editing

// AddFiles appends paths to the queue.
//
// The entries appear immediately, named after their files and with no duration,
// and the tags are filled in afterwards. Reading a file's header costs an
// ffmpeg process each (see ffmpeg.Probe), so probing a few hundred of them
// before returning would leave the window unresponsive for most of a minute.
//
// A directory among the paths is expanded, which is what makes this the single
// entry point for the file dialog, the folder button and a drag-and-drop alike.
func (a *App) AddFiles(paths []string) error {
	var (
		accepted []playlist.Track
		skipped  int
	)

	for _, path := range paths {
		if path == "" {
			continue
		}

		if info, err := os.Stat(path); err == nil && info.IsDir() {
			found, err := ffmpeg.FindAudioFiles(path, maxImportTracks)
			if err != nil {
				return err
			}
			for _, p := range found {
				accepted = append(accepted, newTrack(p))
			}
			continue
		}

		if !ffmpeg.IsAudioFile(path) {
			skipped++
			continue
		}
		accepted = append(accepted, newTrack(path))
	}

	if len(accepted) == 0 {
		if skipped > 0 {
			return fmt.Errorf("nothing added: %s", describeSkipped(skipped))
		}
		return nil
	}

	a.queue.Add(accepted...)
	a.saveQueue()
	go a.probeTracks(accepted)

	if skipped > 0 {
		a.emit(eventError, "Added "+plural(len(accepted), "track")+"; "+describeSkipped(skipped))
	}
	return nil
}

// AddFolder appends every audio file under dir, including subfolders.
func (a *App) AddFolder(dir string) error {
	if dir == "" {
		return errors.New("no folder selected")
	}

	paths, err := ffmpeg.FindAudioFiles(dir, maxImportTracks)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no audio files found in %s", filepath.Base(dir))
	}

	tracks := make([]playlist.Track, 0, len(paths))
	for _, path := range paths {
		tracks = append(tracks, newTrack(path))
	}

	a.queue.Add(tracks...)
	a.saveQueue()
	go a.probeTracks(tracks)
	return nil
}

// RemoveTrack drops one entry from the queue.
func (a *App) RemoveTrack(id string) error {
	// Removing whatever is playing should also silence it; leaving it running
	// after its row has gone from the list would be indefensible.
	if current, ok := a.queue.Current(); ok && current.ID == id && a.playing() {
		a.stopSource()
	}

	a.queue.Remove(id)
	a.saveQueue()
	a.emitStatus()
	return nil
}

// MoveTrack repositions an entry, for drag-and-drop reordering.
func (a *App) MoveTrack(id string, to int) error {
	a.queue.Move(id, to)
	a.saveQueue()
	return nil
}

// ClearQueue empties the playlist and stops playback.
func (a *App) ClearQueue() error {
	a.stopSource()
	a.queue.Clear()

	a.mu.Lock()
	a.track = nil
	a.mu.Unlock()

	a.saveQueue()
	a.emitStatus()
	return nil
}

// ---------------------------------------------------------------- navigation

// PlayTrack starts one entry of the queue immediately.
func (a *App) PlayTrack(id string) error {
	track, ok := a.queue.Select(id)
	if !ok {
		return errors.New("that track is no longer in the queue")
	}

	a.saveQueue()
	return a.playTrack(track, 0, false)
}

// NextTrack skips forward. It keeps playing if something already was, and
// otherwise only moves the selection, so a user browsing a paused queue does
// not have audio start under them.
func (a *App) NextTrack() error { return a.step(a.queue.Next(false)) }

// PreviousTrack skips backward.
//
// Part-way into a track the expected behaviour is to restart it rather than to
// leave, which is what every media player does and what makes a double press
// mean "the one before".
func (a *App) PreviousTrack() error {
	a.mu.Lock()
	pipe := a.pipe
	a.mu.Unlock()

	const restartWithin = 3 * time.Second
	if pipe != nil && a.playing() && pipe.Active() && pipe.Position() > restartWithin {
		return a.SeekTo(0)
	}
	return a.step(a.queue.Prev())
}

// step applies the result of a queue move.
func (a *App) step(track playlist.Track, ok bool) error {
	if !ok {
		return nil
	}
	a.saveQueue()

	// Only follow the move with audio if audio was already flowing.
	a.mu.Lock()
	wasPlaying := a.mode == ModePlayer
	a.track = &track
	a.mu.Unlock()

	if !wasPlaying {
		a.emitStatus()
		return nil
	}
	return a.playTrack(track, 0, false)
}

// ------------------------------------------------------------------- options

// SetShuffle turns shuffled playback on or off.
func (a *App) SetShuffle(on bool) error {
	a.queue.SetShuffle(on)
	a.saveQueue()
	return nil
}

// SetRepeat sets the repeat mode: "off", "all" or "one".
func (a *App) SetRepeat(mode string) error {
	repeat := playlist.Repeat(mode)
	if !repeat.Valid() {
		return fmt.Errorf("unknown repeat mode %q", mode)
	}

	a.queue.SetRepeat(repeat)
	a.saveQueue()
	return nil
}

// ------------------------------------------------------------------ internal

// newTrack makes a queue entry for a file that has not been read yet. The
// basename stands in until the header has been probed, so a long import still
// shows a usable list from the first moment.
func newTrack(path string) playlist.Track {
	return playlist.Track{
		ID:     playlist.NewID(),
		Path:   path,
		Source: playlist.SourceFile,
		Name:   filepath.Base(path),
	}
}

// probeTracks reads tags and durations in the background.
//
// One goroutine walking the list in order is deliberate: the entries fill in
// from the top, which reads as progress, and a folder import cannot spawn
// hundreds of ffmpeg processes at once.
func (a *App) probeTracks(tracks []playlist.Track) {
	var changed bool

	// Pushing the whole queue after every file would be one webview round trip
	// per track. Batching them keeps a large import to a few updates a second,
	// which still looks like it is filling in live.
	flush := time.NewTicker(250 * time.Millisecond)
	defer flush.Stop()

	for _, track := range tracks {
		// Only a local file has a header to read. A link's metadata came from
		// the listing, and handing its URL to ffmpeg here would be a needless
		// failure at best and a download at worst.
		if track.Source != playlist.SourceFile {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		meta, err := ffmpeg.Probe(ctx, track.Path)
		cancel()

		if err != nil {
			// A file that will not probe may still play, and it may simply be
			// a format ffmpeg reports oddly. It keeps its basename and stays in
			// the queue rather than vanishing without explanation.
			a.logger.Warn("could not read track metadata",
				slog.String("path", track.Path), slog.Any("err", err))
			continue
		}

		if a.queue.SetMetadata(track.ID, meta.DisplayName(), meta.Codec, meta.Duration.Milliseconds()) {
			changed = true
		}

		select {
		case <-flush.C:
			a.emitQueue()
			changed = false
		default:
		}
	}

	if changed {
		a.emitQueue()
	}

	// The names and durations just learned are worth keeping for the next run.
	a.persistQueue()
}

// saveQueue writes the queue out and tells the UI, which every mutation owes.
// They are one call so that no edit can do half of it.
func (a *App) saveQueue() {
	a.persistQueue()
	a.emitQueue()
}

func (a *App) persistQueue() {
	if a.store == nil {
		return
	}
	if err := a.store.Update(func(s *config.Settings) { s.Queue = a.queue.Snapshot() }); err != nil {
		// Losing the queue between runs is a nuisance, not a reason to fail the
		// edit the user just made, which has already taken effect in memory.
		a.logger.Error("could not save the queue", slog.Any("err", err))
	}
}

func (a *App) emitQueue() { a.emit(eventQueue, a.queue.Snapshot()) }

// playing reports whether the player, rather than a desktop capture, owns the
// voice connection right now.
func (a *App) playing() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode == ModePlayer
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func describeSkipped(n int) string {
	return plural(n, "file") + " skipped as not audio"
}
