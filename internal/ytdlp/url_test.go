package ytdlp

import (
	"errors"
	"testing"
)

const videoID = "dQw4w9WgXcQ"

func TestParseLink(t *testing.T) {
	tests := []struct {
		raw      string
		video    string
		playlist string
	}{
		{raw: "https://www.youtube.com/watch?v=" + videoID, video: videoID},
		{raw: "http://youtube.com/watch?v=" + videoID, video: videoID},
		{raw: "https://m.youtube.com/watch?v=" + videoID, video: videoID},
		{raw: "https://music.youtube.com/watch?v=" + videoID, video: videoID},
		{raw: "https://youtu.be/" + videoID, video: videoID},
		{raw: "https://youtu.be/" + videoID + "?t=42", video: videoID},
		{raw: "https://www.youtube.com/shorts/" + videoID, video: videoID},
		{raw: "https://www.youtube.com/live/" + videoID, video: videoID},
		{raw: "https://www.youtube.com/embed/" + videoID, video: videoID},

		// No scheme: what a copy out of the address bar often looks like.
		{raw: "youtube.com/watch?v=" + videoID, video: videoID},
		{raw: "  https://youtu.be/" + videoID + "  ", video: videoID},

		// Playlists, with and without a video alongside.
		{raw: "https://www.youtube.com/playlist?list=PLabcdef123", playlist: "PLabcdef123"},
		{
			raw:      "https://www.youtube.com/watch?v=" + videoID + "&list=PLabcdef123&index=3",
			video:    videoID,
			playlist: "PLabcdef123",
		},
		{
			raw:      "https://youtu.be/" + videoID + "?list=RD" + videoID,
			video:    videoID,
			playlist: "RD" + videoID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			link, err := ParseLink(tc.raw)
			if err != nil {
				t.Fatalf("ParseLink(%q): %v", tc.raw, err)
			}
			if link.VideoID != tc.video {
				t.Errorf("VideoID = %q, want %q", link.VideoID, tc.video)
			}
			if link.PlaylistID != tc.playlist {
				t.Errorf("PlaylistID = %q, want %q", link.PlaylistID, tc.playlist)
			}
			if link.IsPlaylist() != (tc.playlist != "") {
				t.Errorf("IsPlaylist() = %v", link.IsPlaylist())
			}
		})
	}
}

func TestParseLinkRejects(t *testing.T) {
	tests := []string{
		"",
		"   ",
		"not a url",
		"https://vimeo.com/123456",
		"https://example.com/watch?v=" + videoID,

		// A lookalike host must not pass; this is the reason for an allowlist
		// rather than a substring check.
		"https://youtube.com.evil.test/watch?v=" + videoID,
		"https://notyoutube.com/watch?v=" + videoID,

		// Non-http schemes, including ones a webview might act on.
		"javascript:alert(1)",
		"file:///etc/passwd",

		// YouTube, but naming nothing playable.
		"https://www.youtube.com/",
		"https://www.youtube.com/feed/subscriptions",
		"https://www.youtube.com/watch?v=tooshort",
		"https://www.youtube.com/watch?v=waytoolongtobeanid",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseLink(raw); !errors.Is(err, ErrNotYouTube) {
				t.Fatalf("ParseLink(%q) error = %v, want ErrNotYouTube", raw, err)
			}
		})
	}
}

func TestLinkURLs(t *testing.T) {
	tests := []struct {
		name    string
		link    Link
		listing string
	}{
		{
			name:    "video only",
			link:    Link{VideoID: videoID},
			listing: "https://www.youtube.com/watch?v=" + videoID,
		},
		{
			name:    "playlist only",
			link:    Link{PlaylistID: "PLabc"},
			listing: "https://www.youtube.com/playlist?list=PLabc",
		},
		{
			// A mix only exists relative to the video it started from, so both
			// ids have to survive into the listing address.
			name:    "video and mix",
			link:    Link{VideoID: videoID, PlaylistID: "RD" + videoID},
			listing: "https://www.youtube.com/watch?v=" + videoID + "&list=RD" + videoID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.link.ListingURL(); got != tc.listing {
				t.Errorf("ListingURL() = %q, want %q", got, tc.listing)
			}
		})
	}

	link := Link{VideoID: videoID, PlaylistID: "PLabc"}
	if got, want := link.WatchURL(), "https://www.youtube.com/watch?v="+videoID; got != want {
		t.Errorf("WatchURL() = %q, want %q", got, want)
	}
}

// Every URL the parser emits must survive a round trip back through it.
func TestLinkURLsReparse(t *testing.T) {
	for _, link := range []Link{
		{VideoID: videoID},
		{PlaylistID: "PLabcdef123"},
		{VideoID: videoID, PlaylistID: "RD" + videoID},
	} {
		got, err := ParseLink(link.ListingURL())
		if err != nil {
			t.Fatalf("ParseLink(%q): %v", link.ListingURL(), err)
		}
		if got != link {
			t.Errorf("round trip gave %+v, want %+v", got, link)
		}
	}
}
