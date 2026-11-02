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

// TestPublishedChildBehindItsMergedParentIsWork: a chart run ends with the
// child published into its parent's branch and the parent's pull request open,
// and the chart is done. Once the parent's pull request merged into main the
// child is owed a merge of main and a publish: the chart is ready again with the
// child as its only work, `jig next <child>` says it is behind and names the
// merge and the publish that follow, and the chart is done once the session has
// merged main into the child's branch.
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

	// Both are published and the parent has not merged: nothing is left to do.
	c := nextJSON[chartOut](t, env, fx.RepoDir, "c")
	if c.Ready || c.Code != "CHART_DONE" || len(c.Run) != 0 {
		t.Fatalf("parent unmerged: ready %v code %q run %v, want CHART_DONE", c.Ready, c.Code, c.Run)
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
	if !c.Ready || c.Code != "" || c.Prompt != "Continue c" || !reflect.DeepEqual(c.Run, [][]string{{"JIG-1"}}) {
		t.Fatalf("parent merged: ready %v code %q prompt %q run %v, want ready under Continue c with [[JIG-1]]", c.Ready, c.Code, c.Prompt, c.Run)
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
		"note: JIG-2 merged in fixture-repo: retarget the PR of JIG-1 to main and merge main (`git merge origin/main`), then run `jig publish JIG-1`",
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
	if c.Ready || c.Code != "CHART_DONE" || len(c.Run) != 0 {
		t.Errorf("after the merge: ready %v code %q run %v, want CHART_DONE", c.Ready, c.Code, c.Run)
	}
	if n := nextJSON[restackNextOut](t, env, fx.RepoDir, "JIG-1"); n.State != "done" {
		t.Errorf("jig next JIG-1 after the merge: state %q, want done", n.State)
	}
}
