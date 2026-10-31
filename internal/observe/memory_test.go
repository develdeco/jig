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

	m, err := readMemory(root, "s1", "")
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
	if m, err := readMemory(root, "s1", ""); err != nil || !m.started.IsZero() || m.head != "" || len(m.delivered) != 0 {
		t.Errorf("memory of an absent ledger = %+v, %v", m, err)
	}
	if err := ledger.Append(root, journal.Line{Event: journal.EventTool, Session: "s2", Commit: "x"}); err != nil {
		t.Fatal(err)
	}
	if m, err := readMemory(root, "s1", ""); err != nil || !m.started.IsZero() || m.head != "" {
		t.Errorf("memory of another session's lines = %+v, %v", m, err)
	}
	// A session with no id has no memory: nothing could say which lines are its.
	if m, err := readMemory(root, "", ""); err != nil || m.head != "" {
		t.Errorf("memory of no session = %+v, %v", m, err)
	}
}

func TestReadMemoryKeepsDeliveredPerAgent(t *testing.T) {
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
	put(0, journal.Line{Event: journal.EventSessionStart, Session: "s1", Ticket: "JIG-1"})
	put(1, journal.Line{Event: journal.EventNoteDelivered, Session: "s1", Ref: "main's", Outcome: "prompt"})
	put(2, journal.Line{Event: journal.EventNoteDelivered, Session: "s1", Agent: "a-1", Ref: "told", Outcome: "tool"})
	put(3, journal.Line{Event: journal.EventNoteDelivered, Session: "s1", Agent: "a-1", Ref: "refused", Outcome: outcomeStopBlock})
	put(4, journal.Line{Event: journal.EventNoteDelivered, Session: "s1", Agent: "a-2", Ref: "a-2's", Outcome: outcomeStopBlock})
	put(5, journal.Line{Event: journal.EventTool, Session: "s1", Agent: "a-1", Ticket: "JIG-2", Cwd: "/wt/a-1"})
	put(6, journal.Line{Event: journal.EventTool, Session: "s1", Agent: "a-2", Ticket: "JIG-3", Cwd: "/wt/a-2"})

	m, err := readMemory(root, "s1", "a-1")
	if err != nil {
		t.Fatal(err)
	}
	if !m.delivered["told"] || !m.delivered["refused"] || !m.blocked["refused"] || m.blocked["told"] {
		t.Errorf("a-1 remembers delivered %v, blocked %v, want its own two and the refusal", m.delivered, m.blocked)
	}
	for _, ref := range []string{"main's", "a-2's"} {
		if m.delivered[ref] || m.blocked[ref] {
			t.Errorf("a-1 remembers %q, which was given to another agent", ref)
		}
	}
	if !m.started.Equal(at) || m.ticket != "JIG-3" {
		t.Errorf("started %v, ticket %q: the session's facts stay the session's, want its start and its newest ticket", m.started, m.ticket)
	}
	if m.agentTicket != "JIG-2" || m.agentCwd != "/wt/a-1" {
		t.Errorf("agent place = %q in %q, want the newest line of a-1 that named one", m.agentTicket, m.agentCwd)
	}

	main, err := readMemory(root, "s1", "")
	if err != nil {
		t.Fatal(err)
	}
	if !main.delivered["main's"] || main.delivered["told"] || main.blocked["refused"] || main.agentCwd != "" {
		t.Errorf("the session's own agent remembers delivered %v, blocked %v, place %q, want only its own", main.delivered, main.blocked, main.agentCwd)
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
