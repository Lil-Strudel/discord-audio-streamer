//go:build !windows

package logging

import "os"

// captureStderr does nothing off Windows: standard error is a real stream when
// the app is run from a terminal, and redirecting it would hide output the
// developer is watching for.
func captureStderr(*os.File) bool { return false }
