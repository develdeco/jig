package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// skillText runs the built binary for one embedded skill or reference and
// returns what a session reads.
func skillText(t *testing.T, args ...string) string {
	t.Helper()
	fx, env := newFixture(t, fixture.Opts{})
	r := runJig(t, env, fx.RepoDir, append([]string{"skill"}, args...)...)
	if r.Code != 0 {
		t.Fatalf("jig skill %s exit = %d\nstderr:\n%s", strings.Join(args, " "), r.Code, r.Stderr)
	}
	return r.Stdout
}

// wantAll fails for each sentence the text lacks, so a rule deleted from a
// skill or from the reference it moved to fails here.
func wantAll(t *testing.T, name, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q", name, want)
		}
	}
}

// TestReviewSkillCarriesItsRoundRules: the review core says how round 1 is
// made complete and points at the reference holding the later-round lines,
// and that reference holds them.
func TestReviewSkillCarriesItsRoundRules(t *testing.T) {
	t.Parallel()
	wantAll(t, "jig skill review", skillText(t, "review"),
		"or it lists open, answered or dismissed findings (`--prior`)",
		"as the context's `read` line says, in either scope",
		"A finding names the rule broken. Before filing, search the change for the same rule's other instances",
		"`--oracle` proves the rule across them",
		"An `--action ask` about a rule lays out every case it must decide, with one recommended answer.",
		"a search that must come back empty",
	)
	wantAll(t, "jig skill review rounds", skillText(t, "review", "rounds"),
		"--still-present",
		"`--prior r1-f2` for a changed version of a finding under `open`, `answers` or `dismissed`, `--still-present` for an unchanged one.",
	)
}

// TestBuildSkillCarriesItsReadAndFixRules: the build core and the handoff
// prompt say to read the function and not the file, and how a fix slice is
// proven before it is reviewed.
func TestBuildSkillCarriesItsReadAndFixRules(t *testing.T) {
	t.Parallel()
	wantAll(t, "jig skill build", skillText(t, "build"),
		"Read the function, not the file: find code with `grep -n` or the LSP",
		"Make independent reads in one step; an edit's result shows it applied.",
		"Fix the rule each finding names wherever its oracle reaches.",
		"mutate each term the fix adds (operand, condition, constant, return)",
		"add a test for each survivor",
	)
	wantAll(t, "jig skill build handoff", skillText(t, "build", "handoff"),
		"Read the function, not the file",
		"make independent reads in one step",
		"fixes the rule each finding names wherever its oracle reaches",
		"mutate each term the fix adds",
		"add a test for each survivor",
	)
}
