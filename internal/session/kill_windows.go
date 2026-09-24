//go:build windows

package session

import (
	"os/exec"
	"strconv"
)

// killTree ends cmd's process and everything it started. On Windows a
// session's children (a shell per Bash call, a test runner, the tools it
// spawns) are not in a killable group of their own, and killing the CLI
// alone leaves them running and holding the pipes jig reads. taskkill /T
// walks the tree from the process id instead.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	if err := kill.Run(); err != nil {
		// taskkill is not there, or the tree is already gone: fall back to
		// the process itself so the bound still ends something.
		return cmd.Process.Kill()
	}
	return nil
}
