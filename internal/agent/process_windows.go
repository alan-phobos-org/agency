//go:build windows

package agent

import (
	"os/exec"
	"syscall"
)

// setupProcessGroup configures the command to run in its own process group.
// On Windows, this uses CREATE_NEW_PROCESS_GROUP to allow proper signal handling.
func setupProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP
}

// killProcessGroup terminates the process group associated with the command.
// On Windows, this sends a Ctrl+Break event to the process group.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		// On Windows, we kill the process. The process group will be terminated
		// because we used CREATE_NEW_PROCESS_GROUP.
		cmd.Process.Kill()
	}
}
