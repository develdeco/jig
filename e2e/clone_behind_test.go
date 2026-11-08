package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// TestCloneBehindIsNamedUntilPulled: a mapped clone one commit behind its
// origin's target is named by jig here and jig doctor with the exact pull,
// and after that pull neither names it.
func TestCloneBehindIsNamedUntilPulled(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	gitLog(t, fx.RepoDir, "fetch", "-q", "origin")
	gitLog(t, fx.RepoDir, "branch", "-q", "-u", "origin/main")

	named := func(r jigResult) bool { return strings.Contains(r.Stdout, "behind") }
	here := runJig(t, env, fx.RepoDir, "here")
	doctor := runJig(t, env, fx.RepoDir, "doctor")
	if named(here) || named(doctor) {
		t.Fatalf("an up-to-date clone is named:\nhere:\n%s\ndoctor:\n%s", here.Stdout, doctor.Stdout)
	}

	scriptedPush(t, fx.RepoRemote, "upstream.txt", "x\n", "upstream")
	gitLog(t, fx.RepoDir, "fetch", "-q", "origin")

	pull := "git -C " + fx.RepoDir + " pull --ff-only"
	here = runJig(t, env, fx.RepoDir, "here")
	for _, want := range []string{"clone:\n", "  behind: 1\n", "  of: origin/main\n", pull} {
		if !strings.Contains(here.Stdout, want) {
			t.Errorf("jig here lacks %q:\n%s", want, here.Stdout)
		}
	}
	if j := runJig(t, env, fx.RepoDir, "here", "--json"); !strings.Contains(j.Stdout, `"clone":{"behind":1,"of":"origin/main"`) {
		t.Errorf("jig here --json lacks the clone key:\n%s", j.Stdout)
	}
	doctor = runJig(t, env, fx.RepoDir, "doctor")
	if !strings.Contains(doctor.Stdout, "clone is 1 commit behind origin/main (as of its last fetch): "+pull) {
		t.Errorf("jig doctor lacks the note:\n%s", doctor.Stdout)
	}

	gitLog(t, fx.RepoDir, "pull", "-q", "--ff-only")
	here = runJig(t, env, fx.RepoDir, "here")
	doctor = runJig(t, env, fx.RepoDir, "doctor")
	if named(here) || named(doctor) {
		t.Errorf("a pulled clone is named:\nhere:\n%s\ndoctor:\n%s", here.Stdout, doctor.Stdout)
	}
}
