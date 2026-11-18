package verify

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/phase"
)

// TestTruthReadsAnOldLedgerWithSpawnedLines: a journal and ledger an old worker
// wrote (a pending line, a spawned line with a pid that is long gone) still
// read. The commit has no recorded result, whatever the pid says, and the
// ledger lines still parse and merge.
func TestTruthReadsAnOldLedgerWithSpawnedLines(t *testing.T) {
	t.Parallel()
	const sha = "abcdef0123456789abcdef0123456789abcdef01"
	home := t.TempDir()
	spawned := journal.Line{Ticket: "T-1", Slice: "a", Event: journal.EventVerifySpawned, Commit: sha, PID: 1 << 30}
	if err := ledger.Append(home, spawned); err != nil {
		t.Fatal(err)
	}
	var seen int
	if err := ledger.ReadBackward(home, 1<<20, func(l journal.Line) bool {
		if l.Event == journal.EventVerifySpawned && l.Commit == sha && l.PID == 1<<30 {
			seen++
		}
		return true
	}); err != nil || seen != 1 {
		t.Fatalf("the spawned line no longer reads: %d, %v", seen, err)
	}

	truth := Truth{
		Ticket:      "T-1",
		Slices:      []string{"a"},
		Lines:       []journal.Line{{Event: journal.EventVerifyPending, Slice: "a", Commit: sha, Attempt: 1, TS: "2026-01-01T00:00:00Z"}, spawned},
		Commits:     []phase.CommitRef{{SHA: sha, Slice: "a"}},
		BranchKnown: true,
	}
	rows := truth.Derive()
	if len(rows) != 1 || rows[0].State != phase.VerifyNoResult || rows[0].Clear != "jig verify T-1 --slice a" {
		t.Fatalf("rows = %+v, want one commit with no recorded result", rows)
	}
	truth.BranchKnown, truth.Commits = false, nil
	if rows := truth.Derive(); len(rows) != 0 {
		t.Fatalf("with no branch, an old pending line names a verification: %+v", rows)
	}
}
