package ffmpeg

import (
	"fmt"
	"sync"
)

var (
	resolveOnce sync.Once
	resolvedBin string
	resolveErr  error
)

// Resolve returns the path to the ffmpeg binary to run.
//
// Release builds carry ffmpeg inside the executable and unpack it on first use;
// development builds take it from PATH. Which of the two applies is chosen by
// the embedffmpeg build tag, so a developer never has to stage a 90 MB asset to
// build the project.
func Resolve() (string, error) {
	resolveOnce.Do(func() {
		resolvedBin, resolveErr = resolve()
	})
	return resolvedBin, resolveErr
}

// ErrNotFound is returned when no usable ffmpeg binary is available.
type ErrNotFound struct{ Reason string }

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("ffmpeg is unavailable: %s", e.Reason)
}
