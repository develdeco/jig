package events

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestOnlyASubagentsFirstCallsAreHeld: the session's own calls, a subagent's
// calls once it has bound a line, and lines that are no calls are placed as
// they arrive.
func TestOnlyASubagentsFirstCallsAreHeld(t *testing.T) {
	t.Parallel()
	b := NewBinder(true)
	call := func(agent, ticket, cmd string) journal.Line {
		return journal.Line{TS: "2026-10-23T09:00:00Z", Event: "tool", Session: "s1", Agent: agent, Ticket: ticket, Tool: "Bash", Command: cmd}
	}
	if got := b.Bind(call("", "", "ls")); len(got) != 1 {
		t.Errorf("the session's own call that names none was held: %+v", got)
	}
	if got := b.Bind(journal.Line{Event: "hook", Session: "s1", Agent: "a-1"}); len(got) != 1 {
		t.Errorf("a line that is no call was held: %+v", got)
	}
	if got := b.Bind(call("a-1", "", "first")); len(got) != 0 {
		t.Errorf("a subagent's first call was placed: %+v", got)
	}
	if got := b.Bind(call("a-1", "T-1", "bound")); len(got) != 2 {
		t.Errorf("the first bound line placed %d lines, want the held call and itself", len(got))
	}
	if got := b.Bind(call("a-1", "", "after")); len(got) != 1 || got[0].Ticket != "" {
		t.Errorf("a call after the first bound line = %+v, want it placed as it is", got)
	}
	if got := b.Bind(call("a-2", "", "other agent")); len(got) != 0 {
		t.Errorf("another agent's first call was placed: %+v", got)
	}
}
