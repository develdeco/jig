//go:build !windows

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
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

// startedBy reports whether pid names a running process that was started no
// later than t: ps gives the start to the second, so two seconds' slack. A
// process ps cannot tell about is not taken for the one recorded.
func startedBy(pid int, t time.Time) bool {
	if !pidAlive(pid) {
		return false
	}
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	started, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.Join(strings.Fields(string(out)), " "), time.Local)
	if err != nil {
		return false
	}
	return !started.After(t.Add(2 * time.Second))
}
