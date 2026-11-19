package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestKeepsStoreReferenceNoteOnlyWhenARoundHasOne: a round whose change cites
// an id of the store is noted with a count and a table naming the file and
// line, both in the report a closed round prints and in the table printed when
// a round opens; a round that cites nothing prints neither.
func TestKeepsStoreReferenceNoteOnlyWhenARoundHasOne(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	refs := []verifydeliver.StoreRef{{Repo: "fixture-repo", File: "notes.txt", Line: 2, ID: fx.Ticket}}

	var out bytes.Buffer
	printGateReport(&out, st, fx.Ticket, verifydeliver.GateReport{Round: 1, Verdict: "clean", StoreRefs: refs})
	for _, want := range []string{"verdict: clean", "store_references: 1", "store_references[1]", "notes.txt:2", fx.Ticket} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the closed round's report lacks %q:\n%s", want, out.String())
		}
	}

	opened := strings.Join(storeRefBlocks(refs), "\n")
	for _, want := range []string{"store_references[1]", "notes.txt:2", fx.Ticket} {
		if !strings.Contains(opened, want) {
			t.Errorf("the table printed when a round opens lacks %q:\n%s", want, opened)
		}
	}

	var none bytes.Buffer
	printGateReport(&none, st, fx.Ticket, verifydeliver.GateReport{Round: 1, Verdict: "clean"})
	if strings.Contains(none.String(), "store_references") {
		t.Errorf("a round that cites nothing of the store prints a note:\n%s", none.String())
	}
	if got := storeRefBlocks(nil); len(got) != 0 {
		t.Errorf("a round that cites nothing prints the opening table %v", got)
	}
}
