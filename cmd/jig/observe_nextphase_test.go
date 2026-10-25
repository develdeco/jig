package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/observe"
	"github.com/develdeco/jig/internal/store"
)

const publishHead = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"

// publishCtx is what the observe core hands the next-phase source at a Stop on
// the fixture ticket, whose latest gate round reviewed publishHead with the
// given verdict, and whose journal holds lines.
func publishCtx(t *testing.T, verdict string, lines ...journal.Line) observe.NoteCtx {
	t.Helper()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(st.TicketDir(fx.Ticket), "gate", "round-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	report := "round: 1\nverdict: " + verdict + "\nreviewed_sha:\n  fixture-repo: " + publishHead + "\n"
	if err := os.WriteFile(filepath.Join(dir, "report.yaml"), []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if err := journal.Append(st, fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	return observe.NoteCtx{
		Event:   observe.Event{Kind: observe.KindStop, Session: "s1", Harness: "claude"},
		Where:   locate.Where{Ticket: fx.Ticket, Cwd: fx.RepoDir, RepoDir: fx.RepoDir},
		Store:   st,
		JigHome: fx.Home,
		Now:     time.Now(),
		Journal: func() ([]journal.Line, error) { return journal.Read(st, fx.Ticket) },
	}
}

func approval(head, provenance string) journal.Line {
	return journal.Line{Event: journal.EventPublishApproved, Commit: head, Branch: "jig/JIG-1", Provenance: provenance}
}

// TestPublishSourceHoldsBackAnApprovedTicketThatIsNotPublished: a clean round,
// a person's approval of the head it reviewed and no publish yet is the one
// case the source speaks, enforced, and it names the command.
func TestPublishSourceHoldsBackAnApprovedTicketThatIsNotPublished(t *testing.T) {
	t.Parallel()
	registered := false
	for _, s := range observe.NoteSources() {
		registered = registered || s.Name == "next-phase"
	}
	if !registered {
		t.Fatalf("note sources = %+v, want next-phase among them", observe.NoteSources())
	}
	c := publishCtx(t, "clean", approval(publishHead, "person"))
	notes, err := publishNotes(c)
	if err != nil || len(notes) != 1 {
		t.Fatalf("publishNotes = %+v, %v, want the one note", notes, err)
	}
	n := notes[0]
	if !n.Enforce || n.Ref != "publish:"+publishHead[:12] || !strings.Contains(n.Text, "`jig publish "+c.Where.Ticket+"`") || !strings.Contains(n.Text, publishHead[:12]) {
		t.Errorf("note = %+v, want it enforced, named by the approved head, and naming the publish command", n)
	}
}

// TestPublishSourceStaysSilentUnlessEverythingIsInPlace: each condition of the
// note on its own - a clean round, a person's approval, of the head the round
// reviewed, not published yet, at a Stop - is needed to hold a session back.
func TestPublishSourceStaysSilentUnlessEverythingIsInPlace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		verdict string
		lines   []journal.Line
		mutate  func(c *observe.NoteCtx)
	}{
		{"reviewed clean but never approved", "clean", nil, nil},
		{"approved by an agent, which a bare publish does not take for a person's", "clean", []journal.Line{approval(publishHead, "agent")}, nil},
		{"approved a head the round did not review", "clean", []journal.Line{approval("ffff0000ffff0000ffff0000ffff0000ffff0000", "person")}, nil},
		{"approved but the round was not clean", "fix-slices", []journal.Line{approval(publishHead, "person")}, nil},
		{"approved but the reviewer built what it reviewed", "not-independent", []journal.Line{approval(publishHead, "person")}, nil},
		{"approved and published already", "clean", []journal.Line{approval(publishHead, "person"), {Event: journal.EventPublishDone, Commit: publishHead}}, nil},
		{"a tool call, not a stop", "clean", []journal.Line{approval(publishHead, "person")}, func(c *observe.NoteCtx) { c.Event.Kind = observe.KindTool }},
		{"a prompt, not a stop", "clean", []journal.Line{approval(publishHead, "person")}, func(c *observe.NoteCtx) { c.Event.Kind = observe.KindPrompt }},
		{"no ticket", "clean", []journal.Line{approval(publishHead, "person")}, func(c *observe.NoteCtx) { c.Where.Ticket = "" }},
		{"no store", "clean", []journal.Line{approval(publishHead, "person")}, func(c *observe.NoteCtx) { c.Store = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := publishCtx(t, tc.verdict, tc.lines...)
			if tc.mutate != nil {
				tc.mutate(&c)
			}
			if notes, err := publishNotes(c); err != nil || len(notes) != 0 {
				t.Errorf("publishNotes = %+v, %v, want nothing", notes, err)
			}
		})
	}
}

// reviewCtx is what the observe core hands the next-phase source at a Stop of
// an unattended session on the fixture ticket, whose slices are all green
// (unless stale names one that is not) and whose journal holds lines. A verdict
// other than "" also records a gate round with it, reviewing publishHead.
func reviewCtx(t *testing.T, verdict, stale string, lines ...journal.Line) observe.NoteCtx {
	t.Helper()
	c := publishCtx(t, "clean", lines...)
	st := c.Store
	ticket := c.Where.Ticket
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range slices {
		state := "green"
		if s.ID == stale {
			state = "queued"
		}
		if err := st.WriteSliceState(ticket, s.ID, store.SliceState{State: state, Attempts: 1}); err != nil {
			t.Fatal(err)
		}
	}
	round := filepath.Join(st.TicketDir(ticket), "gate", "round-1")
	if verdict == "" {
		if err := os.RemoveAll(filepath.Join(st.TicketDir(ticket), "gate")); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(filepath.Join(round, "report.yaml"), []byte("round: 1\nverdict: "+verdict+"\nreviewed_sha:\n  fixture-repo: "+publishHead+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.Unattended = true
	c.Event.Session = "s-builder"
	return c
}

// TestNextPhaseSourceSendsAnUnattendedBuilderToTheGateAndASubagent: an
// unattended session that stops with every slice green and no round has the
// review note, enforced, naming the gate and, since it claimed a slice, a
// subagent and why. A session that did nothing on the ticket is told to review
// it itself, and one jig has no record of, that it cannot tell. After a round
// that found things, the note is for the next round.
func TestNextPhaseSourceSendsAnUnattendedBuilderToTheGateAndASubagent(t *testing.T) {
	t.Parallel()
	claim := journal.Line{Event: journal.EventClaim, Session: "s-builder", Slice: "a"}

	c := reviewCtx(t, "", "", claim)
	notes, err := nextPhaseNotes(c)
	if err != nil || len(notes) != 1 {
		t.Fatalf("nextPhaseNotes = %+v, %v, want the one review note", notes, err)
	}
	n := notes[0]
	for _, want := range []string{"`jig gate " + c.Where.Ticket + "`", "open the gate and review", "subagent", "claimed slice a", "not this session"} {
		if !strings.Contains(n.Text, want) {
			t.Errorf("note %q lacks %q", n.Text, want)
		}
	}
	if !n.Enforce || n.Ref != "review:1" {
		t.Errorf("note = %+v, want it enforced and named by the round to come", n)
	}

	fresh := reviewCtx(t, "", "")
	fresh.Event.Session = "s-other"
	if err := ledgerAppendSession(fresh, "s-other"); err != nil {
		t.Fatal(err)
	}
	if notes, err := nextPhaseNotes(fresh); err != nil || len(notes) != 1 || !strings.Contains(notes[0].Text, "review it in this session") || strings.Contains(notes[0].Text, "subagent") {
		t.Errorf("fresh session: nextPhaseNotes = %+v, %v, want the note that has it review in its own session", notes, err)
	}

	unknown := reviewCtx(t, "", "")
	unknown.Event.Session = "s-unseen"
	if notes, err := nextPhaseNotes(unknown); err != nil || len(notes) != 1 || !strings.Contains(notes[0].Text, "cannot tell whether this session built it") || !strings.Contains(notes[0].Text, "subagent") {
		t.Errorf("unknown session: nextPhaseNotes = %+v, %v, want the note that has a subagent review", notes, err)
	}

	again := reviewCtx(t, "fix-slices", "", claim)
	if notes, err := nextPhaseNotes(again); err != nil || len(notes) != 1 || notes[0].Ref != "review:2" {
		t.Errorf("after a round that found things: nextPhaseNotes = %+v, %v, want the note for round 2", notes, err)
	}
}

// ledgerAppendSession records in the machine ledger that session started: the
// harness's hooks saw it, which is what makes jig sure the session is fresh.
func ledgerAppendSession(c observe.NoteCtx, session string) error {
	return ledger.StartSession(c.JigHome, time.Now(), ledger.Registration{ID: session, Harness: "claude"})
}

// TestNextPhaseSourceNeverHoldsASessionThatIsNotUnattendedOrNotAtTheGate: each
// condition of the review note on its own - unattended, a Stop, every slice
// green, no round open, the last round not clean or none - is needed.
func TestNextPhaseSourceNeverHoldsASessionThatIsNotUnattendedOrNotAtTheGate(t *testing.T) {
	t.Parallel()
	claim := journal.Line{Event: journal.EventClaim, Session: "s-builder", Slice: "a"}
	cases := []struct {
		name    string
		verdict string
		stale   string
		mutate  func(c *observe.NoteCtx)
	}{
		{"an attended session, which is never held", "", "", func(c *observe.NoteCtx) { c.Unattended = false }},
		{"a slice is not green yet", "", "d", nil},
		{"the last round is clean on this head", "clean", "", nil},
		{"a tool call, not a stop", "", "", func(c *observe.NoteCtx) { c.Event.Kind = observe.KindTool }},
		{"a prompt, not a stop", "", "", func(c *observe.NoteCtx) { c.Event.Kind = observe.KindPrompt }},
		{"no ticket", "", "", func(c *observe.NoteCtx) { c.Where.Ticket = "" }},
		{"no store", "", "", func(c *observe.NoteCtx) { c.Store = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := reviewCtx(t, tc.verdict, tc.stale, claim)
			if tc.mutate != nil {
				tc.mutate(&c)
			}
			if notes, err := nextPhaseNotes(c); err != nil || len(notes) != 0 {
				t.Errorf("nextPhaseNotes = %+v, %v, want nothing", notes, err)
			}
		})
	}
}
