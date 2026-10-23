package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/observe"
	"github.com/develdeco/jig/internal/store"
)

const publishHead = "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d"

// publishCtx is what the observe core hands the publish source at a Stop on
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
		registered = registered || s.Name == "publish"
	}
	if !registered {
		t.Fatalf("note sources = %+v, want publish among them", observe.NoteSources())
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
