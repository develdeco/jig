package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAMissingFileFailsAtOnceUnlessItsWriteIsUnderWay: readFile retries a
// missing file only while an AtomicWrite of it holds a fresh temp file
// beside it, so a file that is simply not there costs no wait, and a
// writer's leftover temp file older than replaceWindow is not a write.
func TestAMissingFileFailsAtOnceUnlessItsWriteIsUnderWay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "q-001.md")

	start := time.Now()
	if _, err := readFile(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("readFile of a missing file = %v, want not found", err)
	}
	if took := time.Since(start); took > replaceWindow/4 {
		t.Errorf("readFile of a missing file took %s, want no wait", took)
	}
	if replacementPending(path) {
		t.Error("a write is pending with no temp file")
	}

	tmp := filepath.Join(dir, tempName("q-001.md"))
	if err := os.WriteFile(tmp, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !replacementPending(path) {
		t.Errorf("no write pending beside a fresh temp file %s", filepath.Base(tmp))
	}
	if replacementPending(filepath.Join(dir, "q-00.md")) {
		t.Error("a temp file of q-001.md counts as a write of q-00.md")
	}
	old := time.Now().Add(-2 * replaceWindow)
	if err := os.Chtimes(tmp, old, old); err != nil {
		t.Fatal(err)
	}
	if replacementPending(path) {
		t.Error("a temp file older than replaceWindow counts as a write under way")
	}
}

// TestAReadDuringAFirstWriteGetsTheFile: a read that finds no file while the
// file's first AtomicWrite is under way waits for it and reads it whole.
func TestAReadDuringAFirstWriteGetsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "q-001.md")
	tmp := filepath.Join(dir, tempName("q-001.md"))
	if err := os.WriteFile(tmp, []byte("whole"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		done <- os.Rename(tmp, path)
	}()
	data, err := readFile(path)
	if err != nil || string(data) != "whole" {
		t.Errorf("readFile during the first write = %q, %v, want the file", data, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
