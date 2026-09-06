// Package logging writes the application log to a file.
//
// A Wails release build on Windows is linked as a GUI binary, so it has no
// console and its standard error goes nowhere. Anything logged there is lost,
// including the runtime's own report when the process dies, which is exactly
// the output needed to explain a crash. Everything therefore goes to a file
// under the user's configuration directory, next to config.json.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// appDir matches the folder used by the config package, so a user looking for
// their data finds the logs beside it.
const appDir = "DiscordAudioStreamer"

const (
	logName  = "app.log"
	prevName = "app.previous.log"
)

// Dir returns the directory holding the log files.
func Dir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate configuration directory: %w", err)
	}
	return filepath.Join(dir, appDir, "logs"), nil
}

// Path returns the file the current run logs to.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, logName), nil
}

// Setup opens the log file and returns a logger writing to it, along with a
// function to close it.
//
// The previous run's log is kept alongside the current one. A crash is usually
// reported after the fact, by which time the app has been started again, so the
// log that matters is the one from the run before this one.
//
// If the file cannot be opened the logger still works, writing to standard
// error, and the error is returned for the caller to surface. Losing logs is
// not a reason to refuse to start.
func Setup(debug bool) (*slog.Logger, func(), error) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	stderrLogger := func() *slog.Logger {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	}

	dir, err := Dir()
	if err != nil {
		return stderrLogger(), func() {}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return stderrLogger(), func() {}, fmt.Errorf("create log directory: %w", err)
	}

	path := filepath.Join(dir, logName)
	// Ignore the error: a missing previous log is the normal first-run case,
	// and failing to rotate is not a reason to run without logging.
	_ = os.Rename(path, filepath.Join(dir, prevName))

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return stderrLogger(), func() {}, fmt.Errorf("open log file: %w", err)
	}

	// Send the runtime's crash output to the same file. Without this a panic,
	// or a fault inside one of the C libraries, leaves nothing behind at all.
	var w io.Writer = f
	if !captureStderr(f) {
		// Standard error is still a real stream, so keep using it as well;
		// this is the development case.
		w = io.MultiWriter(os.Stderr, f)
	}

	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))

	// A header makes it obvious where one run ends and the next begins, and
	// records the details worth having in a bug report.
	logger.Info("starting",
		slog.String("os", runtime.GOOS),
		slog.String("arch", runtime.GOARCH),
		slog.String("go", runtime.Version()),
		slog.Time("at", time.Now()),
		slog.String("log", path),
	)

	return logger, func() { _ = f.Close() }, nil
}
