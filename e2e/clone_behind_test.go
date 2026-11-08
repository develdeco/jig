package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/clonebehind"
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

	pull := "git -C " + clonebehind.Quote(fx.RepoDir) + " pull --ff-only origin main"
	here = runJig(t, env, fx.RepoDir, "here")
	for _, want := range []string{"clone:\n", "  behind: 1\n", "  of: origin/main\n", "  pull: " + axi.Quote(pull) + "\n"} {
		if !strings.Contains(here.Stdout, want) {
			t.Errorf("jig here lacks %q:\n%s", want, here.Stdout)
		}
	}
	j := runJig(t, env, fx.RepoDir, "here", "--json")
	var got struct {
		Sections struct {
			Clone struct {
				Behind int    `json:"behind"`
				Of     string `json:"of"`
				Pull   string `json:"pull"`
			} `json:"clone"`
		} `json:"sections"`
	}
	if err := json.Unmarshal([]byte(j.Stdout), &got); err != nil || got.Sections.Clone.Behind != 1 || got.Sections.Clone.Of != "origin/main" || got.Sections.Clone.Pull != pull {
		t.Errorf("jig here --json clone = %+v (%v), want behind 1 of origin/main pull %q\n%s", got.Sections.Clone, err, pull, j.Stdout)
	}
	doctor = runJig(t, env, fx.RepoDir, "doctor")
	if !strings.Contains(doctor.Stdout, "clone is 1 commit behind origin/main (as of its last fetch): "+pull) {
		t.Errorf("jig doctor lacks the note:\n%s", doctor.Stdout)
	}

	gitLog(t, fx.RepoDir, "pull", "-q", "--ff-only", "origin", "main")
	here = runJig(t, env, fx.RepoDir, "here")
	doctor = runJig(t, env, fx.RepoDir, "doctor")
	if named(here) || named(doctor) {
		t.Errorf("a pulled clone is named:\nhere:\n%s\ndoctor:\n%s", here.Stdout, doctor.Stdout)
	}
}
