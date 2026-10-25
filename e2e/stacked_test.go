package e2e

// End-to-end tests of a ticket stacked on another (ticket-graph 4, A3): jig
// names where the child's branch starts and where its pull request goes, the
// parent's branch in a repo they share and the target elsewhere, writes that
// target into the pull request body, and tells the child's session when the
// parent's branch moved (a restack it makes itself) or the parent merged (a
// retarget). jig rebases nothing.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// stackedNextOut is the part of `jig next <ticket> --json` these tests read.
type stackedNextOut struct {
	State string `json:"state"`
	Stack []struct {
		Repo         string `json:"repo"`
		Parent       string `json:"parent"`
		ParentBranch string `json:"parent_branch"`
		ParentHead   string `json:"parent_head"`
		Merged       bool   `json:"merged"`
		StartFrom    string `json:"start_from"`
		PRTarget     string `json:"pr_target"`
		Restack      bool   `json:"restack"`
		Retarget     bool   `json:"retarget"`
	} `json:"stack"`
	Ready []struct {
		Repo  string `json:"repo"`
		Start string `json:"start"`
	} `json:"ready"`
}

// parentBranch is the branch of the parent ticket JIG-2, titled "Parent".
const parentBranch = "JIG-2-parent"

// stackOnAParent adds the parent ticket JIG-2 ("Parent", one slice in alpha)
// to the fixture's store and stacks the fixture's JIG-1 on it, then has the
// harness cut the parent's branch in the clone with one commit in alpha. The
// clone is left on the parent's branch; the parent's head is returned.
func stackOnAParent(t *testing.T, fx *fixture.Fixture, h *fakeHarness) (*store.Store, string) {
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
	writeTicketYAML(t, fx, fx.Ticket, "schema_version: 1\nblocked_by:\n  - ticket: JIG-2\n    kind: stacked\n")
	h.Branch(parentBranch)
	head := h.Commit("p", "JIG-2 p: lay the ground", map[string]string{"alpha/PARENT.md": "the parent's ground\n"})
	return st, head
}

// commitOnParent commits a file on the parent's branch through a worktree of
// its own, as the parent's session would, leaving the clone's checkout (the
// child's branch) where it is. It returns the parent's new head.
func commitOnParent(t *testing.T, h *fakeHarness, file string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "parent")
	h.git("worktree", "add", "-q", dir, parentBranch)
	writeFileOrFatal(t, filepath.Join(dir, filepath.FromSlash(file)), []byte(file+"\n"))
	for _, args := range [][]string{{"add", "--", file}, {"commit", "-q", "-m", "JIG-2 p: more ground", "-m", "Jig-Slice: p"}} {
		if _, err := gitx.RunReplaceEnv(dir, h.gitEnv(), args...); err != nil {
			t.Fatalf("git %v in the parent's worktree: %v", args, err)
		}
	}
	head, err := gitx.RunReplaceEnv(dir, h.gitEnv(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	h.git("worktree", "remove", "--force", dir)
	return head
}

// TestStackedChildStartsFromParentBranch: a child stacked on a parent that has
// a branch starts from that branch, and its pull request goes into it, in the
// repo they share; in a repo only the child builds in, both are the target.
// `jig next` and `jig here` say so, and the child's pull request body, and the
// base `jig publish --body` names, are the parent's branch.
func TestStackedChildStartsFromParentBranch(t *testing.T) {
	t.Parallel()

	t.Run("next and here, in a shared repo and the child's own", func(t *testing.T) {
		t.Parallel()
		fx, env := newFixture(t, fixture.Opts{SecondRepo: true})
		h := newHarness(t, fx, env, "s-parent")
		st, head := stackOnAParent(t, fx, h)
		h.Push(parentBranch)
		if err := journal.Append(st, "JIG-2", journal.Line{Event: journal.EventPublishDone}); err != nil {
			t.Fatal(err)
		}

		n := nextJSON[stackedNextOut](t, env, fx.RepoDir, "JIG-1")
		if n.State != "ready" || len(n.Stack) != 2 {
			t.Fatalf("jig next JIG-1: state %q, stack %+v, want ready with a row for each repo", n.State, n.Stack)
		}
		shared, own := n.Stack[0], n.Stack[1]
		if shared.Repo != "fixture-repo" || shared.Parent != "JIG-2" || shared.ParentBranch != parentBranch || shared.ParentHead != head ||
			shared.StartFrom != parentBranch || shared.PRTarget != parentBranch || shared.Merged || shared.Restack {
			t.Errorf("fixture-repo = %+v, want it to start from and go into %s at %s", shared, parentBranch, head)
		}
		if own.Repo != "second-repo" || own.Parent != "" || own.StartFrom != "origin/main" || own.PRTarget != "main" {
			t.Errorf("second-repo = %+v, want origin/main and main", own)
		}
		starts := map[string]string{}
		for _, b := range n.Ready {
			starts[b.Repo] = b.Start
		}
		if want := map[string]string{"fixture-repo": parentBranch, "second-repo": "origin/main"}; !reflect.DeepEqual(starts, want) {
			t.Errorf("ready starts = %v, want %v", starts, want)
		}
		r := runJig(t, env, fx.RepoDir, "next", "JIG-1")
		for _, want := range []string{
			"stack[2]{repo,parent,start_from,pr_target}:\n  fixture-repo,JIG-2," + parentBranch + "," + parentBranch + "\n  second-repo,-,origin/main,main\n",
			"ready[2]{repo,slices,start}:\n  fixture-repo,a+c+d," + parentBranch + "\n  second-repo,e,origin/main\n",
		} {
			if !strings.Contains(r.Stdout, want) {
				t.Errorf("jig next JIG-1 lacks %q:\n%s", want, r.Stdout)
			}
		}
		if strings.Contains(r.Stdout, "note:") {
			t.Errorf("jig next JIG-1 has a note for a child that has not started:\n%s", r.Stdout)
		}

		// `jig here` on the child's branch, cut from the parent's, says the same.
		h.Branch("jig/" + fx.Ticket)
		here := runJig(t, env, fx.RepoDir, "here")
		if want := "stack[2]{repo,parent,start_from,pr_target}:\n  fixture-repo,JIG-2," + parentBranch + "," + parentBranch + "\n"; here.Code != 0 || !strings.Contains(here.Stdout, want) {
			t.Errorf("jig here lacks %q:\nexit %d\n%s%s", want, here.Code, here.Stdout, here.Stderr)
		}

		// A ticket stacked on nothing has no stack.
		if r := runJig(t, env, fx.RepoDir, "next", "JIG-2"); strings.Contains(r.Stdout, "stack[") {
			t.Errorf("jig next JIG-2 names a stack:\n%s", r.Stdout)
		}
	})

	t.Run("the pull request body", func(t *testing.T) {
		t.Parallel()
		fx, env := newFixture(t, fixture.Opts{})
		st, _ := stackOnAParent(t, fx, newHarness(t, fx, env, "s-parent"))
		if err := journal.Append(st, "JIG-2", journal.Line{Event: journal.EventPublishDone}); err != nil {
			t.Fatal(err)
		}
		// The child is built on the parent's branch, where the clone is.
		b := buildTicketOn(t, fx, env, true)
		b.openRound(t)
		b.reviewedClean(t)

		body := runJig(t, env, b.builder.Clone, "publish", fx.Ticket, "--body")
		if body.Code != 0 || !strings.Contains(body.Stdout, "base: "+parentBranch+"\n") || !strings.Contains(body.Stdout, "--base "+parentBranch) {
			t.Fatalf("jig publish --body exit = %d, want 0 and the base %s\nstdout:\n%s\nstderr:\n%s", body.Code, parentBranch, body.Stdout, body.Stderr)
		}
		text := string(readFileOrFatal(t, filepath.Join(st.TicketDir(fx.Ticket), "pr", "fixture-repo.md")))
		if want := "## Stacked on JIG-2\n\nJIG-1 builds on JIG-2: this pull request goes into `" + parentBranch + "`, the branch of JIG-2. Once JIG-2 merges, retarget it to `main`."; !strings.Contains(text, want) {
			t.Errorf("pr/fixture-repo.md lacks %q:\n%s", want, text)
		}
	})
}

// TestMovedParentGetsARestackNote: a child level with its parent's branch hears
// nothing of it; once the parent's branch gets a commit, the child's session is
// told, once, to restack onto the parent's head with a merge, and the merge
// that does it draws no note of its own.
func TestMovedParentGetsARestackNote(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-corr")
	stackOnAParent(t, fx, h)
	h.Branch("jig/" + fx.Ticket)
	h.Commit("a", "JIG-1 a: on the parent", map[string]string{"alpha/CHILD.md": "the child's work\n"})
	if res := h.Hook("SessionStart", map[string]any{"source": "startup"}); res.Code != 0 {
		t.Fatalf("SessionStart exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	// A first command, so the session watches where HEAD is from here on.
	h.Bash("git status")
	if res := h.Hook("UserPromptSubmit", map[string]any{"prompt": "go on"}); res.Code != 0 || strings.Contains(res.Stdout, "restack") {
		t.Fatalf("a child level with its parent: exit %d, stdout %q, want no restack", res.Code, res.Stdout)
	}

	moved := commitOnParent(t, h, "alpha/MORE.md")
	ctx := corrContext(t, h.Hook("UserPromptSubmit", map[string]any{"prompt": "and now"}))
	want := "JIG-1 is stacked on JIG-2, whose branch moved past it: restack onto `" + parentBranch + "` at `" + moved[:12] + "`: `git merge " + parentBranch + "` in fixture-repo"
	if !strings.Contains(ctx, want) {
		t.Fatalf("prompt context = %q, want %q", ctx, want)
	}
	var restacks []journal.Line
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventCorrection && l.Outcome == "restack" {
			restacks = append(restacks, l)
		}
	}
	if len(restacks) != 1 || restacks[0].Ref != "restack:JIG-2:fixture-repo:"+moved[:12] || restacks[0].Commit != moved || restacks[0].Session != "s-corr" {
		t.Fatalf("restack lines = %+v, want one for the parent's head %s, by s-corr", restacks, moved)
	}
	if refs := corrDelivered(t, env, "prompt"); !reflect.DeepEqual(refs, []string{restacks[0].Ref}) {
		t.Errorf("note-delivered refs = %v, want the restack once", refs)
	}
	if res := h.Hook("UserPromptSubmit", map[string]any{"prompt": "again"}); strings.Contains(res.Stdout, "restack") {
		t.Errorf("a second prompt was told the restack again: %q", res.Stdout)
	}
	if r := runJig(t, env, fx.RepoDir, "next", "JIG-1"); !strings.Contains(r.Stdout, "note: the branch of JIG-2 moved past JIG-1 in fixture-repo: restack onto `"+parentBranch+"` at `"+moved[:12]+"`") {
		t.Errorf("jig next JIG-1 lacks the restack note:\n%s", r.Stdout)
	}

	// The session merges the parent's branch in: no note for the merge, and
	// nothing left to restack.
	merge := h.Bash("git merge --no-edit " + parentBranch)
	if merge.Code != 0 {
		t.Fatalf("git merge exit = %d\nstdout:\n%s\nstderr:\n%s", merge.Code, merge.Stdout, merge.Stderr)
	}
	if strings.Contains(merge.Hook.Stdout, "restack") || strings.Contains(merge.Hook.Stdout, "outside every workspace") || strings.Contains(merge.Hook.Stdout, "names no slice") {
		t.Errorf("the merge's hook printed a correction: %q", merge.Hook.Stdout)
	}
	n := nextJSON[stackedNextOut](t, env, fx.RepoDir, "JIG-1")
	if len(n.Stack) != 1 || n.Stack[0].Restack || n.Stack[0].ParentHead != moved {
		t.Errorf("after the merge: stack = %+v, want level with %s", n.Stack, moved)
	}
}

// TestMergedParentRetargets: once the parent's work merged into the target,
// the child starts from and goes into the target again, `jig next` says so,
// and the child's session is told once to retarget its pull request and merge
// the target; a later session is not told again.
func TestMergedParentRetargets(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-corr")
	st, head := stackOnAParent(t, fx, h)
	h.Push(parentBranch)
	if err := journal.Append(st, "JIG-2", journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
	h.Branch("jig/" + fx.Ticket)
	h.Commit("a", "JIG-1 a: on the parent", map[string]string{"alpha/CHILD.md": "the child's work\n"})
	if n := nextJSON[stackedNextOut](t, env, fx.RepoDir, "JIG-1"); len(n.Stack) != 1 || n.Stack[0].PRTarget != parentBranch || n.Stack[0].Merged {
		t.Fatalf("before the parent merged: stack = %+v, want its pull request into %s", n.Stack, parentBranch)
	}

	// The parent's pull request merges, with a merge commit, as a host makes
	// one; the clone has the target as it now is.
	h.Branch("main")
	h.git("merge", "--no-ff", "--no-edit", parentBranch)
	h.Push("main")
	h.Branch("jig/" + fx.Ticket)

	n := nextJSON[stackedNextOut](t, env, fx.RepoDir, "JIG-1")
	if len(n.Stack) != 1 {
		t.Fatalf("after the parent merged: stack = %+v, want one row", n.Stack)
	}
	if r := n.Stack[0]; !r.Merged || r.StartFrom != "origin/main" || r.PRTarget != "main" || !r.Retarget || r.ParentHead != head {
		t.Errorf("after the parent merged: %+v, want merged, origin/main and main, and a retarget", r)
	}
	r := runJig(t, env, fx.RepoDir, "next", "JIG-1")
	for _, want := range []string{
		"stack[1]{repo,parent,start_from,pr_target}:\n  fixture-repo,JIG-2 (merged),origin/main,main\n",
		"note: JIG-2 merged in fixture-repo: retarget the PR of JIG-1 to main and merge main (`git merge origin/main`)",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig next JIG-1 lacks %q:\n%s", want, r.Stdout)
		}
	}

	if res := h.Hook("SessionStart", map[string]any{"source": "startup"}); res.Code != 0 {
		t.Fatalf("SessionStart exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	ctx := corrContext(t, h.Hook("UserPromptSubmit", map[string]any{"prompt": "go on"}))
	if want := "JIG-1 is stacked on JIG-2, and the parent merged in fixture-repo: retarget the PR to main and merge main (`git merge origin/main`)"; !strings.Contains(ctx, want) {
		t.Fatalf("prompt context = %q, want %q", ctx, want)
	}
	if res := h.Hook("UserPromptSubmit", map[string]any{"prompt": "again"}); strings.Contains(res.Stdout, "retarget") {
		t.Errorf("a second prompt was told the retarget again: %q", res.Stdout)
	}
	later := newHarness(t, fx, env, "s-later")
	if res := later.Hook("SessionStart", map[string]any{"source": "startup"}); res.Code != 0 {
		t.Fatalf("SessionStart of s-later exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	if res := later.Hook("UserPromptSubmit", map[string]any{"prompt": "hello"}); strings.Contains(res.Stdout, "retarget") {
		t.Errorf("a later session was told the retarget: %q", res.Stdout)
	}
	var retargets int
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventCorrection && l.Outcome == "retarget" {
			retargets++
		}
	}
	if retargets != 1 {
		t.Errorf("retarget lines = %d, want one", retargets)
	}
	if _, err := os.Stat(filepath.Join(fx.RepoDir, "alpha", "PARENT.md")); err != nil {
		t.Errorf("the child's checkout lost the parent's file: %v", err)
	}
}
