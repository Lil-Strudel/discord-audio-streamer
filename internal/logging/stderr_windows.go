package logging

import (
	"os"

	"golang.org/x/sys/windows"
)

// captureStderr points the process's standard error at the log file.
//
// The Go runtime writes panics, fatal errors and the report for a fault inside
// a C library straight to the standard error handle, which it looks up through
// GetStdHandle on every write. Replacing that handle is therefore enough to
// capture output the runtime produces while dying, which no amount of logging
// from Go code could catch.
func captureStderr(f *os.File) bool {
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd())); err != nil {
		return false
	}
	os.Stderr = f
	return true
}
