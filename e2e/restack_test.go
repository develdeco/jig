package e2e

// End-to-end tests of a published stacked ticket whose branch is behind
// (ADR 0038): its own standing says it is owed a merge and a publish, so a
// chart run keeps it among its work and `jig next <ticket>` names the merge
// and the publish that follow; the merge is the session's, and the publish
// finds the open pull request by its branch and points it at its base.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// restackChart is the chart c of the tests below: the parent and the child by
// id, as `jig graduate` leaves them.
const restackChart = `tickets:
  - id: JIG-2
    title: Parent
  - id: JIG-1
    title: Child
`

// restackNextOut is the part of `jig next <ticket> --json` these tests read.
type restackNextOut struct {
	State string `json:"state"`
	Phase *struct {
		Name   string `json:"name"`
		Ready  bool   `json:"ready"`
		Code   string `json:"code"`
		Prompt string `json:"prompt"`
	} `json:"phase"`
	Stack []struct {
		Repo     string `json:"repo"`
		Retarget bool   `json:"retarget"`
		Merge    string `json:"merge"`
		Publish  string `json:"publish"`
	} `json:"stack"`
}

// TestPublishedChildBehindItsMergedParentIsWork: a chart run reaches the
// child published into its parent's branch and the parent's pull request open.
// Nothing is left to build, but the chart is not done: it waits on that merge,
// with the child as the end (a screen that rereads a chart on a timer keeps
// reading it for as long as it lists one). Once the parent's pull request merged
// into main the child is owed a merge of main and a publish: the chart is ready
// again with the child as its only work, `jig next <child>` says it is behind
// and names the merge and the publish that follow, and the chart is done once the
// session has merged main into the child's branch.
func TestPublishedChildBehindItsMergedParentIsWork(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env, _ = ghOnPath(t, env.without("JIG_SLICE", "JIG_SESSION"))
	h := newHarness(t, fx, env, "s-chart")
	st, _ := stackOnAParent(t, fx, h)
	chartDir := filepath.Join(fx.StoreDir, "charts", "c")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(chartDir, "tickets.yaml"), []byte(restackChart))

	// The parent is published and pushed. The child was built on the parent's
	// branch and published into it.
	h.Push(parentBranch)
	parentPR, childPR := pullURL(7), pullURL(8)
	publishedWith(t, st, "JIG-2", parentPR)
	h.Branch(fx.Branch)
	h.Commit("a", "JIG-1 a: on the parent", map[string]string{"alpha/CHILD.md": "the child's work\n"})
	h.Push(fx.Branch)
	publishedWith(t, st, "JIG-1", childPR)
	env = withPulls(t, env, fixture.GhPull{Number: 7}, fixture.GhPull{Number: 8})

	// Both are published and the parent has not merged: nothing is left to build
	// and the chart waits for the parent's pull request, then for the retarget.
	c := nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if c.Ready || c.Code != "CHART_WAITS_MERGE" || c.Prompt != "Continue c" || len(c.Run) != 0 || !strings.Contains(c.Reason, parentPR) {
		t.Fatalf("parent unmerged: ready %v code %q prompt %q run %v reason %q, want CHART_WAITS_MERGE under Continue c naming %s", c.Ready, c.Code, c.Prompt, c.Run, c.Reason, parentPR)
	}
	if len(c.Ends) != 1 || c.Ends[0].Ticket != "JIG-1" || c.Ends[0].Blocker != "JIG-2" || c.Ends[0].Kind != "stacked" ||
		len(c.Ends[0].PRs) != 1 || c.Ends[0].PRs[0].URL != parentPR || c.Ends[0].PRs[0].State != "OPEN" {
		t.Errorf("ends = %+v, want JIG-1 stacked on JIG-2's open pull request", c.Ends)
	}
	for _, row := range c.Tickets {
		if row.Reach != "done" || row.Prompt != "" || len(row.WaitsFor) != 0 {
			t.Errorf("%s = reach %s prompt %q waits %v, want done with nothing to ask", row.ID, row.Reach, row.Prompt, row.WaitsFor)
		}
	}
	waiting := runJig(t, env, fx.RepoDir, "next", "c").Stdout
	for _, want := range []string{
		"code: CHART_WAITS_MERGE",
		"then its pull request is retargeted",
		"waits[1]{ticket,blocker,kind,pr,state,fact}:",
		"JIG-1,JIG-2,stacked,\"" + parentPR + "\",OPEN,",
		"Merge " + parentPR + ", then ask: Continue c",
	} {
		if !strings.Contains(waiting, want) {
			t.Errorf("jig next c lacks %q:\n%s", want, waiting)
		}
	}
	if n := nextJSON[restackNextOut](t, env, fx.RepoDir, "JIG-1"); n.State != "done" || n.Phase == nil || n.Phase.Code != "PHASE_DONE" {
		t.Fatalf("jig next JIG-1 with the parent unmerged: %+v, want done", n)
	}

	// The parent's pull request merges into main, with a merge commit, as a host
	// makes one; the child's branch lacks main's new head.
	h.Branch("main")
	h.git("merge", "--no-ff", "--no-edit", parentBranch)
	h.Push("main")
	h.Branch(fx.Branch)
	env = withPulls(t, env, fixture.GhPull{Number: 7, State: "closed", Merged: true}, fixture.GhPull{Number: 8})

	c = nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if !c.Ready || c.Code != "" || c.Prompt != "Continue c" || !reflect.DeepEqual(c.Run, [][]string{{"JIG-1"}}) || len(c.Ends) != 0 {
		t.Fatalf("parent merged: ready %v code %q prompt %q run %v ends %v, want ready under Continue c with [[JIG-1]] and nothing to wait for", c.Ready, c.Code, c.Prompt, c.Run, c.Ends)
	}
	for _, row := range c.Tickets {
		switch row.ID {
		case "JIG-1":
			if row.Phase != "publish" || !reflect.DeepEqual(row.Steps, []string{"done", "done", "done", "current"}) || row.Reach != "here" || row.Prompt != "Publish JIG-1" {
				t.Errorf("JIG-1 = phase %s steps %v reach %s prompt %q, want publish, done done done current, here, Publish JIG-1", row.Phase, row.Steps, row.Reach, row.Prompt)
			}
		case "JIG-2":
			if row.Reach != "done" || row.Prompt != "" {
				t.Errorf("JIG-2 = reach %s prompt %q, want done with nothing to ask", row.Reach, row.Prompt)
			}
		}
	}
	if status := runJig(t, env, fx.RepoDir, "status", "c"); status.Code != 0 || !strings.Contains(status.Stdout, "JIG-1,Child,dddc,here,") {
		t.Errorf("jig status c: exit %d, want JIG-1 at dddc\n%s", status.Code, status.Stdout)
	}

	n := nextJSON[restackNextOut](t, env, fx.RepoDir, "JIG-1")
	if n.State != "behind" || n.Phase == nil || n.Phase.Name != "publish" || !n.Phase.Ready || n.Phase.Code != "" || n.Phase.Prompt != "Publish JIG-1" {
		t.Fatalf("jig next JIG-1 = %+v, want it behind, at a publish that is ready", n)
	}
	if len(n.Stack) != 1 || !n.Stack[0].Retarget || n.Stack[0].Merge != "git merge origin/main" || n.Stack[0].Publish != "jig publish JIG-1" {
		t.Fatalf("stack = %+v, want one retarget row with the merge and the publish", n.Stack)
	}
	text := runJig(t, env, fx.RepoDir, "next", "JIG-1").Stdout
	for _, want := range []string{
		"state: behind",
		"phase: publish",
		"prompt: Publish JIG-1",
		"note: JIG-2 merged in fixture-repo: merge main (`git merge origin/main`), then run `jig publish JIG-1`, which points the PR of JIG-1 at main",
		"in fixture-repo run `git merge origin/main`, then `jig publish JIG-1`, which pushes the merge and points the open pull request at main",
		"a merge resolved by hand needs `jig gate JIG-1` first",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("jig next JIG-1 lacks %q:\n%s", want, text)
		}
	}

	// The session merges main into the child's branch: the chart is done again.
	h.git("merge", "--no-edit", "origin/main")
	c = nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if c.Ready || c.Code != "CHART_DONE" || len(c.Run) != 0 || len(c.Ends) != 0 {
		t.Errorf("after the merge: ready %v code %q run %v ends %v, want CHART_DONE", c.Ready, c.Code, c.Run, c.Ends)
	}
	if n := nextJSON[restackNextOut](t, env, fx.RepoDir, "JIG-1"); n.State != "done" {
		t.Errorf("jig next JIG-1 after the merge: state %q, want done", n.State)
	}
}

// stackedBuiltTicket is a built child ready for the merge chains below: JIG-1,
// built from newBuiltTicket's scenario on a branch cut from its parent's, JIG-2
// ("Parent", published), with round 1 over the whole change opened and closed
// clean. The fixture's repo is a GitHub repo for jig, with a fake gh, and the
// returned environment is the one to run jig in.
func stackedBuiltTicket(t *testing.T) (builtTicket, jigEnv, string) {
	t.Helper()
	fx, env := newFixture(t, fixture.Opts{})
	parent := newHarness(t, fx, env, "s-parent")
	st, _ := stackOnAParent(t, fx, parent)
	b := buildTicketOn(t, fx, env, true)
	ghEnv, log := githubClone(t, b)
	b.env, b.builder.env = ghEnv, ghEnv
	publishedWith(t, st, "JIG-2")
	b.openRound(t)
	b.reviewedClean(t, 1)
	return b, ghEnv, log
}

// mergeParentIntoMain has the parent's pull request merge, as a host makes the
// merge, and the target move on by two files of its own: the clone's main and
// origin's are then ahead of the child's branch, and the clone is back on it.
func mergeParentIntoMain(t *testing.T, b builtTicket) {
	t.Helper()
	h := b.builder
	h.Branch("main")
	h.git("merge", "--no-ff", "--no-edit", parentBranch)
	h.Commit("", "main moves on", map[string]string{"alpha/MAIN_A.md": "from main\n", "docs/MAIN_B.md": "also from main\n"})
	h.Push("main")
	h.Branch(b.branch)
}

// TestHandResolvedMergeGetsADeltaRound: the session merged the target into a
// built child's branch and resolved the merge by hand. A publish refuses the
// head as unreviewed; a gate round opens whose delta is the resolution alone
// (the base is a tree, git's own merge, and must_review names the edited file
// and none of what the target brought in); once that round is clean the publish
// goes through, pushing the merge commit.
func TestHandResolvedMergeGetsADeltaRound(t *testing.T) {
	t.Parallel()
	b, env, _ := stackedBuiltTicket(t)
	mergeParentIntoMain(t, b)
	h := b.builder

	// The session merges main, edits a file the merge touched, and commits.
	h.git("merge", "--no-ff", "--no-commit", "origin/main")
	writeFileOrFatal(t, filepath.Join(h.Clone, "alpha", "MAIN_A.md"), []byte("from main\nand a line of the session's\n"))
	h.git("add", "-A")
	h.git("commit", "-m", "merge main, by hand")
	merge := h.git("rev-parse", "HEAD")

	// A bare publish refuses the head: nobody reviewed the resolution.
	refused := runJig(t, env, h.Clone, "publish", b.fx.Ticket, "--yes")
	if refused.Code == 0 || !strings.Contains(refused.Stdout, "PUBLISH_UNREVIEWED_HEAD") {
		t.Fatalf("jig publish after a hand-resolved merge: exit %d, want PUBLISH_UNREVIEWED_HEAD\nstdout:\n%s\nstderr:\n%s", refused.Code, refused.Stdout, refused.Stderr)
	}
	if got := originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch); got != "" {
		t.Fatalf("the refused publish pushed %s", got)
	}

	// The gate opens round 2 over the resolution.
	gate := h.Jig("gate", b.fx.Ticket)
	if gate.Code != 0 || !strings.Contains(gate.Stdout, "round: 2") || !strings.Contains(gate.Stdout, "scope: delta") {
		t.Fatalf("jig gate: exit %d, want round 2 as a delta\nstdout:\n%s\nstderr:\n%s", gate.Code, gate.Stdout, gate.Stderr)
	}
	req := b.request(t, 2)
	if req.Scope != "delta" || req.HeadSHA != merge {
		t.Errorf("round 2 = scope %q head %s, want a delta at the merge %s", req.Scope, req.HeadSHA, merge)
	}
	if kind := gitLog(t, b.fx.RepoDir, "cat-file", "-t", req.BaseSHA); kind != "tree" {
		t.Errorf("base_sha %s is a %s, want a tree: git's own merge of the merge's parents", req.BaseSHA, kind)
	}
	if want := []string{"alpha/MAIN_A.md"}; !reflect.DeepEqual(req.MustReview, want) {
		t.Errorf("must_review = %v, want the edited file alone, %v", req.MustReview, want)
	}

	// A reviewer in a session of its own reports it clean, and the publish ships
	// the merge commit.
	b.reviewedClean(t, 2)
	pub := runJig(t, env, h.Clone, "publish", b.fx.Ticket, "--yes")
	if pub.Code != 0 {
		t.Fatalf("jig publish after the clean round: exit %d\nstdout:\n%s\nstderr:\n%s", pub.Code, pub.Stdout, pub.Stderr)
	}
	if got := originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch); got != merge {
		t.Errorf("origin's %s = %s, want the merge commit %s", b.branch, got, merge)
	}
}

// TestCleanMergePublishesAndRetargets: a built child published into its parent's
// branch, its pull request open from the branch. The parent merges and the
// session merges main into the child's branch, clean. The ticket is behind, and
// a bare publish needs no new gate round: it pushes the merge commit, finds the
// open pull request by its branch, and points it at main in the one edit, with
// no second pull request.
func TestCleanMergePublishesAndRetargets(t *testing.T) {
	t.Parallel()
	b, env, log := stackedBuiltTicket(t)
	h := b.builder
	h.Push(parentBranch)
	// The host holds the child's pull request, into the parent's branch.
	env = withPulls(t, env, fixture.GhPull{Number: 7, Repo: "fixture-repo", Owner: "acme", Head: b.branch, Base: parentBranch})
	prURL := pullURLIn("fixture-repo", 7)

	first := runJig(t, env, h.Clone, "publish", b.fx.Ticket, "--yes")
	if first.Code != 0 || !strings.Contains(first.Stdout, "updated") {
		t.Fatalf("the first jig publish: exit %d, want the open pull request updated\nstdout:\n%s\nstderr:\n%s", first.Code, first.Stdout, first.Stderr)
	}
	if got := originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch); got != b.sha {
		t.Fatalf("origin's %s = %s after the first publish, want %s", b.branch, got, b.sha)
	}
	if n := nextJSON[restackNextOut](t, env, b.fx.RepoDir, b.fx.Ticket); n.State != "done" {
		t.Fatalf("jig next after the first publish: state %q, want done", n.State)
	}

	// The parent merges into main: the ticket is behind. The session merges main
	// into the branch, clean; the branch then holds main's head, so the standing
	// reads done again while the pull request still goes into the parent's branch
	// (the host's word on its base is not read: the publish below fixes it).
	mergeParentIntoMain(t, b)
	if n := nextJSON[restackNextOut](t, env, b.fx.RepoDir, b.fx.Ticket); n.State != "behind" {
		t.Fatalf("jig next after the parent merged: state %q, want behind", n.State)
	}
	h.git("merge", "--no-ff", "--no-edit", "origin/main")
	merge := h.git("rev-parse", "HEAD")

	pub := runJig(t, env, h.Clone, "publish", b.fx.Ticket, "--yes")
	if pub.Code != 0 {
		t.Fatalf("the bare jig publish after a clean merge: exit %d, want 0 with no new round\nstdout:\n%s\nstderr:\n%s", pub.Code, pub.Stdout, pub.Stderr)
	}
	if !strings.Contains(pub.Stdout, "updated, retargeted to main") {
		t.Errorf("the pr_url table does not say the pull request was retargeted:\n%s", pub.Stdout)
	}
	if _, err := os.Stat(b.roundDir("2")); err == nil {
		t.Errorf("a gate round 2 exists: a clean merge needs no new round")
	}
	if got := originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch); got != merge {
		t.Errorf("origin's %s = %s, want the merge commit %s", b.branch, got, merge)
	}
	var retargets int
	for _, c := range ghCalls(t, log) {
		if strings.HasPrefix(c, "pr create") {
			t.Errorf("publish opened a second pull request: %q", c)
		}
		if strings.HasPrefix(c, "pr edit "+prURL) && strings.Contains(c, " --base main") {
			retargets++
			if !strings.Contains(c, "--body-file") {
				t.Errorf("the retarget %q is not the same edit as the body's", c)
			}
		}
	}
	if retargets != 1 {
		t.Errorf("pr edit calls with --base main = %d, want one: %v", retargets, ghCalls(t, log))
	}
	if n := nextJSON[restackNextOut](t, env, b.fx.RepoDir, b.fx.Ticket); n.State != "done" {
		t.Errorf("jig next after the publish: state %q, want done", n.State)
	}
}
