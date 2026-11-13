//go:build linux

package workerlive

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"
)

// clockTicks is the unit of the start time /proc/<pid>/stat reports. It is
// 100 on every Linux ABI Go targets that matters, and reading sysconf would
// need cgo.
const clockTicks = 100

// processStart reports whether pid is a running process and, when it can be
// read, when it was created: its start time in ticks after boot, from
// /proc/<pid>/stat, plus the boot time in /proc/stat.
func processStart(pid int) (start time.Time, exists, known bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return time.Time{}, !errors.Is(err, fs.ErrNotExist), false
	}
	// The command name is in parentheses and may hold spaces and parens:
	// the fields that matter follow the last closing one.
	i := bytes.LastIndexByte(raw, ')')
	if i < 0 {
		return time.Time{}, true, false
	}
	f := strings.Fields(string(raw[i+1:]))
	// f[0] is the state (field 3); the start time is field 22.
	if len(f) < 20 {
		return time.Time{}, true, false
	}
	if f[0] == "Z" || f[0] == "X" {
		return time.Time{}, false, false
	}
	ticks, err := strconv.ParseInt(f[19], 10, 64)
	if err != nil {
		return time.Time{}, true, false
	}
	boot, ok := bootTime()
	if !ok {
		return time.Time{}, true, false
	}
	return boot.Add(time.Duration(ticks) * time.Second / clockTicks), true, true
}

// bootTime is the machine's boot time, the btime line of /proc/stat.
func bootTime() (time.Time, bool) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return time.Time{}, false
			}
			return time.Unix(secs, 0), true
		}
	}
	return time.Time{}, false
}
