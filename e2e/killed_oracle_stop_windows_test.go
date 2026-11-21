//go:build windows

package e2e

import "os"

// stopCases are the ways a test ends a running jig process on this OS:
// TerminateProcess, the one stop Windows has for a process that does not
// cooperate, and the one a kill-on-close job object must survive.
var stopCases = []stopCase{
	{name: "terminated", stop: func(p *os.Process) error { return p.Kill() }},
}
