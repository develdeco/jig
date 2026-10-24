package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestStatusBriefBlockFollowsTheApproval: the brief block says the brief has
// no approval, then that it is the approved one with who approved it, then,
// once brief.md changes, that the approval is for another brief; the
// sections table names the slices citing each section.
func TestStatusBriefBlockFollowsTheApproval(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	status := func() string {
		t.Helper()
		out, err := RenderStatus(st, fx.Ticket)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := status(); !strings.Contains(out, "brief:\n  approval: none\n") || !strings.Contains(out, "  Slice A - failing clamp,a\n") {
		t.Fatalf("before an approval:\n%s", out)
	}

	sum, err := st.BriefSHA256(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	e := testEnv(fx.Home).withClock(fixedClock(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)))
	a, _, err := recordApproval(e, st, fx.Ticket, sum, "person")
	if err != nil {
		t.Fatal(err)
	}
	out := status()
	for _, want := range []string{"  approval: current\n", "  approved_by: " + a.By + "\n", "  approved_at: \"2026-10-23T12:00:00Z\"\n", "  provenance: person\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("after the approval, status lacks %q:\n%s", want, out)
		}
	}

	brief := filepath.Join(st.TicketDir(fx.Ticket), "brief.md")
	md, err := os.ReadFile(brief)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brief, append(md, []byte("\nOne more line.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := status(); !strings.Contains(out, "  approval: changed\n") {
		t.Errorf("after brief.md changed:\n%s", out)
	}
}

// TestRecordApprovalNamesTheSession: the approval line carries the session
// that recorded it, from JIG_SESSION.
func TestRecordApprovalNamesTheSession(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := st.BriefSHA256(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	e := envFrom(map[string]string{"JIG_HOME": fx.Home, "JIG_SESSION": "s-planner"})
	if _, _, err := recordApproval(e, st, fx.Ticket, sum, "agent"); err != nil {
		t.Fatal(err)
	}
	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.Event == journal.EventApproval {
			if l.Session != "s-planner" {
				t.Errorf("approval line session = %q, want s-planner", l.Session)
			}
			return
		}
	}
	t.Fatal("no approval line")
}
