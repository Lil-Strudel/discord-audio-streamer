package ytdlp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// Media is one video resolved to something ffmpeg can open.
type Media struct {
	// URL is a direct media address, valid only for a while and only from the
	// address that asked for it.
	URL string

	// Headers are what the CDN expects with that URL, chiefly a User-Agent
	// matching the one the address was issued to.
	Headers map[string]string

	Codec      string
	DurationMs int64
	Live       bool

	// ExpiresAt is when URL stops working, read out of the URL itself. It is
	// zero when it could not be determined.
	ExpiresAt time.Time
}

// ErrLive is returned for a stream or an unstarted premiere.
var ErrLive = errors.New("live streams and premieres cannot be queued")

// ErrNoStreamableFormat is returned when a video exists but none of its formats
// can be handed to ffmpeg directly.
var ErrNoStreamableFormat = errors.New("no streamable audio format is available for that video")

// ResolveMedia finds the audio for one video.
//
// This runs at play time rather than when the track was queued, because the
// address it produces is tied to the machine that asked for it and expires in a
// matter of hours. Resolving on the way into the queue would leave a long
// playlist full of addresses that had died before they were reached.
func ResolveMedia(ctx context.Context, watchURL string) (Media, error) {
	args := append([]string{"-J", "--no-playlist"}, urlArgs(watchURL)...)

	out, err := run(ctx, args)
	if err != nil {
		return Media{}, err
	}
	return parseMedia(out)
}

// videoInfo is the part of yt-dlp's -J output this package reads.
type videoInfo struct {
	Title      string   `json:"title"`
	Duration   *float64 `json:"duration"`
	IsLive     bool     `json:"is_live"`
	LiveStatus string   `json:"live_status"`
	Formats    []format `json:"formats"`
}

type format struct {
	FormatID string            `json:"format_id"`
	URL      string            `json:"url"`
	Protocol string            `json:"protocol"`
	ACodec   string            `json:"acodec"`
	VCodec   string            `json:"vcodec"`
	ABR      float64           `json:"abr"`
	Headers  map[string]string `json:"http_headers"`
}

// hasAudio reports whether the format carries a sound track at all.
func (f format) hasAudio() bool { return f.ACodec != "" && f.ACodec != "none" }

// audioOnly reports whether the format is sound with no video alongside, which
// is what this app wants: there is no picture to show, and downloading one
// would waste most of the bandwidth.
func (f format) audioOnly() bool { return f.hasAudio() && (f.VCodec == "" || f.VCodec == "none") }

// streamable reports whether ffmpeg can open the format from its URL.
//
// Plain http(s) and a native HLS playlist are both things ffmpeg fetches for
// itself. Segmented DASH is not: yt-dlp expresses it as a manifest that only
// its own downloader assembles, and handing the bare URL to ffmpeg produces a
// confusing failure rather than audio.
func (f format) streamable() bool {
	switch f.Protocol {
	case "https", "http", "m3u8_native":
		return f.URL != ""
	default:
		return false
	}
}

// parseMedia picks the format to play out of yt-dlp's description of a video.
//
// The choice is made here rather than with yt-dlp's -f expression so that the
// protocol can be part of it, and so that the rule is a table test rather than
// a string nobody can verify without the network.
func parseMedia(out []byte) (Media, error) {
	var info videoInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return Media{}, fmt.Errorf("could not read what yt-dlp reported: %w", err)
	}

	if info.IsLive || info.LiveStatus == "is_live" || info.LiveStatus == "is_upcoming" {
		return Media{}, ErrLive
	}

	best, ok := pickFormat(info.Formats)
	if !ok {
		return Media{}, ErrNoStreamableFormat
	}

	media := Media{
		URL:       best.URL,
		Headers:   best.Headers,
		Codec:     best.ACodec,
		ExpiresAt: expiryFromURL(best.URL),
	}
	if info.Duration != nil {
		media.DurationMs = int64(*info.Duration * float64(time.Second/time.Millisecond))
	}
	return media, nil
}

// pickFormat prefers the best audio-only stream, and falls back to a combined
// one only when the video offers nothing else.
func pickFormat(formats []format) (format, bool) {
	var candidates []format
	for _, f := range formats {
		if f.hasAudio() && f.streamable() {
			candidates = append(candidates, f)
		}
	}
	if len(candidates) == 0 {
		return format{}, false
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.audioOnly() != b.audioOnly() {
			return a.audioOnly()
		}
		return a.ABR > b.ABR
	})
	return candidates[0], true
}

// expiryFromURL reads the lifetime the CDN stamped into the address.
//
// Knowing when the address dies is what lets a resolution be cached for exactly
// as long as it is good for, instead of guessing at a safe interval.
func expiryFromURL(raw string) time.Time {
	parsed, err := url.Parse(raw)
	if err != nil {
		return time.Time{}
	}
	seconds, err := strconv.ParseInt(parsed.Query().Get("expire"), 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}
