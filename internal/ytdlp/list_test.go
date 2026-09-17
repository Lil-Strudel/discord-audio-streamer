package ytdlp

import "testing"

func TestParseEntryLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		want     Entry
		unusable bool
	}{
		{
			name: "ordinary video",
			line: `{"id":"dQw4w9WgXcQ","title":"Never Gonna Give You Up","duration":212.0,"live_status":"not_live"}`,
			want: Entry{VideoID: "dQw4w9WgXcQ", Title: "Never Gonna Give You Up", DurationMs: 212_000},
		},
		{
			// yt-dlp reports fractional seconds, and rounding them away would
			// drift a long queue's timings.
			name: "fractional duration",
			line: `{"id":"dQw4w9WgXcQ","title":"Song","duration":212.48,"live_status":null}`,
			want: Entry{VideoID: "dQw4w9WgXcQ", Title: "Song", DurationMs: 212_480},
		},
		{
			// A flat listing often omits live_status entirely.
			name: "no live status",
			line: `{"id":"dQw4w9WgXcQ","title":"Song","duration":60}`,
			want: Entry{VideoID: "dQw4w9WgXcQ", Title: "Song", DurationMs: 60_000},
		},
		{
			name: "live stream",
			line: `{"id":"dQw4w9WgXcQ","title":"Lofi radio","duration":null,"live_status":"is_live"}`,
			want: Entry{VideoID: "dQw4w9WgXcQ", Title: "Lofi radio", Live: true},
		},
		{
			name: "premiere",
			line: `{"id":"dQw4w9WgXcQ","title":"Coming soon","duration":null,"live_status":"is_upcoming"}`,
			want: Entry{VideoID: "dQw4w9WgXcQ", Title: "Coming soon", Live: true},
		},
		{
			// Null duration on a normal video means unknown, not zero-length.
			name: "null duration",
			line: `{"id":"dQw4w9WgXcQ","title":"Song","duration":null,"live_status":"not_live"}`,
			want: Entry{VideoID: "dQw4w9WgXcQ", Title: "Song"},
		},
		{
			name: "no title falls back to the url",
			line: `{"id":"dQw4w9WgXcQ","title":"","duration":10}`,
			want: Entry{
				VideoID:    "dQw4w9WgXcQ",
				Title:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
				DurationMs: 10_000,
			},
		},

		// A playlist carries its dead entries along with the live ones.
		{
			name:     "deleted video",
			line:     `{"id":null,"title":"[Deleted video]","duration":null}`,
			unusable: true,
		},
		{
			name:     "private video",
			line:     `{"id":"","title":"[Private video]","duration":null}`,
			unusable: true,
		},
		{name: "not json", line: `WARNING: something happened`, unusable: true},
		{name: "empty object", line: `{}`, unusable: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseEntryLine([]byte(tc.line))
			if ok == tc.unusable {
				t.Fatalf("parseEntryLine ok = %v, want %v", ok, !tc.unusable)
			}
			if tc.unusable {
				return
			}
			if got != tc.want {
				t.Errorf("parseEntryLine = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestEntryURL(t *testing.T) {
	entry := Entry{VideoID: "dQw4w9WgXcQ"}
	if got, want := entry.URL(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ"; got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}
