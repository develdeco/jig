package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/gitx"
)

// askedOf counts the git processes of argv that run verb.
func askedOf(argv [][]string, verb string) int {
	n := 0
	for _, a := range argv {
		for _, word := range a {
			if word == "-c" || word == "git" || word == "maintenance.auto=false" {
				continue
			}
			if word == verb {
				n++
			}
			break
		}
	}
	return n
}

// overlapsRig is three tickets whose branches change f.txt (two its top, one its
// bottom), in a clone, and a facts file under a jig home: three pairs share it.
func overlapsRig(t *testing.T) (conflictRig, string) {
	t.Helper()
	r := newConflictRig(t, "T-1", "T-2", "T-3")
	r.branch("T-1-a", topChanged)
	r.branch("T-2-b", topChangedAlt)
	r.branch("T-3-c", bottomChanged)
	return r, filepath.Join(t.TempDir(), "cache", "commit-facts.txt")
}

// scanned is one `jig next` read of the overlaps, inside a scope the facts file
// is kept in.
func scanned(t *testing.T, r conflictRig, facts string) (rows []nextOverlap, diffs, merges int) {
	t.Helper()
	end := gitx.BeginScope(facts, r.dir)
	argv := gittest.GitArgv(t, func() { rows = findOverlaps(r.st, r.cfg, r.mp, nil) })
	end()
	return rows, askedOf(argv, "diff"), askedOf(argv, "merge-tree")
}

// copyFile copies a file, as another process finds it.
func copyFile(t *testing.T, from string) string {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(t.TempDir(), "commit-facts.txt")
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return to
}

// TestOverlapsAreKeptByTipAndAskGitOncePerPair: a first read asks git for the
// files of each branch and the merge of each pair that shares one; another
// process reading the same tips starts no diff and no merge. Not parallel: it
// counts the process's git.
func TestOverlapsAreKeptByTipAndAskGitOncePerPair(t *testing.T) {
	r, facts := overlapsRig(t)
	rows, diffs, merges := scanned(t, r, facts)
	if len(rows) != 1 || rows[0].File != "f.txt" || rows[0].Conflict != pairConflict || len(rows[0].Tickets) != 3 {
		t.Fatalf("rows = %+v, want f.txt in conflict", rows)
	}
	if diffs != 3 || merges != 3 {
		t.Fatalf("a first read asked %d diffs and %d merges, want 3 and 3", diffs, merges)
	}

	// Another process: nothing is held in memory, the file is read.
	again := copyFile(t, facts)
	rows2, diffs, merges := scanned(t, r, again)
	if diffs != 0 || merges != 0 {
		t.Errorf("a read of the same tips asked %d diffs and %d merges, want none", diffs, merges)
	}
	if !reflect.DeepEqual(rows, rows2) {
		t.Errorf("the kept read differs:\n%+v\n%+v", rows2, rows)
	}
}

// TestAMovedTipAsksAgain: a branch that moved is a new key, so its files and the
// merges it is in are asked again and nothing stale is read; the other branches
// are not asked about.
func TestAMovedTipAsksAgain(t *testing.T) {
	r, facts := overlapsRig(t)
	scanned(t, r, facts)

	// T-2's branch gets a second commit that changes the bottom, as T-3's does.
	gitIn(t, r.dir, "switch", "T-2-b")
	r.write("1\n2\n3\n4\n5\n6\n7\n8\n9\nBOTTOM\n")
	gitIn(t, r.dir, "commit", "-am", "moved")
	gitIn(t, r.dir, "switch", "main")

	again := copyFile(t, facts)
	rows, diffs, merges := scanned(t, r, again)
	// The one diff is the moved branch's, the merges are the two pairs it is in.
	if diffs != 1 || merges != 2 {
		t.Errorf("after one tip moved, a read asked %d diffs and %d merges, want 1 and 2", diffs, merges)
	}
	uncached := findOverlaps(r.st, r.cfg, r.mp, nil)
	if !reflect.DeepEqual(rows, uncached) {
		t.Errorf("the read after a move is not what git says now:\n%+v\n%+v", rows, uncached)
	}
}

// TestNextOverlapsEqualAnUncachedRead: with the facts kept, cold and warm, the
// rows are the rows of a read with no scope, and a facts file that cannot be
// written is a read like any other.
func TestNextOverlapsEqualAnUncachedRead(t *testing.T) {
	r, facts := overlapsRig(t)
	want := findOverlaps(r.st, r.cfg, r.mp, nil)
	cold, _, _ := scanned(t, r, facts)
	warm, _, _ := scanned(t, r, copyFile(t, facts))
	if !reflect.DeepEqual(cold, want) || !reflect.DeepEqual(warm, want) {
		t.Errorf("rows differ:\nuncached %+v\ncold     %+v\nwarm     %+v", want, cold, warm)
	}

	// The facts file is a directory: nothing can be read or written there.
	blocked := filepath.Join(t.TempDir(), "commit-facts.txt")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	got, diffs, merges := scanned(t, r, blocked)
	if !reflect.DeepEqual(got, want) || diffs != 3 || merges != 3 {
		t.Errorf("with a facts file that cannot be used: %d diffs, %d merges, rows %+v", diffs, merges, got)
	}
}
