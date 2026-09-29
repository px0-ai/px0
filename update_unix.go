//go:build !windows

package main

import "syscall"

// reexecSelf replaces the current process image with execPath, so a
// successful auto-update takes effect within this same invocation.
func reexecSelf(execPath string, args []string, env []string) error {
	argv := append([]string{execPath}, args[1:]...)
	return syscall.Exec(execPath, argv, env)
}
