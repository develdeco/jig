//go:build !windows

package e2e

import (
	"os"
	"syscall"
)

// stopCases are the ways a test ends a running jig process on this OS:
// the signals a builder's shell, a closing terminal or a harness send. A
// SIGKILL cannot be caught, and a process group does not die with its
// leader's parent, so jig does not cover it (the limit its gate help states);
// that case is listed to say so, and skipped.
var stopCases = []stopCase{
	{name: "SIGINT", stop: func(p *os.Process) error { return p.Signal(os.Interrupt) }},
	{name: "SIGTERM", stop: func(p *os.Process) error { return p.Signal(syscall.SIGTERM) }},
	{name: "SIGHUP", stop: func(p *os.Process) error { return p.Signal(syscall.SIGHUP) }},
	{name: "SIGKILL", skip: "a SIGKILL of jig cannot be caught, and the oracle's process group outlives its parent: not covered on Unix (see jig gate --help)"},
}
