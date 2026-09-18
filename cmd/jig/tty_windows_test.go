//go:build windows

package main

import (
	"os"
	"testing"
)

// TestStdinIsTerminalRealConsole guards the NM1 regression class: round 1's
// stdinIsTerminal (a Stat-mode heuristic comparing against os.DevNull) read
// a real Windows console as non-terminal, disabling interactive triage on
// this project's own development platform, and nothing in the suite caught
// it - every existing stdinIsTerminal test asserts a negative (non-file,
// regular file, the null device, a pipe), so a mutant that always returns
// false passes them all. This opens the process's own console input
// (CONIN$) and asserts stdinIsTerminal is true for it. Some CI runners have
// no console attached; the test skips rather than fails when the open
// itself fails, since that means there is nothing here to check.
func TestStdinIsTerminalRealConsole(t *testing.T) {
	con, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("CONIN$ unavailable (no console attached to this process): %v", err)
	}
	defer con.Close()
	if !stdinIsTerminal(con) {
		t.Fatal("stdinIsTerminal(CONIN$) = false, want true: a real console must be detected as a terminal")
	}
}
