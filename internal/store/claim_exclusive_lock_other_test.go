//go:build !windows

package store

import "testing"

// lockFileExclusive has no portable non-Windows equivalent (POSIX locks are
// advisory, so they would not make git's own write fail); callers skip
// before ever reaching it off Windows.
func lockFileExclusive(t *testing.T, path string) func() {
	t.Helper()
	t.Skip("lockFileExclusive: Windows only")
	return func() {}
}
