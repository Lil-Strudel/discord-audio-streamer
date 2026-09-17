package ytdlp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Entry is one video a link expands to.
type Entry struct {
	VideoID    string
	Title      string
	DurationMs int64

	// Live marks a stream or an unstarted premiere. Neither can be queued: a
	// stream has no duration and never ends, and a premiere has no media yet.
	Live bool
}

// URL is the address to resolve when this entry is played.
func (e Entry) URL() string { return Link{VideoID: e.VideoID}.WatchURL() }

// ListResult reports what a listing produced beyond the entries themselves.
type ListResult struct {
	// Truncated is set when the limit stopped the listing early, so the caller
	// can say so rather than silently queueing part of a playlist.
	Truncated bool

	// Skipped counts entries that were listed but cannot be played, which is
	// mostly deleted and private videos inside an otherwise fine playlist.
	Skipped int
}

// entryFields is the projection asked of yt-dlp, one JSON object per line.
//
// --print with a template is used rather than -J because -J emits the whole
// playlist as a single JSON document: a two thousand entry listing is one line
// of several hundred kilobytes, which no line reader should have to hold, and
// nothing can be shown until the last byte of it arrives. A line per entry
// streams, fills the queue as it goes, and can be cut off at the limit.
const entryFields = "%(.{id,title,duration,live_status})j"

// List enumerates what link contains, calling onEntry for each playable video
// as it arrives.
//
// A link naming a single video produces one entry, so callers do not have to
// care which kind they were given.
func List(ctx context.Context, link Link, limit int, onEntry func(Entry)) (ListResult, error) {
	args := []string{"--flat-playlist", "--print", entryFields}
	if link.IsPlaylist() {
		args = append(args, "--yes-playlist")
	} else {
		args = append(args, "--no-playlist")
	}
	args = append(args, urlArgs(link.ListingURL())...)

	var result ListResult
	var found int

	err := runLines(ctx, args, func(line []byte) bool {
		entry, ok := parseEntryLine(line)
		if !ok {
			result.Skipped++
			return true
		}

		onEntry(entry)
		found++

		if limit > 0 && found >= limit {
			result.Truncated = true
			return false
		}
		return true
	})
	if err != nil {
		return ListResult{}, err
	}

	if found == 0 {
		if result.Skipped > 0 {
			return ListResult{}, fmt.Errorf("nothing in that link can be played: %d unavailable", result.Skipped)
		}
		return ListResult{}, fmt.Errorf("that link contains nothing to play")
	}
	return result, nil
}

// flatEntry is one line of the listing.
//
// Duration is a float because yt-dlp reports fractional seconds, and a pointer
// because it is null for a live stream — which is a fact worth keeping, not a
// zero to be confused with a video of no length.
type flatEntry struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Duration   *float64 `json:"duration"`
	LiveStatus string   `json:"live_status"`
}

// parseEntryLine reads one listed entry and reports whether it is playable.
//
// A flat listing includes videos that have been deleted or made private, with
// a placeholder title and no usable id. They are dropped here rather than
// queued as rows that could only ever fail.
func parseEntryLine(line []byte) (Entry, bool) {
	var raw flatEntry
	if err := json.Unmarshal(line, &raw); err != nil {
		return Entry{}, false
	}
	if !videoIDRe.MatchString(raw.ID) {
		return Entry{}, false
	}

	entry := Entry{VideoID: raw.ID, Title: raw.Title}

	switch raw.LiveStatus {
	case "is_live", "is_upcoming", "post_live":
		entry.Live = true
	default:
		// Duration is null for anything without a fixed length, which is worth
		// keeping as zero rather than guessing at.
		if raw.Duration != nil {
			entry.DurationMs = int64(*raw.Duration * float64(time.Second/time.Millisecond))
		}
	}

	if entry.Title == "" {
		// The title is what the row is labelled with, and an entry that arrived
		// without one is still playable.
		entry.Title = entry.URL()
	}
	return entry, true
}
