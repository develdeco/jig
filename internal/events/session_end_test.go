package events

import (
	"fmt"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// TestASessionEndReachesTheTicketsOfItsSession: the end of a session names no
// ticket, and a scoped reader hands it back for a session whose lines it handed
// back, and not for one it never did.
func TestASessionEndReachesTheTicketsOfItsSession(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	at := func(n int) string { return fmt.Sprintf("2026-10-23T09:01:%02dZ", n) }
	end := func(sec int, session string) journal.Line {
		return journal.Line{TS: at(sec), Event: journal.EventSessionEnd, Session: session}
	}
	r.ledgerAppend(journal.Line{TS: at(1), Event: journal.EventTool, Session: "s-in", Ticket: "T-1", Tool: "Bash", Command: "go test"})
	r.ledgerAppend(end(2, "s-in"))
	r.ledgerAppend(end(3, "s-never"))

	rd := NewReader(r.scope(false, "T-1"), time.Time{})
	envs, err := rd.Poll()
	if err != nil {
		t.Fatal(err)
	}
	var ends []string
	for _, e := range envs {
		if e.Event == journal.EventSessionEnd {
			ends = append(ends, e.Session)
		}
	}
	if len(ends) != 1 || ends[0] != "s-in" {
		t.Errorf("session ends handed back = %v, want only s-in's", ends)
	}
}
