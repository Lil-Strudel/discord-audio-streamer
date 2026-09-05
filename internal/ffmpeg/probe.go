package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Metadata is what we can learn about a file without decoding it.
type Metadata struct {
	Path     string        `json:"path"`
	Title    string        `json:"title"`
	Artist   string        `json:"artist"`
	Duration time.Duration `json:"-"`
	Codec    string        `json:"codec"`
}

// DisplayName is the best label available for the track.
func (m Metadata) DisplayName() string {
	switch {
	case m.Artist != "" && m.Title != "":
		return m.Artist + " — " + m.Title
	case m.Title != "":
		return m.Title
	default:
		return filepath.Base(m.Path)
	}
}

// ErrNotAudio is returned when ffmpeg could not recognise the input at all.
var ErrNotAudio = errors.New("no audio stream recognised")

// probeTimeout bounds how long reading a file's header may take.
const probeTimeout = 15 * time.Second

// Probe reads a file's header without decoding it.
//
// Asking ffmpeg for an input with no output makes it print the header and exit
// with an error, which is a cheap way to get duration and tags. Doing it this
// way avoids bundling ffprobe, which would nearly double the size of the
// release for information ffmpeg already prints.
func Probe(ctx context.Context, path string) (Metadata, error) {
	bin, err := Resolve()
	if err != nil {
		return Metadata{}, err
	}

	// ffmpeg prints the header it read through its log, which is stderr.
	_, lines, err := collect(ctx, bin,
		[]string{"-hide_banner", "-nostdin", "-i", path}, probeTimeout)
	if err != nil {
		return Metadata{}, fmt.Errorf("cannot read %s: %w", filepath.Base(path), err)
	}

	meta, ok := parseProbeOutput(path, lines)
	if !ok {
		if detail := strings.Join(lines, "; "); detail != "" {
			return Metadata{}, fmt.Errorf("cannot read %s: %w: %s", filepath.Base(path), ErrNotAudio, detail)
		}
		return Metadata{}, fmt.Errorf("cannot read %s: %w", filepath.Base(path), ErrNotAudio)
	}
	return meta, nil
}

var (
	inputRe    = regexp.MustCompile(`^Input #\d+`)
	durationRe = regexp.MustCompile(`Duration:\s*(\d+):(\d\d):(\d\d)\.(\d+)`)
	audioRe    = regexp.MustCompile(`Stream #\d+:\d+.*: Audio: ([^\s,(]+)`)
	tagRe      = regexp.MustCompile(`^(\w[\w-]*)\s+:\s*(.+?)$`)
)

// parseProbeOutput reads ffmpeg's header dump. It reports false if ffmpeg never
// managed to open the input, which is how an unsupported or corrupt file is
// distinguished from one that simply carries no tags.
func parseProbeOutput(path string, lines []string) (Metadata, bool) {
	meta := Metadata{Path: path}
	var sawInput, sawAudio bool

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if inputRe.MatchString(line) {
			sawInput = true
			continue
		}
		if m := durationRe.FindStringSubmatch(line); m != nil {
			meta.Duration = parseDuration(m)
			continue
		}
		if m := audioRe.FindStringSubmatch(line); m != nil {
			sawAudio = true
			if meta.Codec == "" {
				meta.Codec = m[1]
			}
			continue
		}
		if m := tagRe.FindStringSubmatch(line); m != nil {
			switch strings.ToLower(m[1]) {
			case "title":
				if meta.Title == "" {
					meta.Title = strings.TrimSpace(m[2])
				}
			case "artist":
				if meta.Artist == "" {
					meta.Artist = strings.TrimSpace(m[2])
				}
			}
		}
	}

	return meta, sawInput && sawAudio
}

func parseDuration(m []string) time.Duration {
	hours, _ := strconv.Atoi(m[1])
	minutes, _ := strconv.Atoi(m[2])
	seconds, _ := strconv.Atoi(m[3])

	// The fractional field is however many digits ffmpeg chose to print, so it
	// is scaled by its own width rather than assumed to be hundredths.
	frac, _ := strconv.Atoi(m[4])
	fracScale := time.Second
	for range len(m[4]) {
		fracScale /= 10
	}

	return time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second +
		time.Duration(frac)*fracScale
}
