package main

import (
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestTUIReadsAStoreThatMoves: with the project screen open on a real store, a
// chart made by another process shows in a later frame once the store commits
// it, and a ticket the store gained with no commit and no journal line shows
// once a landed line (which moves no commit either) reaches the stream.
func TestTUIReadsAStoreThatMoves(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	keys, out, wait := startTUI(t, e)
	out.until(t, fx.Ticket)

	// A chart made by another process commits the store, which moves its head.
	if got, code := runMain(t, e, "", "chart", "new", "intake", "--title", "Intake chart"); code != 0 {
		t.Fatalf("jig chart new: exit %d\n%s", code, got)
	}
	out.until(t, "Intake chart")

	// A ticket record written to the store and left uncommitted moves no head
	// and writes no line: the list does not show it yet.
	if err := st.CreateTicketRecord("JIG-7", store.Ticket{Title: "Late arrival"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second)
	if strings.Contains(out.String(), "not started (") {
		t.Fatalf("a ticket with no line and no commit showed:\n%s", out.String())
	}

	// A landed line from another session's jig land moves no commit, and says
	// the list may be out of date; the new ticket stands in its fold.
	if err := journal.Append(st, fx.Ticket, journal.Line{Event: journal.EventLanded, Repo: "r", URL: "https://example.test/o/r/pull/1", Provenance: "person"}); err != nil {
		t.Fatal(err)
	}
	out.until(t, "not started (1)")

	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}
