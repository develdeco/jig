//go:build !windows

package session

import (
	"os/exec"
	"syscall"
)

// newProcessGroup puts cmd in its own process group, so killTree can end
// the session and everything it started with one signal.
func newProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree ends cmd's process group: the CLI and the children it spawned
// (a shell per Bash call, a test runner, whatever those started). Killing
// the CLI alone would leave them running and holding the pipes jig reads.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
