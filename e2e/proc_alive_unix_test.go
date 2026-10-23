//go:build !windows

package e2e

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// pidAlive reports whether pid names a running process, for a test that waits
// for a detached worker to exit. Signal 0 sends nothing; the kernel only
// reports whether the process exists.
//
// A zombie counts as gone: it has already exited and lingers only until
// whoever inherited it reaps it, and signal 0 still finds it until then. On
// Linux /proc says which state the process is in; elsewhere signal 0 is the
// only probe.
func pidAlive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	if runtime.GOOS != "linux" {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		// A process reaped between the signal and the read leaves no entry.
		return !os.IsNotExist(err) && !errors.Is(err, syscall.ESRCH)
	}
	// The state field follows the command name, which is parenthesized and
	// may itself hold spaces or parentheses, so find the last ')'.
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return true
	}
	switch s[i+2] {
	case 'Z', 'X':
		return false
	}
	return true
}
