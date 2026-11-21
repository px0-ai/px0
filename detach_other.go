//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachProcess lets cmd outlive px0 and its terminal.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
