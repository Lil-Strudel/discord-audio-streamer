//go:build !windows

package main

import "os/exec"

// openInFileManager shows a directory in the desktop's file manager.
func openInFileManager(dir string) error {
	cmd := exec.Command("xdg-open", dir)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
