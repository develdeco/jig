package e2e

import (
	"strings"
	"testing"
)

// TestGateNotesAStoreReference: a branch that adds a comment citing an id of
// the store is noted by the gate with the file and line, at the opening of the
// round and again when the reviewer closes it, and in the round's report.yaml;
// the round is clean and every command exits 0.
func TestGateNotesAStoreReference(t *testing.T) {
	t.Parallel()
	b := newBuiltTicket(t, false)

	// The fixture's store is made up: JIG-1 is its ticket.
	b.sha = b.builder.Commit("", "explain the total", map[string]string{
		"notes.txt": "plain line\n// see " + b.fx.Ticket + " for why\n",
	})

	gate := b.openRound(t)
	for _, want := range []string{"store_references[1]", "notes.txt:2", b.fx.Ticket} {
		if !strings.Contains(gate.Stdout, want) {
			t.Errorf("jig gate output lacks %q\nstdout:\n%s", want, gate.Stdout)
		}
	}

	done := b.reviewedClean(t, 1)
	for _, want := range []string{"verdict: clean", "store_references: 1", "notes.txt:2"} {
		if !strings.Contains(done.Stdout, want) {
			t.Errorf("jig review done output lacks %q\nstdout:\n%s", want, done.Stdout)
		}
	}
	refs, _ := b.report(t, "1")["store_refs"].([]any)
	if len(refs) != 1 {
		t.Fatalf("report.yaml store_refs = %v, want one", refs)
	}
	ref, _ := refs[0].(map[string]any)
	if ref["file"] != "notes.txt" || ref["line"] != 2 || ref["id"] != b.fx.Ticket {
		t.Errorf("report.yaml store_refs[0] = %v, want notes.txt line 2 naming %s", ref, b.fx.Ticket)
	}
}
