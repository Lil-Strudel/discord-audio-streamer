//go:build !embedffmpeg

package ffmpeg

import "os/exec"

// resolve finds ffmpeg on PATH. This is the development path; see
// locate_embedded.go for what release builds do.
func resolve() (string, error) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", &ErrNotFound{Reason: "not found on PATH, and this build does not bundle it"}
	}
	return path, nil
}
