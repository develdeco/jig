package events

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// TestLedgerLinesInScope: with no AllLedger a ledger line is in the stream
// when it names a ticket of the scope, and a line that names none is on none,
// whatever its session did before; AllLedger takes every line.
func TestLedgerLinesInScope(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1", jline("2026-10-23T09:00:00Z", "T-1", "claim", ""))
	at := func(n int) string { return fmt.Sprintf("2026-10-23T09:01:%02dZ", n) }
	// In: names the ticket.
	r.ledgerAppend(journal.Line{TS: at(0), Event: "session-start", Session: "s1", Ticket: "T-1"})
	r.ledgerAppend(journal.Line{TS: at(1), Event: "tool", Session: "s1", Tool: "Edit", Ticket: "T-1"})
	// Out: names none, though its session was on T-1 a line ago.
	r.ledgerAppend(journal.Line{TS: at(2), Event: "tool", Session: "s1", Tool: "Edit"})
	// Out: another ticket, and a line of its session that names none.
	r.ledgerAppend(journal.Line{TS: at(3), Event: "session-start", Session: "s3", Ticket: "X-9"})
	r.ledgerAppend(journal.Line{TS: at(4), Event: "tool", Session: "s3", Tool: "Bash"})
	// Out: no ticket and no session.
	r.ledgerAppend(journal.Line{TS: at(5), Event: "hook"})
	// In: s2 moves to T-1, and its next call names it.
	r.ledgerAppend(journal.Line{TS: at(6), Event: "session-touch", Session: "s2", Ticket: "T-1"})
	r.ledgerAppend(journal.Line{TS: at(7), Event: "tool", Session: "s2", Tool: "Bash", Ticket: "T-1"})
	// Out: s1 moves away, and its call after names none.
	r.ledgerAppend(journal.Line{TS: at(8), Event: "session-touch", Session: "s1", Ticket: "X-9"})
	r.ledgerAppend(journal.Line{TS: at(9), Event: "tool", Session: "s1", Tool: "Read"})

	envs, err := Read(r.scope(false, "T-1"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range envs {
		if e.Src == SrcLedger {
			got = append(got, e.Event+"/"+e.Session+"/"+e.Tool)
		}
	}
	want := []string{"session-start/s1/", "tool/s1/Edit", "session-touch/s2/", "tool/s2/Bash"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ledger lines in scope = %v, want %v", got, want)
	}

	envs, err = Read(r.scope(false, "T-1"), mustParse(t, at(7)))
	if err != nil {
		t.Fatal(err)
	}
	if got := summary(envs); !reflect.DeepEqual(got, []string{"ledger:tool"}) || envs[0].Session != "s2" {
		t.Fatalf("since %s = %v, want only s2's tool line", at(7), got)
	}

	all, err := Read(r.scope(true, "T-1"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(all); n != 11 { // the journal line and the ten ledger lines
		t.Fatalf("AllLedger gave %d lines, want 11", n)
	}
}

// ledgerView is the ledger lines of envs as event/agent/command/ticket.
func ledgerView(envs []Envelope) []string {
	var out []string
	for _, e := range envs {
		if e.Src == SrcLedger {
			out = append(out, e.Event+"/"+e.Agent+"/"+e.Command+"/"+e.Ticket)
		}
	}
	return out
}

// TestReaderBindsLinesByAgent: two agents of one session are on two tickets,
// the session's own calls that name none are on none, the early calls of a
// subagent join the ticket of its first bound line, an old line that names
// none stays on none, and a Poll hands the held calls back once, with that
// line.
func TestReaderBindsLinesByAgent(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1", jline("2026-10-23T09:00:00Z", "T-1", "claim", ""))
	r.writeJournal("T-2", jline("2026-10-23T09:00:00Z", "T-2", "claim", ""))
	at := func(n int) string { return fmt.Sprintf("2026-10-23T09:01:%02dZ", n) }
	tool := func(n int, agent, ticket, cmd string) journal.Line {
		return journal.Line{TS: at(n), Event: "tool", Session: "s1", Agent: agent, Ticket: ticket, Tool: "Bash", Command: cmd}
	}
	r.ledgerAppend(tool(0, "a-1", "", "jig here"))     // early: joins T-1
	r.ledgerAppend(tool(1, "a-2", "", "jig here"))     // early: joins T-2
	r.ledgerAppend(tool(2, "", "", "ls"))              // the session's own: none
	r.ledgerAppend(tool(3, "a-1", "T-1", "jig claim")) // first bound line of a-1
	r.ledgerAppend(tool(4, "a-3", "", "git status"))   // early, and a-3 never binds
	r.ledgerAppend(tool(5, "a-1", "", "go test"))      // after the first bound line: none
	r.ledgerAppend(tool(6, "a-2", "T-2", "jig claim")) // first bound line of a-2
	r.ledgerAppend(tool(7, "a-2", "T-2", "go build"))  // its own
	r.ledgerAppend(journal.Line{TS: at(8), Event: "hook", Session: "s1", Agent: "a-1", Outcome: "stop"})
	// A line that is no call is not an early call: it joins nothing.
	r.ledgerAppend(journal.Line{TS: at(9), Event: "hook", Session: "s1", Agent: "a-5", Outcome: "prompt"})
	r.ledgerAppend(tool(9, "a-5", "T-1", "jig claim"))

	rd := NewReader(r.scope(false, "T-1", "T-2"), time.Time{})
	envs, err := rd.Poll()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tool/a-1/jig here/T-1", "tool/a-2/jig here/T-2", "tool/a-1/jig claim/T-1",
		"tool/a-2/jig claim/T-2", "tool/a-2/go build/T-2", "tool/a-5/jig claim/T-1",
	}
	if got := ledgerView(envs); !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %v\nwant    %v", got, want)
	}

	// A late subagent: its early call arrives in one Poll and its first bound
	// line in a later one; the held call is handed back once, with the line.
	r.ledgerAppend(tool(10, "a-4", "", "jig here"))
	if envs, err = rd.Poll(); err != nil || len(envs) != 0 {
		t.Fatalf("Poll with only an early call = %v, %v, want nothing yet", ledgerView(envs), err)
	}
	r.ledgerAppend(tool(11, "a-4", "T-2", "jig claim"))
	envs, err = rd.Poll()
	if want := []string{"tool/a-4/jig here/T-2", "tool/a-4/jig claim/T-2"}; err != nil || !reflect.DeepEqual(ledgerView(envs), want) {
		t.Fatalf("Poll with the first bound line = %v, %v, want %v", ledgerView(envs), err, want)
	}
	if envs, err = rd.Poll(); err != nil || len(envs) != 0 {
		t.Errorf("Poll again = %v, %v, want nothing: the held call is handed back once", ledgerView(envs), err)
	}
}

// TestBinderHoldsAFixedAmount: an agent that never binds holds a bounded
// number of early calls (the newest), the agents followed are bounded, so is
// the memory of calls for the fold; a stream with no ticket to place lines on
// holds nothing.
func TestBinderHoldsAFixedAmount(t *testing.T) {
	t.Parallel()
	call := func(agent string, n int) journal.Line {
		return journal.Line{TS: fmt.Sprintf("2026-10-23T09:00:00.%09dZ", n), Event: "tool", Session: "s1", Agent: agent, Tool: "Bash", Command: fmt.Sprint("c", n)}
	}
	b := NewBinder(true)
	for n := 0; n < maxHeld+10; n++ {
		if got := b.Bind(call("a-1", n)); len(got) != 0 {
			t.Fatalf("an early call was placed: %+v", got)
		}
	}
	bound := call("a-1", 1000)
	bound.Ticket = "T-1"
	got := b.Bind(bound)
	if len(got) != maxHeld+1 || got[0].Command != "c10" || got[len(got)-1].Command != "c1000" {
		t.Errorf("the first bound line placed %d lines from %q, want the newest %d early calls and itself", len(got), got[0].Command, maxHeld)
	}
	for _, l := range got {
		if l.Ticket != "T-1" {
			t.Fatalf("a placed line is on %q, want T-1", l.Ticket)
		}
	}

	for n := 0; n < maxSeats+50; n++ {
		b.Bind(call(fmt.Sprint("agent-", n), n))
	}
	if len(b.seats) > maxSeats {
		t.Errorf("%d agents followed, want at most %d", len(b.seats), maxSeats)
	}
	for n := 0; n < maxFold+50; n++ {
		b.Bind(call("a-1", 5000+n))
	}
	if len(b.seen) > maxFold || len(b.ring) > maxFold {
		t.Errorf("%d calls remembered, want at most %d", len(b.seen), maxFold)
	}

	free := NewBinder(false)
	if got := free.Bind(call("a-1", 1)); len(got) != 1 || got[0].Ticket != "" {
		t.Errorf("a Binder that holds nothing placed %+v, want the line as it is", got)
	}
}

// TestReaderFoldsIdenticalLines: a call written twice (two hooks heard it)
// is one line; two calls that differ in the instant, the command, the tool,
// the agent or the session are two.
func TestReaderFoldsIdenticalLines(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.writeJournal("T-1", jline("2026-10-23T09:00:00Z", "T-1", "claim", ""))
	call := journal.Line{TS: "2026-10-23T09:01:00Z", Event: "tool", Session: "s1", Agent: "a-1", Ticket: "T-1", Tool: "Bash", Command: "go test"}
	r.ledgerAppend(call)
	r.ledgerAppend(call)
	later := call
	later.TS = "2026-10-23T09:01:01Z"
	otherCmd := call
	otherCmd.Command = "go vet"
	otherTool := call
	otherTool.Tool = "Read"
	otherAgent := call
	otherAgent.Agent = "a-2"
	otherSession := call
	otherSession.Session = "s2"
	for _, l := range []journal.Line{later, otherCmd, otherTool, otherAgent, otherSession} {
		r.ledgerAppend(l)
	}
	envs, err := Read(r.scope(false, "T-1"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ledgerView(envs)); got != 6 {
		t.Errorf("%d call lines, want 6: the double folded and the five that differ kept: %v", got, ledgerView(envs))
	}
}
