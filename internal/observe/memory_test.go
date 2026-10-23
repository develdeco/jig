package observe

import (
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

func TestReadMemoryFoldsOnlyTheSessionsOwnLinesBackToItsStart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	put := func(min int, l journal.Line) {
		t.Helper()
		l.TS = ledger.Stamp(at.Add(time.Duration(min) * time.Minute))
		if err := ledger.Append(root, l); err != nil {
			t.Fatal(err)
		}
	}
	// An earlier life of s1, which a later start cuts off.
	put(0, journal.Line{Event: journal.EventNoteDelivered, Session: "s1", Ref: "old"})
	put(1, journal.Line{Event: journal.EventSessionStart, Session: "s1", Ticket: "JIG-0", Commit: "c0"})
	put(2, journal.Line{Event: journal.EventNoteDelivered, Session: "s1", Ref: "older"})
	put(3, journal.Line{Event: journal.EventSessionStart, Session: "s1", Ticket: "JIG-1", Slice: "a", Commit: "c1"})
	put(4, journal.Line{Event: journal.EventNoteDelivered, Session: "s1", Ref: "told", Outcome: "prompt"})
	put(5, journal.Line{Event: journal.EventNoteDelivered, Session: "s2", Ref: "someone else's"})
	put(6, journal.Line{Event: journal.EventTool, Session: "s1", Cwd: "/work", Commit: "c2", Ticket: "JIG-1", Slice: "a"})
	put(7, journal.Line{Event: journal.EventNoteDelivered, Session: "s1", Ref: "refused", Outcome: outcomeStopBlock})
	put(8, journal.Line{Event: journal.EventTool, Session: "s1", Tool: "Edit"}) // an edit says nothing of HEAD

	m, err := readMemory(root, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !m.started.Equal(at.Add(3 * time.Minute)) {
		t.Errorf("started = %v, want the stamp of the newest session-start", m.started)
	}
	if m.head != "c2" || m.headCwd != "/work" || m.ticket != "JIG-1" || m.slice != "a" {
		t.Errorf("head %q in %q, ticket %q slice %q, want the newest line that recorded each", m.head, m.headCwd, m.ticket, m.slice)
	}
	for _, ref := range []string{"told", "refused"} {
		if !m.delivered[ref] {
			t.Errorf("%q is not remembered as delivered", ref)
		}
	}
	for _, ref := range []string{"old", "older", "someone else's"} {
		if m.delivered[ref] {
			t.Errorf("%q is remembered as delivered to s1", ref)
		}
	}
	if !m.blocked["refused"] || m.blocked["told"] {
		t.Errorf("blocked = %v, want only the note it was refused a Stop for", m.blocked)
	}
}

func TestReadMemoryOfASessionTheLedgerNeverHeardOf(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if m, err := readMemory(root, "s1"); err != nil || !m.started.IsZero() || m.head != "" || len(m.delivered) != 0 {
		t.Errorf("memory of an absent ledger = %+v, %v", m, err)
	}
	if err := ledger.Append(root, journal.Line{Event: journal.EventTool, Session: "s2", Commit: "x"}); err != nil {
		t.Fatal(err)
	}
	if m, err := readMemory(root, "s1"); err != nil || !m.started.IsZero() || m.head != "" {
		t.Errorf("memory of another session's lines = %+v, %v", m, err)
	}
	// A session with no id has no memory: nothing could say which lines are its.
	if m, err := readMemory(root, ""); err != nil || m.head != "" {
		t.Errorf("memory of no session = %+v, %v", m, err)
	}
}

func TestAWorkingDirectoryStandsInForAnEventThatNamesNone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.d.Cwd = r.fx.RepoDir
	res, err := Handle(r.d, Event{Harness: "claude", Kind: KindSessionStart, Session: "s1"})
	if err != nil || res.Context == "" {
		t.Errorf("Handle = %+v, %v, want the context of the working directory", res, err)
	}
}
