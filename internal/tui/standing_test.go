package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// statusOfFacts is `jig status` as it would print the facts f: the part of the
// output the screens read.
func statusOfFacts(f phase.Facts) string {
	parts := []string{"ticket: " + f.Ticket, "state: building"}
	if f.Adopted {
		parts = append(parts, "branch: "+f.Branch)
	}
	var slices [][]string
	for _, s := range f.Slices {
		blocked := "-"
		if len(s.BlockedBy) > 0 {
			blocked = strings.Join(s.BlockedBy, "+")
		}
		slices = append(slices, []string{s.ID, s.State, "1", blocked, "-", firstOf(s.Goal, "Do "+s.ID)})
	}
	parts = append(parts, axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, slices), "questions: none")
	if f.BriefExists {
		approval := [][2]string{{"approval", f.Approval}}
		if f.ApprovedBy != "" {
			approval = append(approval, [2]string{"approved_by", f.ApprovedBy}, [2]string{"provenance", f.ApprovedHow})
		}
		parts = append(parts, axi.KV("brief", approval))
		var sections [][]string
		for i := 0; i < f.Sections; i++ {
			sections = append(sections, []string{fmt.Sprintf("Section %d", i+1), "-"})
		}
		parts = append(parts, axi.Table("brief_sections", []string{"heading", "slices"}, sections))
	}
	if len(f.Asks) > 0 {
		var asks [][]string
		for _, a := range f.Asks {
			asks = append(asks, []string{a.ID, "low", "a.go:1", "Title of " + a.ID, "-"})
		}
		parts = append(parts, axi.Table("outstanding_asks", []string{"id", "risk", "file:line", "title", "why"}, asks))
	}
	return strings.Join(parts, "\n") + "\n"
}

// TestTheScreensStandWhereInternalPhaseSaysTheTicketStands: for a ticket in
// each of the states it goes through, what the Steps panel shows (the current
// step, whether it can start, each step's state, what each says) is what
// phase.Derive answers for the same facts, since the screens read their facts
// from the status and the journal and derive nothing of their own.
func TestTheScreensStandWhereInternalPhaseSaysTheTicketStands(t *testing.T) {
	t.Parallel()
	const T = "SHOP-1"
	slice := func(id, state string, blockedBy ...string) phase.Slice {
		return phase.Slice{ID: id, State: state, BlockedBy: blockedBy}
	}
	drafted := phase.Facts{Ticket: T, BriefExists: true, Sections: 3, Approval: phase.ApprovalNone,
		Slices: []phase.Slice{slice("a", "queued"), slice("b", "queued", "a")}}
	approved := drafted
	approved.Approval, approved.ApprovedBy, approved.ApprovedHow = phase.ApprovalCurrent, "Dana", "person"
	building := approved
	building.Slices = []phase.Slice{slice("a", "green"), slice("b", "building", "a")}
	green := approved
	green.Slices = []phase.Slice{slice("a", "green"), slice("b", "green", "a")}
	open := green
	open.Open = &phase.OpenRound{Round: 1}
	findings := green
	findings.Asks = []phase.Ask{{ID: "r1-f1"}}
	changed := green
	changed.Approval = phase.ApprovalChanged
	adopted := phase.Facts{Ticket: T, Adopted: true, Branch: "feature/cart"}

	l := func(event string, mod ...func(*journal.Line)) journal.Line {
		line := journal.Line{Ticket: T, Event: event}
		for _, f := range mod {
			f(&line)
		}
		return line
	}
	slc := func(id string) func(*journal.Line) { return func(x *journal.Line) { x.Slice = id } }
	attempt := func(n int) func(*journal.Line) { return func(x *journal.Line) { x.Attempt = n } }
	commit := func(c byte) func(*journal.Line) {
		return func(x *journal.Line) { x.Commit = strings.Repeat(string(c), 40) }
	}
	approval := l(journal.EventApproval, func(x *journal.Line) { x.Summary, x.Provenance = "Dana <dana@example.invalid>", "person" })
	built := []journal.Line{
		approval,
		l(journal.EventClaim, slc("a")), l(journal.EventVerified, slc("a"), commit('a')),
		l(journal.EventClaim, slc("b")), l(journal.EventVerified, slc("b"), commit('b')),
	}
	plus := func(base []journal.Line, more ...journal.Line) []journal.Line {
		return append(append([]journal.Line{}, base...), more...)
	}
	round1 := l(journal.EventGateRound, attempt(1))
	clean1 := l(journal.EventGateClean, attempt(1))
	published := green
	published.Published = true

	for _, tc := range []struct {
		name  string
		facts phase.Facts
		lines []journal.Line
		want  string // the current phase and whether it is ready
	}{
		{"a brief that is drafted", drafted, nil, "brief ready"},
		{"a brief that is approved", approved, []journal.Line{approval}, "build ready"},
		{"a build under way", building, plus(built[:3], l(journal.EventClaim, slc("b")), l(journal.EventVerifyPending, slc("b"), commit('b'))), "build ready"},
		{"a build that is green", green, built, "review ready"},
		{"a round open", open, plus(built, l(journal.EventGateOpen, attempt(1))), "review not ready"},
		{"a round that found asks to decide", findings, plus(built, l(journal.EventGateOpen, attempt(1)), round1), "review not ready"},
		{"a round whose asks are decided", findings, plus(built, l(journal.EventGateOpen, attempt(1)), round1,
			l(journal.EventTriage, func(x *journal.Line) { x.Ref, x.Outcome, x.Provenance = "r1-f1", "dismiss", "person" })), "review ready"},
		{"a clean round", green, plus(built, l(journal.EventGateOpen, attempt(1)), clean1), "publish ready"},
		{"a clean round the build went on after", green, plus(built, l(journal.EventGateOpen, attempt(1)), clean1, l(journal.EventClaim, slc("a"))), "review ready"},
		{"a body written", green, plus(built, l(journal.EventGateOpen, attempt(1)), clean1, l(journal.EventPrBody, commit('b'))), "publish ready"},
		{"a published ticket", published, plus(built, l(journal.EventGateOpen, attempt(1)), clean1, l(journal.EventPublishDone)), "publish not ready"},
		{"a brief that changed after it was approved", changed, built, "brief ready"},
		{"an adopted branch", adopted, nil, "review ready"},
	} {
		d := phase.Derive(tc.facts, tc.lines)
		if got := fmt.Sprintf("%s %s", d.Current, map[bool]string{true: "ready", false: "not ready"}[d.Ready]); got != tc.want {
			t.Fatalf("%s: phase.Derive says %s, the case wants %s: the case is wrong", tc.name, got, tc.want)
		}

		m := New(Config{Zone: time.UTC})
		var envs []events.Envelope
		for i, line := range tc.lines {
			line.TS = at(0, i)
			envs = append(envs, events.Envelope{Src: events.SrcJournal, Store: "demo", Line: line})
		}
		m.w.apply(envs)
		tk, _ := m.w.ticketFor(T)
		tk.status = parseStatus(statusOfFacts(tc.facts))
		if tk.status.Err != nil {
			t.Fatalf("%s: the status does not parse: %s", tc.name, tk.status.Err.Msg)
		}

		got, ok := m.Standing(T)
		if !ok {
			t.Fatalf("%s: no standing", tc.name)
		}
		if got.Current != string(d.Current) || got.Ready != d.Ready {
			t.Errorf("%s: the screens stand at %s (ready %v), phase.Derive at %s (ready %v)", tc.name, got.Current, got.Ready, d.Current, d.Ready)
		}
		steps := m.steps(tk)
		for i, p := range d.Phases {
			if got.States[i] != p.State {
				t.Errorf("%s: the screens have %s %s, phase.Derive %s", tc.name, p.Name, got.States[i], p.State)
			}
			if steps[i].Text != p.Text {
				t.Errorf("%s: the %s row says %q, phase.Derive %q", tc.name, p.Name, steps[i].Text, p.Text)
			}
		}
		// The same answer by the entry point a caller with no model uses.
		if s := StandingOf(T, statusOfFacts(tc.facts), envs); s != got {
			t.Errorf("%s: StandingOf = %+v, the model's = %+v", tc.name, s, got)
		}
	}
}

// TestAStatusNobodyReadYetStandsOnTheJournalAlone: the main screen reads no
// status, and a ticket seen only through its lines still stands where the
// lines say.
func TestAStatusNobodyReadYetStandsOnTheJournalAlone(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	g.feed(jig2()...)
	st, ok := g.m.Standing("JIG-2")
	if !ok || st.Current != "publish" || st.States != [4]string{"done", "done", "done", "done"} {
		t.Errorf("a published ticket seen by its lines stands at %+v, want four phases done", st)
	}
	g.feed(jig1()...)
	if st, _ := g.m.Standing("JIG-1"); st.Current != "build" || st.States[0] != "done" {
		t.Errorf("a ticket mid-build seen by its lines stands at %+v, want the build current", st)
	}
	if _, ok := g.m.Standing("NOPE-1"); ok {
		t.Errorf("a ticket the screens know nothing of has a standing")
	}
}

// TestADecisionARoundHasClosedSinceIsSpent: the asks of the screen follow the
// rule the phase derivation does: a decision made before the last round closed
// was applied by it, and an ask the round left again waits for a new one.
func TestADecisionARoundHasClosedSinceIsSpent(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(jig2Mid()...)
	triage := jl(at(12, 0), "JIG-2", "", journal.EventTriage, with("s-person", "", "dismiss", "not a bug", "r1-f1", "person"))
	g.feed(triage)
	asks := g.m.w.tickets["JIG-2"].asks()
	if len(asks) != 2 || asks[0].Decision != "dismiss" || asks[1].Decision != "" {
		t.Fatalf("asks after a decision = %+v, want r1-f1 dismissed and r1-f2 undecided", asks)
	}
	if st, _ := g.m.Standing("JIG-2"); st.Ready {
		t.Errorf("the review is ready with r1-f2 undecided")
	}
	// Round 2 closes with the same asks: both are undecided again.
	g.feed(
		jl(at(13, 0), "JIG-2", "", journal.EventGateOpen, func(l *journal.Line) { l.Attempt = 2 }),
		jl(at(13, 30), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 2, "s-reviewer" }),
	)
	for _, a := range g.m.w.tickets["JIG-2"].asks() {
		if a.Decision != "" {
			t.Errorf("ask %s still reads as decided after a round closed: %+v", a.ID, a)
		}
	}
	if !strings.Contains(g.view(), "undecided") || strings.Contains(g.view(), "dismissed by you") {
		t.Errorf("the ticket screen shows a spent decision:\n%s", g.view())
	}
}
