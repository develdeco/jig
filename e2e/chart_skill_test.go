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
	skillSection(t, out, "## Builds", "## Publish")
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
