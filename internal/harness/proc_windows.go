//go:build windows

package harness

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {
	// On Windows, exec.CommandContext kills the process directly on cancel.
}
