package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// skillSection returns the part of a skill from the heading start up to the
// heading end (or its end when end is empty).
func skillSection(t *testing.T, text, start, end string) string {
	t.Helper()
	i := strings.Index(text, start)
	if i < 0 {
		t.Fatalf("the chart skill has no %q section:\n%s", start, text)
	}
	rest := text[i:]
	if end != "" {
		if j := strings.Index(rest, end); j >= 0 {
			rest = rest[:j]
		}
	}
	return rest
}

// TestChartSkillHandsOff runs the built binary and reads the chart skill a
// session gets: each brief goes to a drafting subagent while the main session
// keeps the questions, each publish goes to a subagent while the main session
// keeps the approvals, and the text stays within the skill budget.
func TestChartSkillHandsOff(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	r := runJig(t, env, fx.RepoDir, "skill", "chart")
	if r.Code != 0 {
		t.Fatalf("jig skill chart exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	out := r.Stdout
	briefs := skillSection(t, out, "## Briefs", "## Builds")
	publish := skillSection(t, out, "## Publish", "## Ends")
	builds := skillSection(t, out, "## Builds", "## Publish")
	skillSection(t, out, "## Ends", "")

	for _, want := range []string{"subagent", "intake", "never mint or approve", "jig validate <T> --check", "ADR 0035"} {
		if !strings.Contains(briefs, want) {
			t.Errorf("the Briefs section lacks %q:\n%s", want, briefs)
		}
	}
	for _, want := range []string{"subagent", "jig skill publish", "removing no worktree", "after its parent's hands back", "once for the group", "never force"} {
		if !strings.Contains(publish, want) {
			t.Errorf("the Publish section lacks %q:\n%s", want, publish)
		}
	}
	for _, want := range []string{
		"`isolation: worktree`", "you run no `add`", "`git branch -m <branch>`", "`git switch -c <branch>`", "`git reset --hard <base>`", "no worktree method",
		// A row in another clone than the session's repo is not the harness's to reach.
		"An `add` row in this session's repo (its `clone`): start the subagent with `isolation: worktree`",
		// The rarer rows are one pointer away.
		"`jig skill chart worktrees`",
		// The main session states each builder's case; the builder never infers it.
		"The prompt states the builder's case, which it never infers from where it finds itself.",
		// A wave starts in one step, and so do independent commands.
		"Start a wave's subagents in one step, and run independent commands in one step.",
		// A fix round proves the fix's new terms as the build skill says.
		"proving each term the fix adds as `jig skill build` says",
	} {
		if !strings.Contains(builds, want) {
			t.Errorf("the Builds section lacks %q:\n%s", want, builds)
		}
	}
	// The rarer worktree cases are read from the reference.
	ref := runJig(t, env, fx.RepoDir, "skill", "chart", "worktrees")
	if ref.Code != 0 {
		t.Fatalf("jig skill chart worktrees exit = %d\nstderr:\n%s", ref.Code, ref.Stderr)
	}
	for _, want := range []string{
		"A single-repo ticket's `add` row in another clone, or any `add` with no worktree method (no Agent isolation, no EnterWorktree): you run its `command`, one at a time, then start the subagent with no isolation.",
		// A multi-repo ticket's other clones are its nested builders'; its own subagent is isolated.
		"A multi-repo ticket's other clones are its nested builders'; its own subagent takes isolation for its row in this repo.",
		// A present row is worked in place, with no isolation.
		"`present`: no isolation.",
		"`no_clone`: tell the person; do not start it.",
	} {
		if !strings.Contains(ref.Stdout, want) {
			t.Errorf("the worktrees reference lacks %q:\n%s", want, ref.Stdout)
		}
	}
	// The tie is only in the isolated case: the no-isolation prompt names its
	// dir, says never to rename or reset, and carries no tie.
	const noIso = `with no isolation is: "`
	i := strings.Index(ref.Stdout, noIso)
	if i < 0 {
		t.Fatalf("the worktrees reference has no no-isolation prompt:\n%s", ref.Stdout)
	}
	isolated, bare := builds, ref.Stdout[i+len(noIso):]
	if j := strings.Index(bare, "\"\n"); j >= 0 {
		bare = bare[:j]
	}
	for _, want := range []string{"Work only in <dir>", "the row's `dir`", "never rename or reset"} {
		if !strings.Contains(bare, want) {
			t.Errorf("the no-isolation prompt lacks %q: %q", want, bare)
		}
	}
	for _, tie := range []string{"git branch -m", "git switch", "git reset --hard"} {
		if strings.Contains(bare, tie) {
			t.Errorf("the no-isolation prompt carries the tie %q: %q", tie, bare)
		}
		if !strings.Contains(isolated, "with isolation, is: \"Tie your worktree") || !strings.Contains(isolated, tie) {
			t.Errorf("the tie %q is not in the isolated prompt: %q", tie, isolated)
		}
	}
	if strings.Count(builds, "git branch -m") != 1 {
		t.Errorf("the tie `git branch -m` is in the Builds section %d times, want once (the isolated prompt)", strings.Count(builds, "git branch -m"))
	}
	if strings.Contains(out, "run the `add` commands") {
		t.Errorf("jig skill chart still has the main session run the add commands")
	}
	for _, want := range []string{"block: true", "`ends`", "none if the call returned one", "or as `jig gate` says", "Three prompts make a chart run", "asks nothing: permission settings decide", "a refused command hands its ticket back"} {
		if !strings.Contains(out, want) {
			t.Errorf("jig skill chart lacks %q", want)
		}
	}
	if n := len(out); n > 6000 {
		t.Errorf("jig skill chart is %d bytes, want at most 6000", n)
	}
	if n := strings.Count(out, "\n"); n > 100 {
		t.Errorf("jig skill chart is %d lines, want at most 100", n)
	}
}
