package e2e

// End-to-end test of `jig depend` through the real binary: an edge to a
// ticket whose branch already exists is advice, not a branch moved, and `jig
// next` reads the ticket as behind with the merge that brings it up.

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// dependNextOut is the part of `jig next <ticket> --json` these tests read.
type dependNextOut struct {
	State     string `json:"state"`
	BlockedBy []struct {
		Ticket string `json:"ticket"`
		Kind   string `json:"kind"`
		Fact   string `json:"fact"`
	} `json:"blocked_by"`
	Stack []struct {
		Repo      string `json:"repo"`
		Parent    string `json:"parent"`
		StartFrom string `json:"start_from"`
	} `json:"stack"`
}

// dependParent adds the standalone parent ticket JIG-2 to the fixture's store
// and has the harness cut its branch in the clone with one commit.
func dependParent(t *testing.T, fx *fixture.Fixture, h *fakeHarness) *store.Store {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Parent"}); err != nil {
		t.Fatalf("CreateTicketRecord JIG-2: %v", err)
	}
	if err := st.AppendSlices("JIG-2", []store.Slice{{ID: "p", Workspace: "alpha", Goal: "Lay the ground.", Oracle: "test", BlockedBy: []string{}}}); err != nil {
		t.Fatalf("AppendSlices JIG-2: %v", err)
	}
	h.Branch(parentBranch)
	h.Commit("p", "JIG-2 p: lay the ground", map[string]string{"alpha/PARENT.md": "the parent's ground\n"})
	return st
}

func branchHeads(h *fakeHarness) string {
	h.t.Helper()
	return h.git("for-each-ref", "--format=%(refname) %(objectname)", "refs/heads")
}

// TestDependOnATicketWithABranchIsAdvice: stacking a ticket whose branch
// already exists from main touches no branch; the ticket reads as behind and
// says the merge that brings it up.
func TestDependOnATicketWithABranchIsAdvice(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-parent")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	h.Branch(fx.Branch)
	h.Commit("a", "JIG-1 a: work from main", map[string]string{"alpha/CHILD.md": "the child's work\n"})
	h.Push(fx.Branch)
	h.Branch("main")
	dependParent(t, fx, h)
	h.Push(parentBranch)
	for _, id := range []string{"JIG-2", fx.Ticket} {
		greenSlices(t, st, id)
		if err := journal.Append(st, id, journal.Line{Event: journal.EventPublishDone}); err != nil {
			t.Fatal(err)
		}
	}
	heads := branchHeads(h)

	r := runJig(t, env, fx.RepoDir, "depend", fx.Ticket, "--on", "JIG-2", "--kind", "stacked")
	if r.Code != 0 || !strings.Contains(r.Stdout, "already has a branch") {
		t.Fatalf("jig depend: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if got := branchHeads(h); got != heads {
		t.Errorf("jig depend moved a branch:\nbefore:\n%s\nafter:\n%s", heads, got)
	}
	n := nextJSON[dependNextOut](t, env, fx.RepoDir, fx.Ticket)
	if n.State != "behind" || len(n.Stack) != 1 || n.Stack[0].StartFrom != parentBranch {
		t.Fatalf("jig next %s: state %q, stack %+v, want behind, stacked on %s", fx.Ticket, n.State, n.Stack, parentBranch)
	}
	if text := runJig(t, env, fx.RepoDir, "next", fx.Ticket).Stdout; !strings.Contains(text, "`git merge "+parentBranch+"`") {
		t.Errorf("jig next lacks the merge of %s:\n%s", parentBranch, text)
	}
}
