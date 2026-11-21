package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// TestLandVerbSharedTaskDependentIsNamedWithItsIssue: a task two issues share
// that waits for the task just landed is the next landing, named with the
// --issue it can land into.
func TestLandVerbSharedTaskDependentIsNamedWithItsIssue(t *testing.T) {
	r := sharedLandRig(t)
	r.ticket(t, "TK-8", "", false)
	r.ticket(t, "TK-9", "blocked_by:\n  - ticket: TK-8\n    kind: merged\n", false)
	r.chart(t, "plan", "issue: TK-1\ntickets:\n  - id: TK-2\n    title: Two\n  - id: TK-8\n    title: Eight\n  - id: TK-9\n    title: Nine\n", "")
	r.chart(t, "second", "issue: TK-7\ntickets:\n  - id: TK-2\n    title: Two\n  - id: TK-9\n    title: Nine\n", "")
	publish := func(id, into, url string) {
		t.Helper()
		for _, l := range []journal.Line{{Event: journal.EventPr, Into: into, URL: url}, {Event: journal.EventPublishDone, Into: into}} {
			if err := journal.Append(r.st, id, l); err != nil {
				t.Fatal(err)
			}
		}
	}
	publish("TK-8", "TK-1", "https://github.example/owner/repo/pull/8")
	publish("TK-9", "TK-1", "https://github.example/owner/repo/pull/9")
	publish("TK-9", "TK-7", "https://github.example/owner/repo/pull/19")
	rec, err := r.st.ReadTicket("TK-1")
	if err != nil {
		t.Fatal(err)
	}
	b1, err := r.st.ResolveTicketBranch("TK-1", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	rec7, err := r.st.ReadTicket("TK-7")
	if err != nil {
		t.Fatal(err)
	}
	b7, err := r.st.ResolveTicketBranch("TK-7", rec7, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t,
		fixture.GhPull{Number: 8, Base: b1, State: "closed", Merged: true},
		fixture.GhPull{Number: 9, Base: b1}, fixture.GhPull{Number: 19, Base: b7},
		fixture.GhPull{Number: 2, Base: b1}, fixture.GhPull{Number: 12, Base: b7}))
	out, code := r.run(t, "land", "TK-8")
	if code != 0 || !strings.Contains(out, "jig land TK-9 --issue TK-1") {
		t.Errorf("land TK-8: exit %d, want TK-9 named with the issue it can land into\n%s", code, out)
	}
	if out, code = r.run(t, "land", "TK-9", "--issue", "TK-1"); code != 0 || !strings.Contains(out, "gh pr merge https://github.example/owner/repo/pull/9") {
		t.Errorf("land TK-9 --issue TK-1: exit %d, want its merge named\n%s", code, out)
	}
}

// TestLandVerbDoesNotNameADependentAlreadyLanded: a dependent whose landing is
// on record is no next landing.
func TestLandVerbDoesNotNameADependentAlreadyLanded(t *testing.T) {
	r := landRig(t)
	r.host(t, true, true)
	if out, code := r.run(t, "land", "TK-3"); code != 0 {
		t.Fatalf("land TK-3: exit %d\n%s", code, out)
	}
	out, code := r.run(t, "land", "TK-2")
	if code != 0 || strings.Contains(out, "jig land TK-3") {
		t.Errorf("land TK-2 after TK-3 landed: exit %d, want TK-3 not named\n%s", code, out)
	}
}

// TestLandVerbSharedTaskDependentIsJudgedForTheLandingsIssue: a task landed into
// the second issue releases the shared dependent there, so that is the landing
// named, not the first issue's.
func TestLandVerbSharedTaskDependentIsJudgedForTheLandingsIssue(t *testing.T) {
	r := sharedLandRig(t)
	r.ticket(t, "TK-10", "", false)
	r.ticket(t, "TK-9", "blocked_by:\n  - ticket: TK-10\n    kind: merged\n", false)
	r.chart(t, "plan", "issue: TK-1\ntickets:\n  - id: TK-2\n    title: Two\n  - id: TK-9\n    title: Nine\n", "")
	r.chart(t, "second", "issue: TK-7\ntickets:\n  - id: TK-2\n    title: Two\n  - id: TK-9\n    title: Nine\n  - id: TK-10\n    title: Ten\n", "")
	publish := func(id, into, url string) {
		t.Helper()
		for _, l := range []journal.Line{{Event: journal.EventPr, Into: into, URL: url}, {Event: journal.EventPublishDone, Into: into}} {
			if err := journal.Append(r.st, id, l); err != nil {
				t.Fatal(err)
			}
		}
	}
	publish("TK-10", "TK-7", "https://github.example/owner/repo/pull/10")
	publish("TK-9", "TK-1", "https://github.example/owner/repo/pull/9")
	publish("TK-9", "TK-7", "https://github.example/owner/repo/pull/19")
	rec7, err := r.st.ReadTicket("TK-7")
	if err != nil {
		t.Fatal(err)
	}
	b7, err := r.st.ResolveTicketBranch("TK-7", rec7, "main")
	if err != nil {
		t.Fatal(err)
	}
	rec1, err := r.st.ReadTicket("TK-1")
	if err != nil {
		t.Fatal(err)
	}
	b1, err := r.st.ResolveTicketBranch("TK-1", rec1, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t,
		fixture.GhPull{Number: 10, Base: b7, State: "closed", Merged: true},
		fixture.GhPull{Number: 9, Base: b1}, fixture.GhPull{Number: 19, Base: b7}))
	out, code := r.run(t, "land", "TK-10")
	if code != 0 || !strings.Contains(out, "jig land TK-9 --issue TK-7") || strings.Contains(out, "jig land TK-9 --issue TK-1") {
		t.Errorf("land TK-10: exit %d, want TK-9 named for TK-7\n%s", code, out)
	}
}
