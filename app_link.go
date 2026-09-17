package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/ffmpeg"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/playlist"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/ytdlp"
)

// Timeouts for the two things yt-dlp is asked to do. Listing a long playlist is
// a slow walk over many pages, while resolving one video is a single lookup
// that the user is waiting on with the music stopped.
const (
	listTimeout    = 3 * time.Minute
	resolveTimeout = 45 * time.Second

	// autoResolveTimeout applies when the queue is advancing by itself. It is
	// shorter because a track that will not resolve promptly is holding up
	// everything behind it, and skipping on is better than a long silence.
	autoResolveTimeout = 15 * time.Second
)

// ------------------------------------------------------------------- adding

// AddLink queues a YouTube link: one video, or every video of a playlist.
//
// Unlike a file import this reads the listing before returning anything, since
// there is nothing to show until yt-dlp says what the link contains. The entries
// still appear as they arrive rather than all at once at the end, which is what
// makes a long playlist look like it is working rather than hung.
func (a *App) AddLink(raw string) error {
	link, err := ytdlp.ParseLink(raw)
	if err != nil {
		return err
	}
	if _, err := ytdlp.Resolve(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(a.ctx, listTimeout)
	defer cancel()

	var (
		added int
		live  int
	)

	// Pushing the queue after every entry would be one webview round trip per
	// track, so they are batched the way a folder import's metadata is.
	flush := time.NewTicker(250 * time.Millisecond)
	defer flush.Stop()

	result, listErr := ytdlp.List(ctx, link, maxImportTracks, func(entry ytdlp.Entry) {
		// A stream has no length and never ends, and a premiere has no media at
		// all. Either would sit in the queue as a row that cannot behave like
		// the others, so they are counted and reported instead.
		if entry.Live {
			live++
			return
		}

		a.queue.Add(playlist.Track{
			ID:         playlist.NewID(),
			Path:       entry.URL(),
			Source:     playlist.SourceYouTube,
			Name:       entry.Title,
			DurationMs: entry.DurationMs,
		})
		added++

		select {
		case <-flush.C:
			a.emitQueue()
		default:
		}
	})

	// Whatever arrived before a failure is worth keeping: a playlist that died
	// half way through still queued the half that worked.
	if added > 0 {
		a.saveQueue()
	}
	if listErr != nil {
		if added > 0 {
			a.logger.Warn("a link was only partly listed",
				slog.Int("added", added), slog.Any("err", listErr))
		}
		return listErr
	}

	// Nothing queued is a failure worth explaining rather than a silent no-op:
	// the usual cause is a link to a stream, which looks like an ordinary video
	// until it is looked at.
	if added == 0 {
		return errors.New("nothing was added: " + strings.Join(reasonsNothingAdded(live, result), ", "))
	}

	if notes := describeImport(added, live, result); notes != "" {
		a.emit(eventError, notes)
	}
	return nil
}

// reasonsNothingAdded explains an import that produced no tracks at all.
func reasonsNothingAdded(live int, result ytdlp.ListResult) []string {
	var reasons []string
	if live > 0 {
		reasons = append(reasons, describeLive(live)+" cannot be queued")
	}
	if result.Skipped > 0 {
		reasons = append(reasons, plural(result.Skipped, "video")+" unavailable")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "that link contains nothing playable")
	}
	return reasons
}

// describeLive names what was left out. The two halves do not share a plural,
// so the phrase is written rather than assembled.
func describeLive(n int) string {
	if n == 1 {
		return "a live stream or premiere"
	}
	return fmt.Sprintf("%d live streams and premieres", n)
}

// describeImport reports anything about an import the queue itself does not
// show. A link that produced exactly what was asked for says nothing: the new
// rows are their own confirmation.
func describeImport(added, live int, result ytdlp.ListResult) string {
	var notes []string
	if result.Truncated {
		notes = append(notes, "the rest was left out")
	}
	if result.Skipped > 0 {
		notes = append(notes, plural(result.Skipped, "video")+" unavailable")
	}
	if live > 0 {
		notes = append(notes, describeLive(live)+" skipped")
	}

	if len(notes) == 0 {
		return ""
	}
	return "Added " + plural(added, "track") + "; " + strings.Join(notes, ", ")
}

// ---------------------------------------------------------------- resolving

// playbackInput is where one track's audio is about to be read from.
//
// It exists so that deciding what to open is separate from opening it: the
// decision can take seconds for a link and none at all for a file, and only the
// caller knows whether the answer is still wanted by the time it arrives.
type playbackInput struct {
	path    string
	url     string
	headers map[string]string
}

// open starts decoding at offset.
func (i playbackInput) open(ctx context.Context, offset time.Duration) (ffmpeg.Source, error) {
	if i.url != "" {
		return ffmpeg.OpenURL(ctx, i.url, i.headers, offset)
	}
	return ffmpeg.OpenFile(ctx, i.path, offset)
}

// resolveInput works out where a queue entry's audio comes from.
//
// A local file is already the answer. A link has to be asked about, which is
// why this can block, and why it happens here rather than when the track was
// queued: the address yt-dlp returns is tied to this machine and expires within
// hours, so one resolved at add time would be dead before a long queue reached
// it.
func (a *App) resolveInput(ctx context.Context, track playlist.Track, auto bool) (playbackInput, error) {
	if track.Source != playlist.SourceYouTube {
		return playbackInput{path: track.Path}, nil
	}

	if media, ok := a.links.Get(track.Path); ok {
		return playbackInput{url: media.URL, headers: media.Headers}, nil
	}

	timeout := resolveTimeout
	if auto {
		timeout = autoResolveTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	media, err := ytdlp.ResolveMedia(ctx, track.Path)
	if err != nil {
		return playbackInput{}, err
	}

	a.links.Put(track.Path, media)
	a.noteResolved(track, media)

	return playbackInput{url: media.URL, headers: media.Headers}, nil
}

// noteResolved records what resolving taught us about the track.
//
// A listing gives no codec, so a link's row would otherwise stay blank where a
// file's fills in after probing. The duration is only overwritten when the
// listing had none, since the listing's figure is the one the seek bar has been
// using.
func (a *App) noteResolved(track playlist.Track, media ytdlp.Media) {
	duration := track.DurationMs
	if duration == 0 {
		duration = media.DurationMs
	}
	if media.Codec == "" && duration == track.DurationMs {
		return
	}

	// An empty name leaves the existing one alone, which is what we want here.
	if a.queue.SetMetadata(track.ID, "", media.Codec, duration) {
		a.saveQueue()
	}
}

// forgetResolved drops a cached address after playback failed on it.
//
// The usual reason is that it expired or was issued to a different address than
// the one now asking, and both are fixed by resolving again. Without this the
// dead address would be handed out until its own clock said it had expired.
func (a *App) forgetResolved(track playlist.Track) {
	if track.Source == playlist.SourceYouTube {
		a.links.Forget(track.Path)
	}
}
