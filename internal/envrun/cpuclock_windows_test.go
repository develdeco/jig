//go:build windows

package envrun

import (
	"time"

	"golang.org/x/sys/windows"
)

// processCPU is the user plus kernel CPU time this process has used so far.
func processCPU() time.Duration {
	var create, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &create, &exit, &kernel, &user); err != nil {
		return 0
	}
	ticks := func(f windows.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return time.Duration(ticks(kernel)+ticks(user)) * 100
}
