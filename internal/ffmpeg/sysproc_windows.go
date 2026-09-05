package ffmpeg

import (
	"os/exec"
	"syscall"
)

// hideConsoleWindow stops ffmpeg from flashing a console window. Without this
// every playback start, seek and device probe pops a black box on screen, since
// the app itself is a GUI binary with no console of its own.
func hideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
