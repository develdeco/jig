//go:build windows

package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

// lockFileShared opens path sharing reads but denying writes and renames
// until the returned func closes the handle (also run at test cleanup): the
// way a test makes git's restore of that file fail deterministically.
func lockFileShared(t *testing.T, path string) func() {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("lockFileShared %s: %v", path, err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("lockFileShared %s: CreateFile: %v", path, err)
	}
	closed := false
	unlock := func() {
		if !closed {
			closed = true
			_ = windows.CloseHandle(h)
		}
	}
	t.Cleanup(unlock)
	return unlock
}
