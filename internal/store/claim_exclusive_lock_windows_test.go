//go:build windows

package store

import (
	"testing"

	"golang.org/x/sys/windows"
)

// lockFileExclusive opens path sharing reads but denying writes
// (dwShareMode FILE_SHARE_READ), so git can still `add` or `commit` it (a
// read) but any write to it - including `git restore`'s overwrite of the
// working tree - fails with a sharing violation, until the returned func
// closes the handle. Used to force a real, deterministic git failure inside
// undoClaim, rather than one assembled by touching git's own internals.
func lockFileExclusive(t *testing.T, path string) func() {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("lockFileExclusive %s: %v", path, err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("lockFileExclusive %s: CreateFile: %v", path, err)
	}
	return func() {
		if err := windows.CloseHandle(h); err != nil {
			t.Errorf("lockFileExclusive %s: CloseHandle: %v", path, err)
		}
	}
}
