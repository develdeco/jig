//go:build windows

package verifydeliver

import (
	"strings"
	"syscall"
)

// shortenQuotedPath works around a real interaction bug between Go's
// os/exec Windows argument escaping and cmd.exe's own command-tail
// parsing: a command string built as `"<absolute path with spaces>" args`
// (exactly what a quoted @GO/@ENVTOOL substitution produces) gets mangled
// when handed to `cmd /C` through exec.Command, because exec.Command
// re-escapes the whole string as a single MSVCRT-style argument instead of
// leaving cmd.exe's own quoting alone. envrun.Shell is implemented and
// owned elsewhere and is not touched here; instead, a leading quoted
// absolute path is resolved to its Windows short (8.3) form, which
// contains no spaces and needs no quoting at all, sidestepping the bug
// entirely. It is a no-op when the command has no leading quoted path or
// the path cannot be resolved.
func shortenQuotedPath(cmd string) string {
	if len(cmd) == 0 || cmd[0] != '"' {
		return cmd
	}
	end := strings.IndexByte(cmd[1:], '"')
	if end < 0 {
		return cmd
	}
	end++ // index of the closing quote within cmd
	quoted := cmd[1:end]
	rest := cmd[end+1:]

	longPtr, err := syscall.UTF16PtrFromString(quoted)
	if err != nil {
		return cmd
	}
	buf := make([]uint16, 300)
	n, err := syscall.GetShortPathName(longPtr, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return cmd
	}
	short := syscall.UTF16ToString(buf[:n])
	return short + rest
}
