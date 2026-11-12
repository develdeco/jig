// Package workerlive tells whether a process id still names the worker that
// was started, rather than nothing or a process that took the id over.
package workerlive

import "time"

// slack is how far after notBefore a process may have been created and still
// count as the worker: the clock that stamps the line and the one that stamps
// the process do not agree to the second.
const slack = 2 * time.Second

// Alive reports whether pid names a running process created no later than
// notBefore plus a little slack. A worker is older than the line that records
// its pid, so a process created after that line is one that reused the id
// once the worker was gone, and is not the worker. A process that exists but
// whose creation time cannot be read counts as alive: a running worker must
// never be called gone.
func Alive(pid int, notBefore time.Time) bool {
	if pid <= 0 {
		return false
	}
	start, exists, known := processStart(pid)
	if !exists {
		return false
	}
	if !known {
		return true
	}
	return !start.After(notBefore.Add(slack))
}
