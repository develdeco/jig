//go:build !windows

package main

import "testing"

// lockFileShared has no portable non-Windows equivalent; callers skip first.
func lockFileShared(t *testing.T, path string) func() {
	t.Helper()
	t.Skip("lockFileShared: Windows only")
	return func() {}
}
