//go:build windows

package main

import (
	"os"
	"os/exec"
)

// reexecSelf spawns the freshly installed binary as a child process, waits
// for it, and exits with its status. Windows has no true exec() syscall, so
// unlike Unix this keeps the parent process alive until the child finishes.
func reexecSelf(execPath string, args []string, env []string) error {
	cmd := exec.Command(execPath, args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return err
	}
	err := cmd.Wait()
	if exitErr, ok := err.(*exec.ExitError); ok {
		os.Exit(exitErr.ExitCode())
	}
	if err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
