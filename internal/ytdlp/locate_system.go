//go:build !embedytdlp

package ytdlp

import "os/exec"

// resolve finds yt-dlp on PATH. This is the development path; see
// locate_embedded.go for what release builds do.
func resolve() (string, error) {
	path, err := exec.LookPath("yt-dlp")
	if err != nil {
		return "", &ErrNotFound{Reason: "was not found on PATH, and this build does not bundle it"}
	}
	return path, nil
}
