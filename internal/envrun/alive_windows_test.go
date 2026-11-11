//go:build windows

package envrun

import "golang.org/x/sys/windows"

// stillActive is the exit code of a process that has not exited.
const stillActive = 259

// pidAlive reports whether pid names a running process.
func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) != nil || code == stillActive
}
