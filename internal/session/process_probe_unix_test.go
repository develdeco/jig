//go:build !windows

package session

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// processAlive reports whether pid names a running process, for a test that
// needs to prove a process this package's kill path targeted is actually
// gone. Signal 0 sends nothing; the kernel only reports whether the process
// (and this process's permission to signal it, which it has for its own
// descendants) exists.
//
// A zombie counts as gone: it has already been killed and lingers only
// until whoever inherited it reaps it, and signal 0 still finds it until
// then. On Linux /proc says which state the process is in; elsewhere signal
// 0 is the only probe, and the caller's wait covers the reaping.
func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	if runtime.GOOS != "linux" {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return !os.IsNotExist(err)
	}
	// The state field follows the command name, which is parenthesized and
	// may itself contain spaces or parentheses, so find the last ')'.
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
