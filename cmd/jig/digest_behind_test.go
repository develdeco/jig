package main

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
)

// TestTheRosterSaysATicketOwedAMergeIsBehind: a ticket that published, is
// stacked on a parent that has merged and whose branch lacks the target's head
// is behind in `jig next <ticket>` and in the roster's digest, from the one rule;
// every other row agrees with `jig next <ticket>` on it.
func TestTheRosterSaysATicketOwedAMergeIsBehind(t *testing.T) {
	t.Parallel()
	r, merges := digestRig(t)
	r.ticket(t, "TK-14", "blocked_by:\n  - ticket: TK-12\n    kind: stacked\n", true)
	r.green(t, "TK-14")
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Outcome: "opened", URL: "https://github.com/acme/app/pull/30"},
		{Event: journal.EventPublishDone},
	} {
		if err := journal.Append(r.st, "TK-14", l); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := project.LoadStoreConfig(r.root)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := r.st.ReadTicket("TK-14")
	if err != nil {
		t.Fatal(err)
	}
	branch, err := r.st.ResolveTicketBranch("TK-14", rec, "main")
	if err != nil {
		t.Fatal(err)
	}
	// The clone: the ticket's branch holds a commit the target's head, which
	// moved on, does not have.
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, dir, "checkout", "-q", "-b", branch)
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "ticket")
	gitIn(t, dir, "checkout", "-q", "main")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "target moved")
	gitIn(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	mp := project.MachineProject{Clones: map[string]string{"app": dir}}

	fleet, err := nextForStore(r.st, cfg, mp, merges)
	if err != nil {
		t.Fatal(err)
	}
	behind := 0
	for _, row := range fleet.Tickets {
		n, err := nextForTicket(r.st, cfg, mp, row.ID, "", false, "", merges)
		if err != nil {
			t.Fatal(err)
		}
		if want := n.State == stateBehind; row.Digest.Behind != want {
			t.Errorf("%s: the digest says behind %v, jig next says state %s", row.ID, row.Digest.Behind, n.State)
		}
		if row.Digest.Behind {
			behind++
		}
	}
	if behind != 1 {
		t.Errorf("%d rows are behind, want TK-14 alone", behind)
	}
}
