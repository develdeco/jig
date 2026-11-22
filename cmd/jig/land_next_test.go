package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// TestLandVerbSharedTaskNamesOnlyTheNextLandingItReleased: after a landing,
// the next landing jig names is one that landing released and that runs. A task
// that waits for a shared task is named once the shared task merged into its
// own issue, not when only the other issue's merged; the container is named
// once it published and its tasks landed, and until then the child it waits for
// is named with its --issue.
func TestLandVerbSharedTaskNamesOnlyTheNextLandingItReleased(t *testing.T) {
	r := sharedLandRig(t)
	// TK-8 is a task of TK-1 that waits for nothing.
	r.ticket(t, "TK-8", "", false)
	r.green(t, "TK-8")
	r.chart(t, "plan", "issue: TK-1\ntickets:\n  - id: TK-2\n    title: Two\n  - id: TK-3\n    title: Three\n  - id: TK-8\n    title: Eight\n", "")
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Into: "TK-1", URL: "https://github.example/owner/repo/pull/8"},
		{Event: journal.EventPublishDone, Into: "TK-1"},
	} {
		if err := journal.Append(r.st, "TK-8", l); err != nil {
			t.Fatal(err)
		}
	}
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
	pulls := func(merged2, merged12 bool) {
		t.Helper()
		pull := func(n int, base string, merged bool) fixture.GhPull {
			p := fixture.GhPull{Number: n, Base: base}
			if merged {
				p.State, p.Merged = "closed", true
			}
			return p
		}
		t.Setenv("GH_STUB_PULLS", fixture.GhPulls(t, pull(2, b1, merged2), pull(3, b1, false), pull(8, b1, true), pull(12, b7, merged12),
			pull(21, "main", false), pull(27, "main", false)))
	}
	run := func(args ...string) string {
		t.Helper()
		out, code := r.run(t, args...)
		if code != 0 {
			t.Fatalf("jig %s: exit %d\n%s", strings.Join(args, " "), code, out)
		}
		return out
	}

	// Its container has not published: nothing is named for it.
	pulls(false, true)
	if out := run("land", "TK-8"); strings.Contains(out, "jig land TK-1") || strings.Contains(out, "TK-1 lands once") {
		t.Errorf("land TK-8 names a container that has not published:\n%s", out)
	}
	// Published, it waits for TK-2 (a shared task, open in TK-1) and TK-3: the
	// child that is named is the shared one, with its issue.
	for _, l := range []journal.Line{
		{Event: journal.EventPr, URL: "https://github.example/owner/repo/pull/21"},
		{Event: journal.EventPublishDone},
	} {
		if err := journal.Append(r.st, "TK-1", l); err != nil {
			t.Fatal(err)
		}
	}
	if out := run("land", "TK-8"); !strings.Contains(out, "jig land TK-2 --issue TK-1") {
		t.Errorf("land TK-8 again does not name the shared child with its issue:\n%s", out)
	}

	// Merged into TK-7 only: TK-3 (a task of TK-1 that waits for TK-2) is not
	// released, and the container TK-7 is not published.
	if out := run("land", "TK-2", "--issue", "TK-7"); strings.Contains(out, "jig land TK-3") {
		t.Errorf("land TK-2 --issue TK-7 names TK-3, which still waits for TK-1's merge:\n%s", out)
	}
	// Merged into TK-1 too: TK-3 is released.
	pulls(true, true)
	if out := run("land", "TK-2", "--issue", "TK-1"); !strings.Contains(out, "jig land TK-3") {
		t.Errorf("land TK-2 --issue TK-1 does not name TK-3, which it released:\n%s", out)
	}
}
