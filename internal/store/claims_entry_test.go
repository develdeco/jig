package store

import (
	"strings"
	"testing"
)

func entryLines(t *testing.T, st *Store) []ClaimLine {
	t.Helper()
	lines, err := st.Claims()
	if err != nil {
		t.Fatal(err)
	}
	var out []ClaimLine
	for _, l := range lines {
		if l.Kind == ClaimEntry {
			out = append(out, l)
		}
	}
	return out
}

// TestReserveEntryIsRefusedForAnEntryAnotherTicketHolds: the loser of a race
// for one item learns whose it is, and the same ticket asking again appends
// nothing.
func TestReserveEntryIsRefusedForAnEntryAnotherTicketHolds(t *testing.T) {
	t.Parallel()
	alice, _, remote := newWritersStore(t)
	bob, _ := newWriter(t, remote, "bob")
	if err := bob.Sync(); err != nil {
		t.Fatal(err)
	}
	e := TrackerEntry{Provider: ProviderGitHub, Scope: "o/r", ID: "48"}
	if err := alice.ReserveEntry(e, "JIG-5"); err != nil {
		t.Fatalf("alice ReserveEntry: %v", err)
	}
	lines := entryLines(t, alice)
	if len(lines) != 1 || lines[0].Entry != "github/o/r/48" || lines[0].Ticket != "JIG-5" || lines[0].Writer != "alice" {
		t.Fatalf("entry lines = %+v, want one for github/o/r/48 and JIG-5 by alice", lines)
	}
	if err := alice.ReserveEntry(e, "JIG-5"); err != nil {
		t.Fatalf("alice again: %v", err)
	}
	if n := len(entryLines(t, alice)); n != 1 {
		t.Errorf("the same ticket reserving again left %d lines, want 1", n)
	}
	// Bob's view lacks the line: the push is rejected, the fetch shows it.
	err := bob.ReserveEntry(e, "JIG-6")
	if axiCode(err) != "TRACKER_ENTRY_TAKEN" || !strings.Contains(err.Error(), "JIG-5") {
		t.Fatalf("bob ReserveEntry = %v, want TRACKER_ENTRY_TAKEN naming JIG-5", err)
	}
	if n := len(entryLines(t, bob)); n != 1 {
		t.Errorf("bob sees %d entry lines after losing, want alice's one", n)
	}
	other := TrackerEntry{Provider: ProviderGitHub, Scope: "o/r", ID: "49"}
	if err := bob.ReserveEntry(other, "JIG-6"); err != nil {
		t.Errorf("another entry: %v", err)
	}
}

// TestClaimEntryAppendsNothingInSingleMainMode: with no claims branch the
// tracker tree is the guard, and no claims file appears.
func TestClaimEntryAppendsNothingInSingleMainMode(t *testing.T) {
	t.Parallel()
	st, _ := newTestStandaloneStore(t)
	if err := st.ReserveEntry(TrackerEntry{Provider: ProviderGitHub, Scope: "o/r", ID: "48"}, "JIG-5"); err != nil {
		t.Fatal(err)
	}
	if lines, err := st.Claims(); err != nil || len(lines) != 0 {
		t.Errorf("claims = %v, %v; want none", lines, err)
	}
}
