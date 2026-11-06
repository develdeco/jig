package verify

import (
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
)

// changedRig is a rig whose clone holds a branch that merged its target: the
// start sha is where the branch began, origin's target has moved on by a file
// (other_test.go) that the branch merged, and the branch itself added
// own_test.go, modified alpha.go and deleted gone.txt.
type changedRig struct {
	*rig
	start, head string
}

func newChangedRig(t *testing.T) *changedRig {
	t.Helper()
	r := newRig(t, fixture.Opts{})
	r.deps.Repo, r.deps.Target = repoName, "main"
	r.commit("", "seed", map[string]string{"gone.txt": "x"})
	if _, err := gitx.Run(r.fx.RepoDir, "fetch", "origin"); err != nil {
		t.Fatal(err)
	}
	start, err := gitx.RevParse(r.fx.RepoDir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Origin's target moves on: a commit beside the branch, set as origin/main.
	branch, err := gitx.Run(r.fx.RepoDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(r.fx.RepoDir, "checkout", "-b", "side", start); err != nil {
		t.Fatal(err)
	}
	moved := r.commit("", "main moves", map[string]string{"e2e/other_test.go": "package e2e"})
	if _, err := gitx.Run(r.fx.RepoDir, "checkout", strings.TrimSpace(branch)); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(r.fx.RepoDir, "update-ref", "refs/remotes/origin/main", moved); err != nil {
		t.Fatal(err)
	}
	// The branch's own work, then the integration merge of the target.
	if err := r.st.WriteStartSHA(r.fx.Ticket, repoName, start); err != nil {
		t.Fatal(err)
	}
	r.fixClamp()
	r.commit("a", "own test", map[string]string{"e2e/own_test.go": "package e2e"})
	if _, err := gitx.Run(r.fx.RepoDir, "rm", "-q", "gone.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(r.fx.RepoDir, "commit", "-q", "-m", "drop gone", "-m", "Jig-Slice: a"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(r.fx.RepoDir, "merge", "--no-edit", moved); err != nil {
		t.Fatal(err)
	}
	head, err := gitx.RevParse(r.fx.RepoDir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return &changedRig{rig: r, start: start, head: head}
}

func (c *changedRig) run() *run {
	return &run{d: c.deps, r: c.request("a", c.head), sha: c.head, dir: c.fx.RepoDir}
}

// TestChangedFilesReadsTheBranchAgainstTheGatesScopeBase: the files a verify
// run calls changed are what the branch added, modified or type-changed since
// merge-base(origin/<target>, commit) - the gate scope's base - so the files
// the target changed and the branch merged are not the branch's, and a
// deleted file is left out. With no target, or no origin ref, the recorded
// start sha is the base; with no base at all, nothing is known.
func TestChangedFilesReadsTheBranchAgainstTheGatesScopeBase(t *testing.T) {
	t.Parallel()
	c := newChangedRig(t)
	t.Run("the merge base with origin's target", func(t *testing.T) {
		got := c.run().changedFiles()
		if want := []string{"alpha/alpha.go", "e2e/own_test.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("changedFiles = %v, want %v: the branch's own adds and modifies, not the merged target's, no deletions", got, want)
		}
	})
	t.Run("no target: the recorded start sha", func(t *testing.T) {
		x := c.run()
		x.d.Target = ""
		got := x.changedFiles()
		if want := []string{"alpha/alpha.go", "e2e/other_test.go", "e2e/own_test.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("changedFiles = %v, want %v", got, want)
		}
	})
	t.Run("no origin ref: the recorded start sha", func(t *testing.T) {
		x := c.run()
		x.d.Target = "nowhere"
		got := x.changedFiles()
		if want := []string{"alpha/alpha.go", "e2e/other_test.go", "e2e/own_test.go"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("changedFiles = %v, want %v", got, want)
		}
	})
	t.Run("no base at all", func(t *testing.T) {
		x := c.run()
		x.d.Repo, x.d.Target = "", ""
		x.r.Ticket = "NO-START"
		if got := x.changedFiles(); got != nil {
			t.Fatalf("changedFiles = %v, want none", got)
		}
	})
}

// TestRunRecordsTheBranchsOwnScenariosFirst: a verify run builds the changed
// set itself, from the branch, and hands it to the recorder, so a run that
// holds more recordings than the limit keeps those of a test file the branch
// changed, not the first by name.
func TestRunRecordsTheBranchsOwnScenariosFirst(t *testing.T) {
	t.Parallel()
	c := newChangedRig(t)
	files := manyFiles(maxRecordings + 5)
	names := sortedNames(files)
	own := names[len(names)-2:]
	for _, name := range names {
		file := "e2e/other_test.go"
		if name == own[0] || name == own[1] {
			file = "e2e/own_test.go"
		}
		files[name+".json"] = `{"file":"` + file + `"}`
	}
	c.oracle.files = files
	res, err := Run(c.deps, c.request("a", c.head))
	if err != nil || res.State != journal.VerifyPass {
		t.Fatalf("Run = %+v, %v, want a pass", res, err)
	}
	var kept map[string]bool
	for _, l := range c.lines() {
		if l.Event == journal.EventRecorded {
			kept = map[string]bool{}
			for _, rec := range l.Recordings {
				kept[rec.File] = true
			}
			if len(l.Recordings) != maxRecordings {
				t.Fatalf("recorded %d recordings, want %d", len(l.Recordings), maxRecordings)
			}
		}
	}
	if kept == nil {
		t.Fatal("no recorded line")
	}
	for _, name := range own {
		if !kept[name] {
			t.Errorf("%s is the branch's own scenario and was left out", name)
		}
	}
}
