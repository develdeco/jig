//go:build windows

package store

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// TestAtomicWriteReplacesAFileAReaderHolds: on Windows AtomicWrite replaces
// a file while a reader holds it open as readFile opens it, in one step: the
// held handle goes on reading the old bytes, and the next read sees the new.
func TestAtomicWriteReplacesAFileAReaderHolds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q-001.md")
	if err := AtomicWrite(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	held := os.NewFile(uintptr(h), path)
	defer held.Close()

	if err := AtomicWrite(path, []byte("new")); err != nil {
		t.Fatalf("AtomicWrite over a file a reader holds: %v", err)
	}
	if data, err := readFile(path); err != nil || string(data) != "new" {
		t.Errorf("readFile after the replacement = %q, %v, want new", data, err)
	}
	if data, err := io.ReadAll(held); err != nil || string(data) != "old" {
		t.Errorf("the held handle reads %q, %v, want old", data, err)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".q-001.md.*.tmp")); len(matches) != 0 {
		t.Errorf("temp files left behind: %v", matches)
	}
}
