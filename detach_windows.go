//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// detachProcess lets cmd outlive px0 and keeps it from opening a console window.
func detachProcess(cmd *exec.Cmd) {
	const createNoWindow, createNewProcessGroup = 0x08000000, 0x00000200
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createNewProcessGroup}
}
