//go:build windows

package e2e

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code GetExitCodeProcess reports for a process that
// has not exited.
const stillActive = 259

// pidAlive reports whether pid names a running process, for a test that waits
// for a detached worker to exit. It opens the process for the one query a
// limited handle allows and asks for its exit code: a process that is gone
// cannot be opened, and one that has exited but is still held open by
// someone reports a real exit code.
func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Access denied means the process exists and is not ours to query.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}

// startedBy reports whether pid names a running process that was created no
// later than t, with a second's slack: a process Windows started later under
// a reused pid is a different one. A process that cannot be queried is not
// taken for the one recorded.
func startedBy(pid int, t time.Time) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil || code != stillActive {
		return false
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return false
	}
	return !time.Unix(0, created.Nanoseconds()).After(t.Add(time.Second))
}
