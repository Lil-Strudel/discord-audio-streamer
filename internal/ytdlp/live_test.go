package ytdlp

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
	"github.com/Lil-Strudel/discord-audio-streamer/internal/ffmpeg"
)

// These are the only tests here that talk to YouTube, and they are opt-in.
//
// Guarding on an explicit variable rather than on -short is deliberate: CI runs
// the suite without -short, and a shared runner's address is routinely served a
// bot check instead of a video. A test that fails for that reason would be a
// permanently red build reporting nothing about this code.
//
//	DAS_YTDLP_LIVE=1 go test ./internal/ytdlp/ -run Live -v
func requireLive(t *testing.T) {
	t.Helper()

	if os.Getenv("DAS_YTDLP_LIVE") == "" {
		t.Skip("set DAS_YTDLP_LIVE=1 to run tests that reach YouTube")
	}
	if _, err := Resolve(); err != nil {
		t.Skipf("yt-dlp unavailable: %v", err)
	}
}

// liveVideo is a video that has been public for over a decade, which is about
// as stable as a fixture on someone else's service can be.
const liveVideo = "dQw4w9WgXcQ"

func TestLiveListSingleVideo(t *testing.T) {
	requireLive(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	var entries []Entry
	result, err := List(ctx, Link{VideoID: liveVideo}, 10, func(e Entry) {
		entries = append(entries, e)
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("listed %d entries, want 1", len(entries))
	}
	if entries[0].VideoID != liveVideo {
		t.Errorf("VideoID = %q, want %q", entries[0].VideoID, liveVideo)
	}
	if entries[0].Title == "" || entries[0].Title == entries[0].URL() {
		t.Errorf("Title = %q, want a real title", entries[0].Title)
	}
	if entries[0].DurationMs == 0 {
		t.Error("DurationMs = 0, want the video's length")
	}
	if result.Truncated {
		t.Error("a single video reported itself as truncated")
	}
}

// The cap has to stop a long playlist, since it is the only thing standing
// between a stray paste and thousands of queued rows.
func TestLiveListPlaylistTruncates(t *testing.T) {
	requireLive(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const limit = 3
	var entries []Entry
	result, err := List(ctx, Link{PlaylistID: "PLbpi6ZahtOH6Blw3RGYpWkSByi_T7Rygb"}, limit, func(e Entry) {
		entries = append(entries, e)
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(entries) != limit {
		t.Fatalf("listed %d entries, want the limit of %d", len(entries), limit)
	}
	if !result.Truncated {
		t.Error("Truncated = false, want it set once the limit stopped the listing")
	}
}

func TestLiveResolveMedia(t *testing.T) {
	requireLive(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	media, err := ResolveMedia(ctx, Link{VideoID: liveVideo}.WatchURL())
	if err != nil {
		t.Fatalf("ResolveMedia: %v", err)
	}

	if media.URL == "" {
		t.Fatal("no media URL")
	}
	if media.Codec == "" {
		t.Error("no codec reported")
	}
	if media.DurationMs == 0 {
		t.Error("DurationMs = 0, want the video's length")
	}
	if len(media.Headers) == 0 {
		t.Error("no headers reported; the CDN usually requires a matching agent")
	}
	if media.ExpiresAt.IsZero() {
		t.Error("no expiry parsed out of the media URL")
	} else if !media.ExpiresAt.After(time.Now()) {
		t.Errorf("ExpiresAt = %v, which is already past", media.ExpiresAt)
	}
}

// A video that does not exist must produce the reason rather than an exit code,
// since that message is what the user is shown when a track is skipped.
func TestLiveResolveMissingVideoExplainsWhy(t *testing.T) {
	requireLive(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	_, err := ResolveMedia(ctx, "https://www.youtube.com/watch?v=aaaaaaaaaaa")
	if err == nil {
		t.Fatal("ResolveMedia succeeded on a video that does not exist")
	}
	if got := err.Error(); got == "" || got == "exit status 1" {
		t.Errorf("error = %q, want the reason yt-dlp gave", got)
	}
	t.Logf("reported: %v", err)
}

// The whole chain, which is the only way to know the pieces fit: yt-dlp finds
// where the audio is, ffmpeg fetches that address with the headers it came with
// and decodes it, and what comes out is the PCM the pipeline expects.
//
// It also proves seeking works over HTTP against the real CDN rather than
// against a local server, which is the part most likely to differ.
func TestLiveResolveAndDecode(t *testing.T) {
	requireLive(t)
	if _, err := ffmpeg.Resolve(); err != nil {
		t.Skipf("ffmpeg unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	media, err := ResolveMedia(ctx, Link{VideoID: liveVideo}.WatchURL())
	if err != nil {
		t.Fatalf("ResolveMedia: %v", err)
	}

	const offset = 30 * time.Second
	src, err := ffmpeg.OpenURL(ctx, media.URL, media.Headers, offset)
	if err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	defer src.Close()

	// One second of audio is enough to prove it decodes; reading the whole
	// track would download it for no added confidence.
	want := audio.SampleRate * audio.Channels * 2
	decoded, err := io.ReadAll(io.LimitReader(src, int64(want)))
	if err != nil {
		t.Fatalf("read PCM: %v", err)
	}
	if len(decoded) < want {
		t.Fatalf("decoded %d bytes, want %d", len(decoded), want)
	}

	// Silence would mean the range request landed somewhere unexpected, or that
	// the CDN served an error page that ffmpeg decoded as nothing.
	var loudest int16
	for i := 0; i+1 < len(decoded); i += 2 {
		sample := int16(decoded[i]) | int16(decoded[i+1])<<8
		if sample > loudest {
			loudest = sample
		}
	}
	if loudest == 0 {
		t.Error("decoded a second of pure silence, want audio")
	}
	t.Logf("decoded %d bytes from %v in, peak sample %d", len(decoded), offset, loudest)
}
