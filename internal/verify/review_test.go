package verify

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// TestATreeGitCannotReadCoversNothing: a worktree whose status git cannot read
// is not known to be clean, so a run there covers nothing.
func TestATreeGitCannotReadCoversNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	if err := os.WriteFile(filepath.Join(r.fx.RepoDir, ".git", "index"), []byte("not an index"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(r.deps, r.request("a", sha))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Covered || res.Uncovered != journal.UncoveredDirtyBefore {
		t.Errorf("Result = %+v, want a run that covers nothing: the tree could not be read", res)
	}
	for _, l := range r.lines() {
		if l.Event == journal.EventVerified {
			t.Errorf("a run on an unreadable tree wrote %+v", l)
		}
	}
}

// TestAnUncoveredRunDoesNotParkTheSlice: an env class that will not come up
// parks a slice only for a run that could have covered its commit; a run that
// already could not (another command) leaves the slice and the journal alone.
func TestAnUncoveredRunDoesNotParkTheSlice(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{EnvFail: true})
	sha := r.commit("d", "d: rig smoke", map[string]string{"beta/smoke.txt": "x"})
	before := r.state("d")

	rq := r.request("d", sha)
	rq.Command = "echo exploring"
	res, err := Run(r.deps, rq)
	if err != nil || res.State != journal.VerifyError || res.Code != ExitUnrun {
		t.Fatalf("Run = %+v, %v, want an error: the env class is unavailable", res, err)
	}
	if len(r.lines()) != 0 || !reflect.DeepEqual(r.state("d"), before) {
		t.Errorf("an uncovered run left %v, state %+v, want nothing", events(r.lines()), r.state("d"))
	}

	// The run that could have covered still parks it.
	if _, err := Run(r.deps, r.request("d", sha)); err != nil {
		t.Fatal(err)
	}
	if st := r.state("d"); st.State != "env-blocked" {
		t.Errorf("slice d state = %+v, want env-blocked after a run that could cover", st)
	}
}
