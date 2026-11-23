package main

import (
	"os"
	"strings"
	"testing"
)

// TestAnUntriedMergeIsNotKept: a pair git could not try reads untested, and that
// is no fact about the two commits: the next read tries it again. Not parallel:
// it swaps the merge.
func TestAnUntriedMergeIsNotKept(t *testing.T) {
	r, facts := overlapsRig(t)
	real := testPairWith
	testPairWith = func(dir, a, b string) (string, []string) { return pairUntested, nil }
	rows, _, merges := scanned(t, r, facts)
	testPairWith = real
	if len(rows) != 1 || rows[0].Conflict != pairUntested || merges != 0 {
		t.Fatalf("rows = %+v, %d merges, want an untested row and no merge process", rows, merges)
	}
	if data, err := os.ReadFile(facts); err != nil || strings.Contains(string(data), "merge ") {
		t.Errorf("an untried merge was kept (%v):\n%s", err, data)
	}
	rows, _, _ = scanned(t, r, facts)
	if len(rows) != 1 || rows[0].Conflict != pairConflict {
		t.Errorf("the next read of the same tips says %+v, want the merge tried again", rows)
	}
}
