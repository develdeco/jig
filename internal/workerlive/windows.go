//go:build windows

package workerlive

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code GetExitCodeProcess reports for a process that
// has not ended.
const stillActive = 259

// processStart reports whether pid is a running process and, when it can be
// read, when it was created.
func processStart(pid int) (start time.Time, exists, known bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// No such process is an invalid parameter; anything else (access
		// denied) means it is there and we may not look.
		return time.Time{}, !errors.Is(err, windows.ERROR_INVALID_PARAMETER), false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err == nil && code != stillActive {
		return time.Time{}, false, false
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, true, false
	}
	return time.Unix(0, creation.Nanoseconds()), true, true
}
