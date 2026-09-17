package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/Lil-Strudel/discord-audio-streamer/internal/subproc"
)

// stderrTailLines is how much of yt-dlp's stderr to keep for a failure message.
const stderrTailLines = 12

// maxLineBytes bounds one line of streamed JSON. The per-entry objects this
// package asks for are a few hundred bytes; this is large enough that a title
// full of unusual characters cannot trip it and small enough to be a bound.
const maxLineBytes = 1 << 20

// baseArgs are passed to every invocation.
//
// --ignore-config is the important one. yt-dlp reads a configuration file from
// the user's home, and a perfectly reasonable one that sets --extract-audio, an
// output template or a default format would silently change the shape of what
// this package parses. The app asks for exactly what it needs and nothing else.
func baseArgs() []string {
	args := []string{
		"--ignore-config",
		"--no-warnings",
		"--no-progress",
		"--no-color",
		"--socket-timeout", "15",
		"--retries", "3",
	}
	if dir := cacheDir(); dir != "" {
		args = append(args, "--cache-dir", dir)
	}
	return args
}

// urlArgs terminates the option list before the URL.
//
// There is no shell here — exec.Command passes argv straight through — so the
// only way a URL could be read as an option is by starting with a dash, which
// "--" settles. The URL has already been through ParseLink by this point in any
// case, which is the real guard.
func urlArgs(url string) []string { return []string{"--", url} }

// run executes yt-dlp and returns everything it wrote to stdout.
func run(ctx context.Context, args []string) ([]byte, error) {
	var out bytes.Buffer
	err := stream(ctx, args, func(r io.Reader) error {
		_, err := io.Copy(&out, r)
		return err
	})
	return out.Bytes(), err
}

// runLines executes yt-dlp and hands each line of stdout to onLine as it
// arrives. Returning false from onLine stops reading and terminates yt-dlp.
//
// Streaming rather than collecting matters for a playlist: a long one takes
// tens of seconds to enumerate, and this is what lets the queue fill in as the
// entries arrive instead of appearing all at once at the end. It is also how a
// cap is enforced — yt-dlp is stopped at the limit rather than asked for
// thousands of entries that would then be discarded.
func runLines(ctx context.Context, args []string, onLine func([]byte) bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var stopped bool
	err := stream(ctx, args, func(r io.Reader) error {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			if !onLine(line) {
				// Stopping early is a normal outcome, not a failure. Cancelling
				// here is what kills yt-dlp part-way through a long playlist.
				stopped = true
				cancel()
				return nil
			}
		}
		return scanner.Err()
	})

	if stopped {
		return nil
	}
	return err
}

// stream runs yt-dlp, gives consume its stdout, and turns a non-zero exit into
// an error carrying whatever yt-dlp said on stderr.
func stream(ctx context.Context, args []string, consume func(io.Reader) error) error {
	bin, err := Resolve()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, bin, append(baseArgs(), args...)...)
	subproc.Hide(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("yt-dlp stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("yt-dlp stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start yt-dlp: %w", err)
	}

	// stderr is drained on its own goroutine so it cannot fill its pipe and
	// wedge the process while we are reading stdout.
	var (
		mu    sync.Mutex
		lines []string
		done  = make(chan struct{})
	)
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 4*1024), maxLineBytes)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			mu.Lock()
			lines = append(lines, line)
			if len(lines) > stderrTailLines {
				lines = lines[len(lines)-stderrTailLines:]
			}
			mu.Unlock()
		}
	}()

	consumeErr := consume(stdout)

	// Drain anything left so yt-dlp is never blocked writing into a pipe we
	// have stopped reading, then let the stderr scanner finish before Wait
	// closes that pipe under it.
	_, _ = io.Copy(io.Discard, stdout)
	<-done
	waitErr := cmd.Wait()

	mu.Lock()
	tail := append([]string(nil), lines...)
	mu.Unlock()

	if consumeErr != nil {
		return consumeErr
	}
	if waitErr != nil {
		if ctxErr := ctx.Err(); errors.Is(ctxErr, context.DeadlineExceeded) {
			return fmt.Errorf("yt-dlp timed out: %w", ctxErr)
		}
		return errorFromStderr(tail, waitErr)
	}
	return nil
}

// errorFromStderr turns a non-zero exit into something worth showing a user.
//
// yt-dlp reports the actual reason on stderr — "Video unavailable", "Private
// video", "Sign in to confirm your age" — and prefixes it with ERROR: and the
// extractor and video id. The exit status alone says nothing, so the message is
// dug out and the prefix stripped.
func errorFromStderr(lines []string, fallback error) error {
	for _, line := range lines {
		rest, ok := strings.CutPrefix(line, "ERROR:")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)

		// What remains is usually "[youtube] <id>: the real message".
		if strings.HasPrefix(rest, "[") {
			if _, after, found := strings.Cut(rest, ": "); found {
				rest = after
			}
		}
		if rest = strings.TrimSpace(rest); rest != "" {
			return errors.New(rest)
		}
	}

	if tail := strings.Join(lines, "; "); tail != "" {
		return fmt.Errorf("%w: %s", fallback, tail)
	}
	return fallback
}
