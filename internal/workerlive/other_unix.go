//go:build !windows && !linux

package workerlive

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// processStart reports whether pid is a running process and, when it can be
// read, when it was created: kill with signal 0 for existence (a permission
// error means it is there) and ps for the start time.
func processStart(pid int) (start time.Time, exists, known bool) {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return time.Time{}, false, false
	}
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		// ps exits non-zero when the process is gone by now.
		if _, ok := err.(*exec.ExitError); ok {
			return time.Time{}, false, false
		}
		return time.Time{}, true, false
	}
	// lstart is "Mon Jan  2 15:04:05 2006" in local time.
	t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.TrimSpace(string(out)), time.Local)
	if err != nil {
		return time.Time{}, true, false
	}
	return t, true, true
}
