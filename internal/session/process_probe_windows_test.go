//go:build windows

package session

import (
	"os/exec"
	"strconv"
	"strings"
)

// processAlive reports whether pid names a running process, for a test that
// needs to prove a process this package's kill path targeted is actually
// gone. os.Process offers no liveness check on Windows - Signal only
// supports os.Kill - so this asks tasklist, which works for any pid, not
// only one this test binary started itself (killTree walks the tree by pid,
// not by the child accounting os/exec keeps).
func processAlive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), `"`+strconv.Itoa(pid)+`"`)
}
