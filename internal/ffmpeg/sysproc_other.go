//go:build !windows

package ffmpeg

import "os/exec"

// hideConsoleWindow is a no-op outside Windows.
func hideConsoleWindow(*exec.Cmd) {}
