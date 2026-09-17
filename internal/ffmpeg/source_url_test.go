package ffmpeg

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/audio"
)

// indexOfArg returns where flag sits in args, or -1.
func indexOfArg(args []string, flag string) int { return slices.Index(args, flag) }

func TestURLArgsOrdering(t *testing.T) {
	args := urlArgs("https://example.test/audio", nil, 30*time.Second)

	input := indexOfArg(args, "-i")
	if input < 0 {
		t.Fatalf("no -i in %v", args)
	}

	// Everything below is an input option: after -i it would either apply to
	// the output or be rejected outright.
	for _, flag := range []string{"-ss", "-reconnect", "-rw_timeout", "-multiple_requests"} {
		at := indexOfArg(args, flag)
		if at < 0 {
			t.Errorf("no %s in %v", flag, args)
			continue
		}
		if at > input {
			t.Errorf("%s is at %d, after -i at %d: it must precede the input", flag, at, input)
		}
	}

	// -ss before -i is what makes this a range request rather than a decode of
	// everything up to the offset.
	if got := args[indexOfArg(args, "-ss")+1]; got != "30.000" {
		t.Errorf("-ss = %q, want 30.000", got)
	}
	if got := args[input+1]; got != "https://example.test/audio" {
		t.Errorf("-i = %q, want the media URL", got)
	}
	if got := args[len(args)-1]; got != "pipe:1" {
		t.Errorf("last argument = %q, want pipe:1", got)
	}
}

func TestURLArgsOmitsSeekAtTheStart(t *testing.T) {
	if args := urlArgs("https://example.test/audio", nil, 0); indexOfArg(args, "-ss") >= 0 {
		t.Errorf("-ss present for a zero offset: %v", args)
	}
}

func TestURLArgsHeaders(t *testing.T) {
	headers := map[string]string{
		"User-Agent":      "Mozilla/5.0",
		"Cookie":          "a=b",
		"Origin":          "https://www.youtube.com",
		"Range":           "bytes=0-",
		"Accept-Encoding": "gzip",
		"Blank":           "  ",
	}
	args := urlArgs("https://example.test/audio", headers, 0)

	// The agent has its own option; in -headers ffmpeg's default shadows it.
	at := indexOfArg(args, "-user_agent")
	if at < 0 {
		t.Fatalf("no -user_agent in %v", args)
	}
	if got := args[at+1]; got != "Mozilla/5.0" {
		t.Errorf("-user_agent = %q, want Mozilla/5.0", got)
	}

	at = indexOfArg(args, "-headers")
	if at < 0 {
		t.Fatalf("no -headers in %v", args)
	}
	got := args[at+1]

	if want := "Cookie: a=b\r\nOrigin: https://www.youtube.com\r\n"; got != want {
		t.Errorf("-headers = %q, want %q", got, want)
	}
}

func TestURLArgsWithoutHeaders(t *testing.T) {
	args := urlArgs("https://example.test/audio", nil, 0)
	if indexOfArg(args, "-headers") >= 0 || indexOfArg(args, "-user_agent") >= 0 {
		t.Errorf("header options present with no headers: %v", args)
	}
}

// A value carrying a newline could append headers of its own, so it is dropped.
func TestFormatHeadersRejectsInjection(t *testing.T) {
	got := formatHeaders(map[string]string{
		"Cookie": "a=b\r\nX-Injected: yes",
		"Origin": "https://www.youtube.com",
	})
	if strings.Contains(got, "X-Injected") {
		t.Fatalf("formatHeaders = %q, want the smuggled header dropped", got)
	}
	if got != "Origin: https://www.youtube.com\r\n" {
		t.Errorf("formatHeaders = %q", got)
	}
}

func TestDescribeURL(t *testing.T) {
	tests := map[string]string{
		"https://rr1.googlevideo.com/videoplayback?expire=1&sig=verylong": "rr1.googlevideo.com",
		"not a url": "the media URL",
		"":          "the media URL",
	}
	for raw, want := range tests {
		if got := describeURL(raw); got != want {
			t.Errorf("describeURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

// OpenURL end to end against a local server: real ffmpeg, real HTTP, real range
// request, and no external network. This is what proves the option list above
// actually works rather than merely being ordered correctly.
func TestOpenURLDecodesOverHTTP(t *testing.T) {
	requireFFmpeg(t)

	path := makeTone(t, "tone.wav", 3)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var (
		mu   sync.Mutex
		seen []*http.Request
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r)
		mu.Unlock()

		// ServeContent is what answers the Range request a seek turns into.
		http.ServeContent(w, r, filepath.Base(path), time.Now(), strings.NewReader(string(body)))
	}))
	defer server.Close()

	headers := map[string]string{"User-Agent": "DiscordAudioStreamer/test", "Cookie": "session=abc"}

	src, err := OpenURL(context.Background(), server.URL+"/tone.wav", headers, 0)
	if err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	defer src.Close()

	decoded, err := io.ReadAll(src)
	if err != nil {
		t.Fatalf("read PCM: %v", err)
	}
	if err := src.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Three seconds of 48 kHz stereo s16le.
	want := 3 * audio.SampleRate * audio.Channels * 2
	if diff := len(decoded) - want; diff < -audio.BytesPerFrame*10 || diff > audio.BytesPerFrame*10 {
		t.Errorf("decoded %d bytes, want about %d", len(decoded), want)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("ffmpeg made no request")
	}
	if got := seen[0].Header.Get("User-Agent"); got != "DiscordAudioStreamer/test" {
		t.Errorf("User-Agent = %q, want the one we supplied", got)
	}
	if got := seen[0].Header.Get("Cookie"); got != "session=abc" {
		t.Errorf("Cookie = %q, want the one we supplied", got)
	}
}

// Seeking over HTTP must start where it was asked to, which is the whole reason
// for resolving a direct URL rather than piping a download into ffmpeg.
func TestOpenURLSeeks(t *testing.T) {
	requireFFmpeg(t)

	path := makeTone(t, "tone.wav", 4)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "tone.wav", time.Now(), strings.NewReader(string(body)))
	}))
	defer server.Close()

	src, err := OpenURL(context.Background(), server.URL+"/tone.wav", nil, 3*time.Second)
	if err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	defer src.Close()

	decoded, err := io.ReadAll(src)
	if err != nil {
		t.Fatalf("read PCM: %v", err)
	}
	if err := src.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if src.Offset() != 3*time.Second {
		t.Errorf("Offset() = %v, want 3s", src.Offset())
	}

	// One second of the four remains after the seek.
	want := 1 * audio.SampleRate * audio.Channels * 2
	if diff := len(decoded) - want; diff < -audio.BytesPerFrame*10 || diff > audio.BytesPerFrame*10 {
		t.Errorf("decoded %d bytes after seeking to 3s, want about %d", len(decoded), want)
	}
}
