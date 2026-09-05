// Package ffmpeg drives ffmpeg subprocesses that decode files or capture
// devices into the raw PCM the rest of the pipeline consumes.
//
// ffmpeg is used only to decode and capture, never to encode: it is asked for
// signed 16-bit little-endian samples at 48 kHz in stereo, and everything after
// that happens in Go so gain can be applied per frame.
package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// stderrTailLines is how much of ffmpeg's stderr to keep. ffmpeg reports the
// actual reason for a failure there, and the exit status alone is useless for
// telling a user what went wrong.
const stderrTailLines = 12

// Process is a running ffmpeg invocation whose stdout is a stream of raw PCM.
type Process struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	cancel context.CancelFunc

	waitOnce sync.Once
	waitErr  error
	exited   chan struct{}

	// stderrDone is closed once the stderr scanner has consumed the pipe.
	// exec.Cmd.Wait closes that pipe as soon as the process exits, so waiting
	// on this first is what keeps the last lines of ffmpeg's output — which is
	// where it says what went wrong — from being truncated.
	stderrDone chan struct{}

	mu     sync.Mutex
	stderr []string
}

// Start launches bin with args. onStderrLine, if non-nil, is called for each
// line ffmpeg writes to stderr, from a goroutine, and must not block for long.
func Start(ctx context.Context, bin string, args []string, onStderrLine func(string)) (*Process, error) {
	ctx, cancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(ctx, bin, args...)
	hideConsoleWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("ffmpeg stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	p := &Process{
		cmd:        cmd,
		stdout:     stdout,
		cancel:     cancel,
		exited:     make(chan struct{}),
		stderrDone: make(chan struct{}),
	}

	go p.consumeStderr(stderr, onStderrLine)

	return p, nil
}

func (p *Process) consumeStderr(r io.Reader, onLine func(string)) {
	defer close(p.stderrDone)

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		p.mu.Lock()
		p.stderr = append(p.stderr, line)
		if len(p.stderr) > stderrTailLines {
			p.stderr = p.stderr[len(p.stderr)-stderrTailLines:]
		}
		p.mu.Unlock()

		if onLine != nil {
			onLine(line)
		}
	}
}

// Read returns raw PCM bytes from ffmpeg's stdout.
func (p *Process) Read(b []byte) (int, error) {
	return p.stdout.Read(b)
}

// Wait blocks until ffmpeg exits and reports why.
//
// A non-zero exit is annotated with the tail of stderr, because "exit status 1"
// on its own cannot be turned into a message worth showing a user.
func (p *Process) Wait() error {
	p.waitOnce.Do(func() {
		<-p.stderrDone
		err := p.cmd.Wait()
		p.cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			if tail := p.StderrTail(); tail != "" {
				err = fmt.Errorf("%w: %s", err, tail)
			}
		}
		p.waitErr = err
		close(p.exited)
	})
	<-p.exited
	return p.waitErr
}

// Close terminates ffmpeg and waits for it to go away.
//
// Killing rather than signalling is deliberate: ffmpeg is usually blocked
// writing into a pipe nobody is draining any more, and there is no output worth
// flushing.
func (p *Process) Close() error {
	p.cancel()
	_ = p.stdout.Close()
	err := p.Wait()
	if isKilled(err) {
		return nil
	}
	return err
}

// StderrTail returns the last few lines ffmpeg wrote to stderr.
func (p *Process) StderrTail() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.stderr, "; ")
}

// isKilled reports whether err is just the result of us terminating ffmpeg,
// which is the normal way every source shuts down and is not a failure.
func isKilled(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, os.ErrClosed) {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return !exitErr.Exited()
	}
	return false
}

// collect runs a short-lived ffmpeg invocation and returns everything it wrote
// to stdout and stderr.
//
// Both streams are captured because ffmpeg is inconsistent about which it uses:
// -list_devices reports through the log, on stderr, while -sources prints to
// stdout. Both are drained concurrently so neither can fill its pipe and wedge
// the process.
//
// ffmpeg is allowed to exit on its own rather than being killed. For probing
// and device listing its output is the entire result, and killing the process
// discards whatever it had not flushed.
func collect(ctx context.Context, bin string, args []string, timeout time.Duration) (stdout, stderr []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var (
		mu          sync.Mutex
		stderrLines []string
	)

	proc, err := Start(ctx, bin, args, func(line string) {
		mu.Lock()
		stderrLines = append(stderrLines, line)
		mu.Unlock()
	})
	if err != nil {
		return nil, nil, err
	}

	stdoutLines := readLines(proc)
	waitErr := proc.Wait()

	mu.Lock()
	defer mu.Unlock()

	// The exit status is not meaningful for these calls: ffmpeg ends with "no
	// output file" or "dummy is not a device" even when it printed exactly what
	// was asked for. Only report the error when it produced nothing at all.
	if len(stdoutLines) == 0 && len(stderrLines) == 0 && waitErr != nil {
		return nil, nil, waitErr
	}
	return stdoutLines, append([]string(nil), stderrLines...), nil
}

func readLines(r io.Reader) []string {
	var lines []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
