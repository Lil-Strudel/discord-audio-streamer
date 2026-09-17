package ffmpeg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsAudioFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/music/song.mp3", true},
		{"/music/song.FLAC", true}, // matching is case insensitive
		{"song.opus", true},
		{`C:\Music\song.m4a`, true},
		{"/music/cover.jpg", false},
		{"/music/playlist.cue", false},
		{"/music/README", false},
		{"", false},
		{"/music/.hidden", false}, // a dotfile is not an "hidden" extension
	}

	for _, c := range cases {
		if got := IsAudioFile(c.path); got != c.want {
			t.Errorf("IsAudioFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestAudioFilePatternCoversEveryExtension(t *testing.T) {
	pattern := AudioFilePattern()
	for _, ext := range AudioExtensions() {
		if !strings.Contains(pattern, "*."+ext) {
			t.Errorf("pattern %q is missing %q", pattern, ext)
		}
	}
}

func TestAudioExtensionsDoesNotAliasThePackageSlice(t *testing.T) {
	AudioExtensions()[0] = "exe"
	if !IsAudioFile("song.mp3") {
		t.Fatal("the caller's copy mutated the package list")
	}
}

// audioTree lays out a folder of mixed content to walk.
func audioTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := []string{
		"b.mp3",
		"a.flac",
		"cover.jpg",
		"notes.txt",
		filepath.Join("disc2", "c.opus"),
		filepath.Join("disc2", "folder.png"),
	}
	for _, name := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFindAudioFilesWalksSubfoldersAndSkipsNonAudio(t *testing.T) {
	root := audioTree(t)

	got, err := FindAudioFiles(root, 0)
	if err != nil {
		t.Fatalf("FindAudioFiles: %v", err)
	}

	want := []string{
		filepath.Join(root, "a.flac"),
		filepath.Join(root, "b.mp3"),
		filepath.Join(root, "disc2", "c.opus"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestFindAudioFilesHonoursTheLimit(t *testing.T) {
	got, err := FindAudioFiles(audioTree(t), 2)
	if err != nil {
		t.Fatalf("FindAudioFiles: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d files, want 2", len(got))
	}
}

func TestFindAudioFilesOnAFolderWithNoAudio(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := FindAudioFiles(root, 0)
	if err != nil {
		t.Fatalf("FindAudioFiles: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want nothing", got)
	}
}

func TestFindAudioFilesReportsAMissingFolder(t *testing.T) {
	if _, err := FindAudioFiles(filepath.Join(t.TempDir(), "gone"), 0); err == nil {
		t.Fatal("walking a folder that does not exist reported no error")
	}
}
