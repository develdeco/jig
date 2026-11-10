package main

import (
	"fmt"
	"strings"
	"testing"
)

// TestStatusStoreRefsListsTheRoundsHits: a round whose report records store
// references has a store_references block under the rounds table, one row per
// hit with its round, and a round with none adds no row.
func TestStatusStoreRefsListsTheRoundsHits(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	writeRound(t, fx.StoreDir, fx.Ticket, 1, roundFiles{
		report: "round: 1\nverdict: clean\nstore_refs:\n" +
			"  - {repo: app, file: a.go, line: 3, id: ZQX-7}\n" +
			"  - {repo: lib, file: sub/b.go, line: 12, id: '<store path>'}\n",
	})
	writeRound(t, fx.StoreDir, fx.Ticket, 2, roundFiles{report: "round: 2\nverdict: clean\n"})

	got := block(statusOf(t, fx, st), "store_references[")
	want := []string{
		"store_references[2]{round,repo,file:line,id}:",
		`  1,app,"a.go:3",ZQX-7`,
		`  1,lib,"sub/b.go:12",<store path>`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("store_references block:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestStatusStoreRefsAbsentWithoutAny: rounds that record none, as every round
// an older jig wrote, add no block.
func TestStatusStoreRefsAbsentWithoutAny(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	writeRound(t, fx.StoreDir, fx.Ticket, 1, roundFiles{report: "round: 1\nverdict: clean\n"})
	if out := statusOf(t, fx, st); strings.Contains(out, "store_references") {
		t.Errorf("a ticket whose rounds record no store reference has the block:\n%s", out)
	}
}

// TestStatusStoreRefsSumsUpPastTwentyRows: the block lists 20 rows and then one
// row saying how many more and in how many files.
func TestStatusStoreRefsSumsUpPastTwentyRows(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	var report strings.Builder
	report.WriteString("round: 1\nverdict: clean\nstore_refs:\n")
	for i := 1; i <= 23; i++ {
		file := "a.go"
		if i > 21 {
			file = "b.go"
		}
		fmt.Fprintf(&report, "  - {repo: app, file: %s, line: %d, id: ZQX-7}\n", file, i)
	}
	writeRound(t, fx.StoreDir, fx.Ticket, 1, roundFiles{report: report.String()})

	got := block(statusOf(t, fx, st), "store_references[")
	if len(got) != 22 || got[1] != `  1,app,"a.go:1",ZQX-7` || got[21] != "  and 3 more in 2 files,,," {
		t.Errorf("store_references block has %d lines, want 22:\n%s", len(got), strings.Join(got, "\n"))
	}
}
