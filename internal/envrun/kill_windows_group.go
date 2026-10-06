//go:build windows

package envrun

import "os/exec"

// NewProcessGroup is a no-op on Windows: a child is not put in a killable
// group of its own here, so KillTree walks the process tree instead.
func NewProcessGroup(cmd *exec.Cmd) {}
