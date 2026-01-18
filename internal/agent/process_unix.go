//go:build unix

package agent

import (
	"os/exec"
	"syscall"
)

// setupProcessGroup configures the command to run in its own process group.
// This ensures that signals (like SIGINT from Ctrl-C) are properly propagated
// to the entire process tree, allowing clean shutdown of CLI subprocesses.
func setupProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup terminates the process group associated with the command.
// This ensures all child processes are terminated, not just the parent process.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		// Kill the entire process group by sending signal to negative PID
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}
