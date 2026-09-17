package ytdlp

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// A trimmed -J payload in the shape yt-dlp actually emits: several formats,
// audio-only ones among video ones, and a segmented DASH entry that ffmpeg
// cannot open from its URL.
const videoJSON = `{
  "id": "dQw4w9WgXcQ",
  "title": "Never Gonna Give You Up",
  "duration": 212.061,
  "is_live": false,
  "live_status": "not_live",
  "formats": [
    {
      "format_id": "18",
      "url": "https://rr1.googlevideo.com/videoplayback?expire=1789000000&itag=18",
      "protocol": "https",
      "acodec": "mp4a.40.2",
      "vcodec": "avc1.42001E",
      "abr": 96,
      "http_headers": {"User-Agent": "Mozilla/5.0", "Accept-Language": "en-us"}
    },
    {
      "format_id": "248",
      "url": "https://rr1.googlevideo.com/videoplayback?expire=1789000000&itag=248",
      "protocol": "https",
      "acodec": "none",
      "vcodec": "vp9",
      "abr": 0
    },
    {
      "format_id": "251",
      "url": "https://rr1.googlevideo.com/videoplayback?expire=1789000000&itag=251",
      "protocol": "https",
      "acodec": "opus",
      "vcodec": "none",
      "abr": 160,
      "http_headers": {"User-Agent": "Mozilla/5.0"}
    },
    {
      "format_id": "140",
      "url": "https://rr1.googlevideo.com/videoplayback?expire=1789000000&itag=140",
      "protocol": "https",
      "acodec": "mp4a.40.2",
      "vcodec": "none",
      "abr": 128,
      "http_headers": {"User-Agent": "Mozilla/5.0"}
    },
    {
      "format_id": "234",
      "url": "https://manifest.googlevideo.com/api/manifest/dash/",
      "protocol": "http_dash_segments",
      "acodec": "opus",
      "vcodec": "none",
      "abr": 999
    }
  ]
}`

func TestParseMediaPicksTheBestAudioOnlyFormat(t *testing.T) {
	media, err := parseMedia([]byte(videoJSON))
	if err != nil {
		t.Fatalf("parseMedia: %v", err)
	}

	// 234 has the highest bitrate but is segmented DASH, and 18 carries video
	// alongside the audio; 251 is the best of what ffmpeg can actually open.
	if !strings.HasSuffix(media.URL, "itag=251") {
		t.Errorf("URL = %q, want the opus audio-only format (itag=251)", media.URL)
	}
	if media.Codec != "opus" {
		t.Errorf("Codec = %q, want opus", media.Codec)
	}
	if media.DurationMs != 212_061 {
		t.Errorf("DurationMs = %d, want 212061", media.DurationMs)
	}
	if media.Headers["User-Agent"] != "Mozilla/5.0" {
		t.Errorf("Headers = %v, want the format's User-Agent", media.Headers)
	}
	if got, want := media.ExpiresAt, time.Unix(1789000000, 0); !got.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", got, want)
	}
}

// A video offering only combined audio and video is still playable; ffmpeg
// discards the picture. Refusing it would be worse than the wasted bandwidth.
func TestParseMediaFallsBackToACombinedFormat(t *testing.T) {
	const combinedOnly = `{
	  "duration": 60,
	  "formats": [
	    {"format_id":"18","url":"https://example.test/a","protocol":"https","acodec":"mp4a.40.2","vcodec":"avc1","abr":96}
	  ]
	}`

	media, err := parseMedia([]byte(combinedOnly))
	if err != nil {
		t.Fatalf("parseMedia: %v", err)
	}
	if media.URL != "https://example.test/a" {
		t.Errorf("URL = %q, want the combined format", media.URL)
	}
}

func TestParseMediaRejects(t *testing.T) {
	tests := []struct {
		name string
		json string
		want error
	}{
		{
			name: "live stream",
			json: `{"is_live": true, "live_status": "is_live", "formats": [
				{"url":"https://example.test/a","protocol":"https","acodec":"opus","vcodec":"none"}]}`,
			want: ErrLive,
		},
		{
			name: "premiere",
			json: `{"live_status": "is_upcoming", "formats": []}`,
			want: ErrLive,
		},
		{
			// Everything is segmented DASH, which ffmpeg cannot open directly.
			name: "nothing streamable",
			json: `{"duration": 10, "formats": [
				{"url":"https://example.test/m","protocol":"http_dash_segments","acodec":"opus","vcodec":"none"}]}`,
			want: ErrNoStreamableFormat,
		},
		{
			name: "video with no audio at all",
			json: `{"duration": 10, "formats": [
				{"url":"https://example.test/v","protocol":"https","acodec":"none","vcodec":"vp9"}]}`,
			want: ErrNoStreamableFormat,
		},
		{name: "no formats", json: `{"duration": 10}`, want: ErrNoStreamableFormat},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseMedia([]byte(tc.json)); !errors.Is(err, tc.want) {
				t.Fatalf("parseMedia error = %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("not json", func(t *testing.T) {
		if _, err := parseMedia([]byte("ERROR: Video unavailable")); err == nil {
			t.Fatal("parseMedia succeeded, want an error")
		}
	})
}

func TestExpiryFromURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Time
	}{
		{
			name: "present",
			raw:  "https://rr1.googlevideo.com/videoplayback?expire=1789000000&itag=251",
			want: time.Unix(1789000000, 0),
		},
		{name: "absent", raw: "https://example.test/a"},
		{name: "not a number", raw: "https://example.test/a?expire=soon"},
		{name: "negative", raw: "https://example.test/a?expire=-5"},
		{name: "not a url", raw: "://"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := expiryFromURL(tc.raw)
			if !got.Equal(tc.want) {
				t.Errorf("expiryFromURL(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestErrorFromStderr(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{
			name:  "strips the extractor prefix",
			lines: []string{"ERROR: [youtube] dQw4w9WgXcQ: Video unavailable"},
			want:  "Video unavailable",
		},
		{
			name:  "age gate",
			lines: []string{"ERROR: [youtube] abc: Sign in to confirm your age"},
			want:  "Sign in to confirm your age",
		},
		{
			name:  "finds the error among other output",
			lines: []string{"WARNING: nothing to worry about", "ERROR: [youtube] x: Private video"},
			want:  "Private video",
		},
		{
			name:  "no prefix to strip",
			lines: []string{"ERROR: Unable to download webpage"},
			want:  "Unable to download webpage",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := errorFromStderr(tc.lines, errors.New("exit status 1"))
			if err.Error() != tc.want {
				t.Errorf("errorFromStderr = %q, want %q", err, tc.want)
			}
		})
	}

	t.Run("no error line keeps the exit status", func(t *testing.T) {
		fallback := errors.New("exit status 1")
		err := errorFromStderr([]string{"WARNING: hmm"}, fallback)
		if !errors.Is(err, fallback) {
			t.Errorf("errorFromStderr = %v, want it to wrap the exit status", err)
		}
	})

	t.Run("no output at all", func(t *testing.T) {
		fallback := errors.New("exit status 1")
		if err := errorFromStderr(nil, fallback); !errors.Is(err, fallback) {
			t.Errorf("errorFromStderr = %v, want the exit status", err)
		}
	})
}
