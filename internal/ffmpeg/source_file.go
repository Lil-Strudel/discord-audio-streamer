package ffmpeg

import (
	"context"
	"fmt"
	"time"
)

// FileSource decodes an audio file to PCM.
type FileSource struct {
	proc   *Process
	offset time.Duration
}

// OpenFile starts decoding path, beginning at offset.
//
// Seeking is expressed by starting a new decode at a new offset rather than by
// any in-band mechanism, because ffmpeg's stdout is a one-way pipe with no way
// to ask it to jump. Putting -ss before -i also lets ffmpeg seek by index
// instead of decoding and discarding everything up to that point.
func OpenFile(ctx context.Context, path string, offset time.Duration) (*FileSource, error) {
	bin, err := Resolve()
	if err != nil {
		return nil, err
	}

	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if offset > 0 {
		args = append(args, "-ss", formatSeekTime(offset))
	}
	args = append(args, "-i", path)
	args = append(args, pcmOutputArgs()...)

	proc, err := Start(ctx, bin, args, nil)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	return &FileSource{proc: proc, offset: offset}, nil
}

// Read returns decoded PCM.
func (f *FileSource) Read(b []byte) (int, error) { return f.proc.Read(b) }

// Close stops decoding.
func (f *FileSource) Close() error { return f.proc.Close() }

// Wait blocks until decoding finishes.
func (f *FileSource) Wait() error { return f.proc.Wait() }

// Offset is the position in the file this source started from. Playback
// position is this plus however much has been read since.
func (f *FileSource) Offset() time.Duration { return f.offset }

// formatSeekTime renders a seek position for -ss. Seconds with millisecond
// precision are accepted by every ffmpeg build and avoid the ambiguity of the
// HH:MM:SS form.
func formatSeekTime(d time.Duration) string {
	return fmt.Sprintf("%.3f", d.Seconds())
}
