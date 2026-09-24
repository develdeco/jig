//go:build windows

package session

import "os/exec"

// newProcessGroup is a no-op on Windows: a child is not put in a killable
// group of its own here, so killTree walks the process tree instead.
func newProcessGroup(cmd *exec.Cmd) {}
