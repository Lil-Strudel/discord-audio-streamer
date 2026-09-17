package ffmpeg

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// networkTimeout bounds how long a stalled read may block before ffmpeg gives
// up. Without it a CDN that accepts the connection and then goes quiet wedges
// the decode for as long as the track is left playing, and the pipeline just
// sees a source that never produces.
const networkTimeout = 15 * time.Second

// skippedHeaders are ones ffmpeg must be left to manage itself.
//
// Range is the mechanism seeking depends on, Host follows from the URL, and
// passing through the encoding and connection handling a different client
// negotiated only confuses matters.
var skippedHeaders = map[string]bool{
	"range":           true,
	"host":            true,
	"accept-encoding": true,
	"connection":      true,
	"content-length":  true,
}

// OpenURL decodes a media URL to PCM, beginning at offset.
//
// This is the same decode as OpenFile with the input flags a network source
// needs, and it deliberately stays a separate entry point: the reconnect and
// timeout options below are http-protocol options, and ffmpeg fails outright
// when they are applied to a local file.
//
// Seeking works the way it does for a file — a new decode at a new offset, with
// -ss before -i — because ffmpeg turns that into a range request rather than
// fetching and discarding everything up to the offset.
func OpenURL(ctx context.Context, mediaURL string, headers map[string]string, offset time.Duration) (*FileSource, error) {
	bin, err := Resolve()
	if err != nil {
		return nil, err
	}

	proc, err := Start(ctx, bin, urlArgs(mediaURL, headers, offset), nil)
	if err != nil {
		return nil, fmt.Errorf("stream %s: %w", describeURL(mediaURL), err)
	}
	return &FileSource{proc: proc, offset: offset}, nil
}

// urlArgs builds the command line for a network decode. It is separate from
// OpenURL so the ordering rules below can be asserted without running ffmpeg.
func urlArgs(mediaURL string, headers map[string]string, offset time.Duration) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}

	// A long track outlives the connection it started on often enough that
	// reconnecting is the difference between playing to the end and stopping
	// silently part way.
	args = append(args,
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_on_network_error", "1",
		"-reconnect_delay_max", "5",
		"-multiple_requests", "1",
		"-rw_timeout", fmt.Sprintf("%d", networkTimeout.Microseconds()),
	)

	// The User-Agent goes in its own option rather than into -headers, where
	// ffmpeg's built-in default would shadow it. A media URL is often issued to
	// one agent and refused to others, so this is load bearing.
	if ua := headerValue(headers, "User-Agent"); ua != "" {
		args = append(args, "-user_agent", ua)
	}
	if extra := formatHeaders(headers); extra != "" {
		args = append(args, "-headers", extra)
	}

	// Before -i, as for a file: it is what makes ffmpeg seek rather than decode
	// and discard.
	if offset > 0 {
		args = append(args, "-ss", formatSeekTime(offset))
	}

	args = append(args, "-i", mediaURL)
	return append(args, pcmOutputArgs()...)
}

// formatHeaders renders the remaining headers the way ffmpeg expects them: one
// per line, CRLF terminated, in a single argument.
func formatHeaders(headers map[string]string) string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		if skippedHeaders[strings.ToLower(name)] || strings.EqualFold(name, "User-Agent") {
			continue
		}
		if strings.TrimSpace(headers[name]) == "" {
			continue
		}
		names = append(names, name)
	}

	// Sorted so the command line is the same from one run to the next, which
	// makes a failure reproducible and this function testable.
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		// A newline smuggled into a value would let it inject headers of its
		// own, so anything carrying one is dropped rather than sanitised.
		if strings.ContainsAny(headers[name], "\r\n") {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\r\n", name, headers[name])
	}
	return b.String()
}

func headerValue(headers map[string]string, want string) string {
	for name, value := range headers {
		if strings.EqualFold(name, want) {
			return value
		}
	}
	return ""
}

// describeURL trims a media URL down to something worth putting in an error.
// They run to several hundred characters of signed query parameters, none of
// which mean anything to the person reading the message.
func describeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "the media URL"
	}
	return parsed.Host
}
