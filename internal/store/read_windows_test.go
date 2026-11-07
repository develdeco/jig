//go:build windows

package store

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestAtomicWriteReplacesAFileAReaderHolds: on Windows AtomicWrite replaces
// a file while a reader holds it open as ReadFile opens it, in one step: the
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
	if data, err := ReadFile(path); err != nil || string(data) != "new" {
		t.Errorf("ReadFile after the replacement = %q, %v, want new", data, err)
	}
	if data, err := io.ReadAll(held); err != nil || string(data) != "old" {
		t.Errorf("the held handle reads %q, %v, want old", data, err)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".q-001.md.*.tmp")); len(matches) != 0 {
		t.Errorf("temp files left behind: %v", matches)
	}
}

// TestAtomicWriteWaitsOutAReaderThatDoesNotShareDelete: a reader jig does not
// control (an editor, a virus scanner) opens the file with os.Open, which on
// Windows refuses a rename over it. AtomicWrite retries until the reader lets
// go, and the new bytes land.
func TestAtomicWriteWaitsOutAReaderThatDoesNotShareDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q-002.md")
	if err := AtomicWrite(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(200*time.Millisecond, func() { held.Close() })

	if err := AtomicWrite(path, []byte("new")); err != nil {
		t.Fatalf("AtomicWrite over a file a reader released after 200ms: %v", err)
	}
	if data, err := ReadFile(path); err != nil || string(data) != "new" {
		t.Errorf("ReadFile after the replacement = %q, %v, want new", data, err)
	}
}

// TestAtomicWriteFailsWithThePathPastItsBound: a reader that never lets go
// fails the write after replaceWindow, naming the path, and leaves no temp
// file and the old bytes.
func TestAtomicWriteFailsWithThePathPastItsBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q-003.md")
	if err := AtomicWrite(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	err = AtomicWrite(path, []byte("new"))
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("AtomicWrite over a file held for good = %v, want an error naming %s", err, path)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".q-003.md.*.tmp")); len(matches) != 0 {
		t.Errorf("temp files left behind: %v", matches)
	}
}
