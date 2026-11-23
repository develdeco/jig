package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// byAgent sets the agent of a ledger line.
func byAgent(agent string) func(*journal.Line) {
	return func(l *journal.Line) { l.Agent = agent }
}

// TestWorldBindsByAgentAndFoldsDoubles: the world places a ledger line as
// `jig events` does. A line is on the ticket it names; the early calls of a
// subagent join its first bound line's ticket and carry its agent; a call that
// names none after that is on none; a call heard twice is one, and two calls
// that differ in the command are two.
func TestWorldBindsByAgentAndFoldsDoubles(t *testing.T) {
	t.Parallel()
	w := newWorld()
	first := tool(at(0, 1), "s-1", "jig here", said("Read where the work is"), byAgent("a-1"))
	touched, fresh := w.apply([]events.Envelope{
		first,
		tool(at(0, 2), "s-1", "git status", byAgent("a-2")),
	})
	if len(touched) != 0 || fresh || len(w.feed) != 0 {
		t.Fatalf("early calls placed before any bound line: touched %v, fresh %v, feed %d", touched, fresh, len(w.feed))
	}
	touched, fresh = w.apply([]events.Envelope{
		toolOn("T-1", at(0, 3), "s-1", "jig claim T-1 --slice a", byAgent("a-1")),
		toolOn("T-1", at(0, 3), "s-1", "jig claim T-1 --slice a", byAgent("a-1")), // the same call, heard twice
		toolOn("T-1", at(0, 4), "s-1", "go test ./...", byAgent("a-1")),
		toolOn("T-1", at(0, 4), "s-1", "go vet ./...", byAgent("a-1")), // another call of the same instant
		tool(at(0, 5), "s-1", "ls"),                                    // the session's own, after its claim
		tool(at(0, 6), "s-1", "ls", byAgent("a-1")),
	})
	if !fresh || !touched["T-1"] || len(touched) != 1 {
		t.Errorf("touched = %v, fresh = %v, want T-1 alone, new", touched, fresh)
	}
	var got []string
	for _, l := range w.tickets["T-1"].ledger {
		got = append(got, l.Agent+" "+l.Command)
	}
	want := []string{"a-1 jig here", "a-1 jig claim T-1 --slice a", "a-1 go test ./...", "a-1 go vet ./..."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("T-1's calls = %q, want %q: the early call joined, the double folded, the call that names none on none", got, want)
	}
	if got := w.tickets["T-1"].ledger[0].Ticket; got != "T-1" {
		t.Errorf("the early call is on %q, want T-1", got)
	}
	if len(w.tickets) != 1 {
		t.Errorf("tickets = %d, want only T-1: a-2 never bound a line", len(w.tickets))
	}
}

// TestActivityShowsASubagentsCallsOnItsOwnTicket: two subagents of one
// session each work a ticket from the session's directory. Each ticket's
// activity holds its own agent's calls from its first call, once each, and
// not the other's, nor a call of the session after its claims ended.
func TestActivityShowsASubagentsCallsOnItsOwnTicket(t *testing.T) {
	t.Parallel()
	status := func(id string) string {
		return strings.Join([]string{"ticket: " + id, "state: building",
			axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
				{"a", "building", "1", "-", "-", "Round to the cent"},
			}), "questions: none"}, "\n") + "\n"
	}
	feed := []events.Envelope{
		ll(at(0, 0), "", journal.EventSessionStart, func(l *journal.Line) { l.Session, l.Harness, l.Model = "s-m", "claude", "claude-sonnet-5" }),
		tool(at(1, 0), "s-m", "jig here", said("First look at one"), byAgent("a-1")),
		tool(at(1, 1), "s-m", "jig here", said("First look at two"), byAgent("a-2")),
		jl(at(1, 5), "SHOP-1", "a", journal.EventClaim, with("s-m", "", "", "", "", "")),
		jl(at(1, 6), "SHOP-2", "a", journal.EventClaim, with("s-m", "", "", "", "", "")),
		toolOn("SHOP-1", at(1, 7), "s-m", "jig claim SHOP-1 --slice a", said("Claim one"), byAgent("a-1")),
		toolOn("SHOP-2", at(1, 8), "s-m", "jig claim SHOP-2 --slice a", said("Claim two"), byAgent("a-2")),
		toolOn("SHOP-1", at(2, 0), "s-m", "go test ./...", said("Test one"), byAgent("a-1")),
		toolOn("SHOP-1", at(2, 0), "s-m", "go test ./...", said("Test one"), byAgent("a-1")),
		toolOn("SHOP-2", at(2, 1), "s-m", "go build ./...", said("Build two"), byAgent("a-2")),
		tool(at(3, 0), "s-m", "git log", said("After the work")),
	}
	for _, c := range []struct {
		ticket, other string
		have, lack    []string
	}{
		{"SHOP-1", "SHOP-2", []string{"First look at one", "Claim one", "Test one"}, []string{"First look at two", "Claim two", "Build two", "After the work"}},
		{"SHOP-2", "SHOP-1", []string{"First look at two", "Claim two", "Build two"}, []string{"First look at one", "Claim one", "Test one", "After the work"}},
	} {
		g := newRig(t, c.ticket)
		g.r.answer("status "+c.ticket+" --store /s", status(c.ticket), 0)
		g.send(tea.WindowSizeMsg{Width: 100, Height: 50})
		g.feed(feed...)
		act := strings.Join(panelLines(strings.Split(g.view(), "\n"), "Activity"), "\n")
		for _, want := range c.have {
			if n := strings.Count(act, want); n != 1 {
				t.Errorf("%s: the activity shows %q %d times, want once:\n%s", c.ticket, want, n, act)
			}
		}
		for _, no := range c.lack {
			if strings.Contains(act, no) {
				t.Errorf("%s: the activity shows %q, a call that is not its own:\n%s", c.ticket, no, act)
			}
		}
	}
}
