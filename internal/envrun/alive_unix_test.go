//go:build !windows

package envrun

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// pidAlive reports whether pid names a running process. A zombie counts as
// gone: it has exited and lingers only until it is reaped.
func pidAlive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	if runtime.GOOS != "linux" {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	return i < 0 || i+2 >= len(s) || (s[i+2] != 'Z' && s[i+2] != 'X')
}
