//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// isTerminalFile reports whether f is a real terminal, via a termios ioctl
// (TCGETS on linux, TIOCGETA elsewhere in this build group; see
// tty_termios_request_*.go). A pipe, a regular file, and the null device
// all fail the ioctl.
func isTerminalFile(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), termiosRequest)
	return err == nil
}
