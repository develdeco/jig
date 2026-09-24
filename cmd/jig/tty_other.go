//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package main

import "os"

// isTerminalFile reports false on any GOOS with no terminal query wired up
// here: the non-interactive path keeps every finding jig can route on its
// own and leaves the rest for a human, which is safe.
func isTerminalFile(f *os.File) bool {
	return false
}
