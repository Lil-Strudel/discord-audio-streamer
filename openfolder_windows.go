package main

import (
	"os/exec"
	"syscall"
)

// openInFileManager shows a directory in Explorer.
//
// Explorer exits with a non-zero status even when it succeeds, so its error is
// deliberately discarded; the only failure worth reporting would be not finding
// the executable at all.
func openInFileManager(dir string) error {
	cmd := exec.Command("explorer.exe", dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
