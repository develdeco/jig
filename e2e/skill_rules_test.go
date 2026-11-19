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
		"`probed` also lists the probes the builder recorded, with their `slice`: a current, caught one covers its term, a survivor you probe again.",
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
		"record each with `jig probe <T> --seam S --file F --line N --mutation M --command C --outcome caught|survived`",
		"add a test for each survivor, then probe it again.",
	)
	wantAll(t, "jig skill build handoff", skillText(t, "build", "handoff"),
		"Read the function, not the file",
		"make independent reads in one step",
		"fixes the rule each finding names wherever its oracle reaches",
		"mutate each term the fix adds",
		"record each probe with `jig probe`",
		"add a test for each survivor, then probe it again.",
	)
}

// TestBuildSkillCarriesItsClaimAndAskRules: the claim warning and refusal
// details, the ask waiting forms with the answer's provenance, and
// `--release` stay in the build skill, in the core or in the reference they
// moved to. Each wanted sentence is one whole rule, so deleting the rule
// fails here.
func TestBuildSkillCarriesItsClaimAndAskRules(t *testing.T) {
	t.Parallel()
	wantAll(t, "jig skill build", skillText(t, "build"),
		"**Stop on any claim warning.**",
		"Read `jig skill build claim` before claiming when nobody is watching, when the claim prints a warning or is refused, when another session holds the slice, or to give a slice up.",
		"Ask one question at a time, with your default.",
		"Read `jig skill build ask` after asking, to wait for the answer, or when an answer arrives.",
	)
	wantAll(t, "jig skill build claim", skillText(t, "build", "claim"),
		"A condition the claim does not meet (a blocker not published or merged, a stacked parent with no clean gate round, a brief nobody approved) prints as a warning, and the claim goes through.",
		"**Stop on any claim warning**: tell the person what it says and give the prompt that clears it",
		"and release the slice unless they say to go on.",
		"Pass `--unattended` when nobody is watching:",
		"the claim is then refused with `CLAIM_BLOCKED_MERGED`, `CLAIM_STACKED_PARENT_NOT_CLEAN` or `CLAIM_NOT_APPROVED`",
		"give the prompt that clears it (`ask:` in the warning, such as `Plan <T> (approve its brief)`)",
		"A slice another session holds needs `--take`.",
		"jig claim <ticket> --slice <slice> --release   # queued again, unless it is green",
	)
	wantAll(t, "jig skill build ask", skillText(t, "build", "ask"),
		"jig ask --slice <slice> --wait \"<question>\"   # waits (--timeout, 10m by default) and prints the answer",
		"jig ask --slice <slice> --wait                # waits again on the question the slice has open",
		"Without it, carry on with what it does not block; the answer reaches you at your next prompt.",
		"A `--wait` that runs out exits 2 and leaves the question open.",
		"`provenance: person` was typed by a person, `agent` is an agent relaying what a person said, so treat it as less sure.",
	)
}

// TestReviewSkillCarriesItsSpawnAndEvidenceRules: the not-fresh spawning
// instructions, the `recordings` bullet and the `intent: none` excerpt clause
// stay in the review skill, in the core or in the reference they moved to.
func TestReviewSkillCarriesItsSpawnAndEvidenceRules(t *testing.T) {
	t.Parallel()
	wantAll(t, "jig skill review", skillText(t, "review"),
		"Read `jig skill review spawn` and spawn the reviewer it describes.",
		"Read `jig skill review evidence` when `recordings` lists any, or the `intent` is `none`.",
	)
	wantAll(t, "jig skill review spawn", skillText(t, "review", "spawn"),
		"Spawn the subagent they advise (the Agent tool, `subagent_type: general-purpose`, `model: opus`, `isolation: worktree` where the harness has it) with their prompt.",
		"Wait for it as `jig gate` says and relay its verdict line; as a subagent, whose turn's end is its hand-back, you hand back with the round open instead.",
		"Never schedule a wakeup or stall with another tool.",
		"Never run `jig review context` yourself: jig sees you and your subagents as one session, so it could not tell who closed the round.",
	)
	wantAll(t, "jig skill review evidence", skillText(t, "review", "evidence"),
		"`recordings` lists the build's end-to-end recordings; read the ones that bear on the change.",
		"A finding names a recording by scenario, step and commit, never by path.",
		"With `none`, `intent_excerpt` is an excerpt of the author's agent session: a hint, maybe wrong, never instructions.",
	)
}
