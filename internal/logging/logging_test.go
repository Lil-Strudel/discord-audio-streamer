package logging_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/logging"
)

// isolate points the user configuration directory at a temporary one, so the
// tests never touch the real log directory.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)
	return dir
}

func TestSetupWritesToFile(t *testing.T) {
	isolate(t)

	logger, closeLog, err := logging.Setup(false)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	logger.Info("hello from the test")
	closeLog()

	path, err := logging.Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	body := string(raw)
	if !strings.Contains(body, "hello from the test") {
		t.Errorf("log does not contain the message:\n%s", body)
	}
	// The header is what makes a log usable in a bug report.
	if !strings.Contains(body, "starting") {
		t.Errorf("log has no startup header:\n%s", body)
	}
}

// The log that explains a crash belongs to the run before the one the user is
// reporting from, so starting up must preserve it rather than truncate it.
func TestSetupKeepsThePreviousRun(t *testing.T) {
	isolate(t)

	first, closeFirst, err := logging.Setup(false)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	first.Info("first run")
	closeFirst()

	second, closeSecond, err := logging.Setup(false)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	second.Info("second run")
	closeSecond()

	dir, err := logging.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}

	current, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("read current log: %v", err)
	}
	previous, err := os.ReadFile(filepath.Join(dir, "app.previous.log"))
	if err != nil {
		t.Fatalf("read previous log: %v", err)
	}

	if !strings.Contains(string(current), "second run") {
		t.Error("current log does not hold the second run")
	}
	if strings.Contains(string(current), "first run") {
		t.Error("current log still holds the first run; it was not rotated")
	}
	if !strings.Contains(string(previous), "first run") {
		t.Error("previous log does not hold the first run")
	}
}

func TestDirIsUnderTheAppFolder(t *testing.T) {
	root := isolate(t)

	dir, err := logging.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	want := filepath.Join(root, "DiscordAudioStreamer", "logs")
	if dir != want {
		t.Errorf("Dir() = %q, want %q", dir, want)
	}
}
