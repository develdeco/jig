//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// isTerminalFile reports whether f is a real Windows console, via
// GetConsoleMode on its handle. A pipe, a regular file, and the null device
// all fail GetConsoleMode.
func isTerminalFile(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}
