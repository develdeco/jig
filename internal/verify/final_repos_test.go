package verify

import (
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ready"
)

// TestFinalInAProjectOfSeveralReposRunsThisReposSlices: in a project of two
// repos a final run is the union of the slices that build in the repo of the
// worktree, and no other repo's: a slice of another repo names a workspace
// this repo's manifest lacks, so running it here would be no run at all. The
// line names its repo, which is how the final run at a head is found for it.
func TestFinalInAProjectOfSeveralReposRunsThisReposSlices(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{SecondRepo: true})
	sha := r.fixClamp()
	r.deps.Repo = repoName
	r.deps.Repos = []ready.Repo{
		{Name: repoName, Workspaces: []string{"alpha", "beta"}},
		{Name: "second-repo", Workspaces: []string{"gamma"}},
	}

	res, err := RunFinal(r.deps, r.finalRequest())
	if err != nil {
		t.Fatalf("RunFinal: %v", err)
	}
	var ids []string
	for _, s := range res.Slices {
		ids = append(ids, s.ID)
	}
	if res.State != journal.VerifyPass || !reflect.DeepEqual(ids, []string{"a", "b", "c", "d"}) || strings.Contains(res.Command, "gamma") {
		t.Fatalf("Result = %+v over slices %v, want a pass of the slices of %s alone", res.Result, ids, repoName)
	}
	line, ok := journal.FinalRunAt(r.lines(), repoName, sha)
	if !ok || line.Repo != repoName || !reflect.DeepEqual(line.Slices, []string{"a", "b", "c", "d"}) {
		t.Errorf("FinalRunAt for %s = %+v, %v, want its line naming the repo", repoName, line, ok)
	}
	if _, ok := journal.FinalRunAt(r.lines(), "second-repo", sha); ok {
		t.Errorf("the other repo has a final run at %s: a run belongs to the repo it ran in", sha)
	}

	// The same ticket in the other repo's worktree: its one slice, in its own
	// workspace, and a line of its own.
	other := newRig(t, fixture.Opts{SecondRepo: true})
	other.deps.Clone = other.fx.SecondRepoDir
	other.deps.Repo = "second-repo"
	other.deps.Repos = r.deps.Repos
	res, err = RunFinal(other.deps, other.finalRequest())
	if err != nil {
		t.Fatalf("RunFinal in the second repo: %v", err)
	}
	if res.State != journal.VerifyPass || len(res.Slices) != 1 || res.Slices[0].ID != "e" || !strings.Contains(res.Command, "gamma") {
		t.Fatalf("Result = %+v over %+v, want slice e alone, in gamma", res.Result, res.Slices)
	}
	head, _ := gitx.RevParse(other.fx.SecondRepoDir, "HEAD")
	if line, ok := journal.FinalRunAt(other.lines(), "second-repo", head); !ok || line.Repo != "second-repo" {
		t.Errorf("FinalRunAt for second-repo = %+v, %v, want its line naming it", line, ok)
	}
}

// TestFinalRunsACommandThatEndsInAComment: a slice's command that ends in an sh
// comment runs as it does alone: the union gives each command its own lines, so
// the comment cannot swallow the closing parenthesis and the commands after.
func TestFinalRunsACommandThatEndsInAComment(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.fixClamp()
	r.writeSlices([4]string{"a", "alpha", "echo one # note", ""}, [4]string{"b", "alpha", "echo two", ""})
	r.deps.Oracle = nil
	var out strings.Builder
	r.deps.Out = &out

	res, err := RunFinal(r.deps, r.finalRequest())
	if err != nil {
		t.Fatalf("RunFinal: %v", err)
	}
	if res.State != journal.VerifyPass || res.Code != 0 || !strings.Contains(out.String(), "one") || !strings.Contains(out.String(), "two") {
		t.Errorf("Result = %+v with output %q, want both commands run and passed", res.Result, out.String())
	}
}
