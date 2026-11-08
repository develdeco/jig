package e2e

// End-to-end tests of `jig depend` through the real binary: an edge written
// to a standalone ticket is read by `jig next` like one `jig graduate` wrote
// (the wait, the stack plan and the branch to merge), a merged edge cleared
// releases the ticket, and a refused edge leaves ticket.yaml as it was.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
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

func readTicketYAML(t *testing.T, fx *fixture.Fixture, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fx.StoreDir, "tickets", id, "ticket.yaml"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func branchHeads(h *fakeHarness) string {
	h.t.Helper()
	return h.git("for-each-ref", "--format=%(refname) %(objectname)", "refs/heads")
}

// TestDependStacksAStandaloneTicketOnABranch: a ticket stacked by `jig depend`
// waits for its parent, and its branch starts from the parent's branch.
func TestDependStacksAStandaloneTicketOnABranch(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-parent")
	dependParent(t, fx, h)

	r := runJig(t, env, fx.RepoDir, "depend", fx.Ticket, "--on", "JIG-2", "--kind", "stacked")
	if r.Code != 0 || !strings.Contains(r.Stdout, "result: recorded") {
		t.Fatalf("jig depend: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	n := nextJSON[dependNextOut](t, env, fx.RepoDir, fx.Ticket)
	if n.State != "blocked" || len(n.BlockedBy) != 1 || n.BlockedBy[0].Ticket != "JIG-2" || n.BlockedBy[0].Kind != "stacked" || n.BlockedBy[0].Fact != "it has not published" {
		t.Fatalf("jig next %s: state %q, blocked_by %+v, want blocked on JIG-2 (stacked) with its fact", fx.Ticket, n.State, n.BlockedBy)
	}
	if len(n.Stack) != 1 || n.Stack[0].Parent != "JIG-2" || n.Stack[0].StartFrom != parentBranch {
		t.Errorf("stack = %+v, want the branch to start from %s", n.Stack, parentBranch)
	}
	text := runJig(t, env, fx.RepoDir, "next", fx.Ticket).Stdout
	if want := "stack[1]{repo,parent,start_from,pr_target}:\n  fixture-repo,JIG-2," + parentBranch + "," + parentBranch + "\n"; !strings.Contains(text, want) {
		t.Errorf("jig next lacks %q:\n%s", want, text)
	}
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

// TestDependClearReleasesAMergedEdge: a merged edge holds the ticket in
// `jig next` until `jig depend --on --clear` removes it.
func TestDependClearReleasesAMergedEdge(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-parent")
	dependParent(t, fx, h)

	if r := runJig(t, env, fx.RepoDir, "depend", fx.Ticket, "--on", "JIG-2"); r.Code != 0 || !strings.Contains(r.Stdout, "result: recorded") {
		t.Fatalf("jig depend: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	n := nextJSON[dependNextOut](t, env, fx.RepoDir, fx.Ticket)
	if n.State != "blocked" || len(n.BlockedBy) != 1 || n.BlockedBy[0].Kind != "merged" || len(n.Stack) != 0 {
		t.Fatalf("with the edge: state %q, blocked_by %+v, stack %+v, want blocked by a merged edge and no stack", n.State, n.BlockedBy, n.Stack)
	}
	if r := runJig(t, env, fx.RepoDir, "depend", fx.Ticket, "--on", "JIG-2", "--clear"); r.Code != 0 || !strings.Contains(r.Stdout, "result: cleared") {
		t.Fatalf("jig depend --clear: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	n = nextJSON[dependNextOut](t, env, fx.RepoDir, fx.Ticket)
	if n.State != "ready" || len(n.BlockedBy) != 0 {
		t.Errorf("after the clear: state %q, blocked_by %+v, want ready and nothing blocking", n.State, n.BlockedBy)
	}
}

// TestDependRefusesACycleAndASelfEdge: both are refused, naming the chain,
// and ticket.yaml stays as it was.
func TestDependRefusesACycleAndASelfEdge(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-parent")
	dependParent(t, fx, h)
	if r := runJig(t, env, fx.RepoDir, "depend", fx.Ticket, "--on", "JIG-2"); r.Code != 0 {
		t.Fatalf("jig depend: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	child, parent := readTicketYAML(t, fx, fx.Ticket), readTicketYAML(t, fx, "JIG-2")

	r := runJig(t, env, fx.RepoDir, "depend", "JIG-2", "--on", fx.Ticket, "--kind", "stacked")
	if r.Code == 0 || !strings.Contains(r.Stdout, "VALIDATION_ERROR") || !strings.Contains(r.Stdout, "JIG-2 -> "+fx.Ticket+" -> JIG-2") {
		t.Errorf("a cycle: exit %d, want a refusal naming the chain\n%s", r.Code, r.Stdout)
	}
	r = runJig(t, env, fx.RepoDir, "depend", fx.Ticket, "--on", fx.Ticket)
	if r.Code == 0 || !strings.Contains(r.Stdout, "VALIDATION_ERROR") || !strings.Contains(r.Stdout, "cannot wait for itself") {
		t.Errorf("a self edge: exit %d, want a refusal\n%s", r.Code, r.Stdout)
	}
	if readTicketYAML(t, fx, fx.Ticket) != child || readTicketYAML(t, fx, "JIG-2") != parent {
		t.Errorf("a refusal changed a ticket.yaml")
	}
	if out, err := gitx.Run(fx.StoreDir, "status", "--porcelain"); err != nil || strings.TrimSpace(out) != "" {
		t.Errorf("the store has uncommitted changes after the refusals: %q, %v", out, err)
	}
}
