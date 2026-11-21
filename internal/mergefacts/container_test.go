package mergefacts

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// TestContainerMergeFollowsTheBase: the work of a ticket that has a container
// has merged, by its branch tip, when the tip is in the container's branch on
// the origin, not in the target; a ticket with no container is judged by the
// target as before.
func TestContainerMergeFollowsTheBase(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	st := &store.Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("I-1", store.Ticket{Title: "Issue", Kind: store.KindIssue}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("JIG-1", store.Ticket{Title: "First"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AtomicWrite(st.ChartFile("c"), []byte("issue: I-1\ntickets:\n  - id: JIG-1\n    title: First\n")); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(st, "JIG-1", journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
	rec, _ := st.ReadTicket("I-1")
	issueBranch, err := st.ResolveTicketBranch("I-1", rec, "main")
	if err != nil {
		t.Fatal(err)
	}

	// The container's branch starts at main; the task's branch is off main.
	mustGit(t, r.clone, "branch", issueBranch, "main")
	mustGit(t, r.clone, "push", "origin", issueBranch)
	r.branch(t, "JIG-1-first")

	// Merged into the target only: the container's branch lacks it.
	mustGit(t, r.clone, "merge", "--no-ff", "-m", "merge", "JIG-1-first")
	mustGit(t, r.clone, "push", "origin", "main")
	if m, err := reader(st, r, nil).Of("JIG-1"); err != nil || m.Met() {
		t.Fatalf("merged into the target, not the container: %+v, %v, want not met", m, err)
	}

	// Merged into the container's branch.
	mustGit(t, r.clone, "switch", issueBranch)
	mustGit(t, r.clone, "merge", "--no-ff", "-m", "merge", "JIG-1-first")
	mustGit(t, r.clone, "push", "origin", issueBranch)
	mustGit(t, r.clone, "switch", "main")
	m, err := reader(st, r, nil).Of("JIG-1")
	if err != nil || !m.Met() || m.Via() != ready.ByAncestry {
		t.Fatalf("merged into the container: %+v, %v, want met by ancestry", m, err)
	}

	// No container (no chart lists it): the target decides, as it always did.
	if err := store.AtomicWrite(st.ChartFile("c"), []byte("issue: I-1\ntickets: []\n")); err != nil {
		t.Fatal(err)
	}
	m, err = reader(st, r, nil).Of("JIG-1")
	if err != nil || !m.Met() {
		t.Errorf("no container, merged into the target: %+v, %v, want met", m, err)
	}
}

// TestContainerMergeRecordedPullRequestStillDecides: a pull request the host
// calls merged is the word whatever its base was.
func TestContainerMergeRecordedPullRequestStillDecides(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	st := &store.Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("I-1", store.Ticket{Title: "Issue", Kind: store.KindIssue}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("JIG-1", store.Ticket{Title: "First"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AtomicWrite(st.ChartFile("c"), []byte("issue: I-1\ntickets:\n  - id: JIG-1\n    title: First\n")); err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/acme/app/pull/7"
	for _, l := range []journal.Line{{Event: journal.EventPr, URL: url}, {Event: journal.EventPublishDone}} {
		if err := journal.Append(st, "JIG-1", l); err != nil {
			t.Fatal(err)
		}
	}
	prs := &fakePRs{reads: map[string]PRRead{url: {State: ready.PRMerged}}}
	m, err := reader(st, r, prs).Of("JIG-1")
	if err != nil || !m.Met() {
		t.Errorf("a merged pull request into the container's branch: %+v, %v, want met", m, err)
	}
}
