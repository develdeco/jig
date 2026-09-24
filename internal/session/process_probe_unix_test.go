//go:build !windows

package session

import "syscall"

// processAlive reports whether pid names a running process, for a test that
// needs to prove a process this package's kill path targeted is actually
// gone. Signal 0 sends nothing; the kernel only reports whether the process
// (and this process's permission to signal it, which it has for its own
// descendants) exists.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
