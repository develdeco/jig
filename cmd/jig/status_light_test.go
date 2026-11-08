package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestStatusShowsALightPassAsLintOnly: a slice whose latest verified commit
// passed on a light delta is listed as lint only, reusing the origin run; a
// slice that ran its own oracle, and a ticket with none, show nothing.
func TestStatusShowsALightPassAsLintOnly(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	add := func(l journal.Line) {
		t.Helper()
		if err := journal.Append(st, fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	origin := "0123456789abcdef0123456789abcdef01234567"
	add(journal.Line{Slice: "full", Event: journal.EventOracle, Outcome: "pass", Commit: origin, Attempt: 1, Seconds: 90})
	add(journal.Line{Slice: "full", Event: journal.EventVerified, Commit: origin, Attempt: 1})
	if got := block(statusOf(t, fx, st), "light["); got != nil {
		t.Fatalf("a full run shows a light block: %v", got)
	}

	notes := "fedcba9876543210fedcba9876543210fedcba98"
	add(journal.Line{Slice: "notes", Event: journal.EventOracle, Outcome: "pass", Commit: notes, Attempt: 1, Seconds: 3, Ran: "go test ./lint", ReusedFrom: origin})
	add(journal.Line{Slice: "notes", Event: journal.EventVerified, Commit: notes, Attempt: 1})
	got := strings.Join(block(statusOf(t, fx, st), "light["), "\n")
	want := strings.Join([]string{
		"light[1]{slice,commit,pass}:",
		`  notes,fedcba987654,"lint only, reusing 0123456789ab"`,
	}, "\n")
	if got != want {
		t.Errorf("light block:\n%s\nwant:\n%s", got, want)
	}
}
