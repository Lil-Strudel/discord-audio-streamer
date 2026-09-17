//go:build !windows

package subproc

import "os/exec"

// Hide is a no-op outside Windows.
func Hide(*exec.Cmd) {}
