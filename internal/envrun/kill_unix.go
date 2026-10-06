//go:build !windows

package envrun

import (
	"os/exec"
	"syscall"
)

// NewProcessGroup puts cmd in its own process group, so KillTree can end
// it and everything it started with one signal.
func NewProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// KillTree ends cmd's process group: the process and the children it
// spawned (a shell per Bash call, a test runner, whatever those started).
// Killing the process alone would leave them running and holding the pipes
// jig reads.
func KillTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
