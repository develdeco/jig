package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestTestPair: two branches that change the same lines of a file conflict in
// it; two that change different parts of one file merge cleanly; a ref git
// cannot resolve leaves the pair untested, never an error.
func TestTestPair(t *testing.T) {
	t.Parallel()
	dir := cmdGitRepo(t, filepath.Join(t.TempDir(), "repo"))
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("f.txt", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", "base")
	branch := func(name, body string) {
		gitIn(t, dir, "switch", "-c", name, "main")
		write("f.txt", body)
		gitIn(t, dir, "commit", "-am", name)
	}
	branch("top", "TOP\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	branch("bottom", "1\n2\n3\n4\n5\n6\n7\n8\n9\nBOTTOM\n")
	branch("same-top", "OTHER\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")

	if state, files := testPair(dir, "top", "bottom"); state != pairClean || len(files) != 0 {
		t.Errorf("different parts of one file: %s %v, want clean", state, files)
	}
	if state, files := testPair(dir, "top", "same-top"); state != pairConflict || !reflect.DeepEqual(files, []string{"f.txt"}) {
		t.Errorf("the same lines: %s %v, want a conflict in f.txt", state, files)
	}
	if state, files := testPair(dir, "top", "nosuchref"); state != pairUntested || len(files) != 0 {
		t.Errorf("a bad ref: %s %v, want untested", state, files)
	}
}
