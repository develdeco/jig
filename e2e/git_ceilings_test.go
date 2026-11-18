package e2e

// A jig command asks git for what it needs and no more. This file holds the
// number of git processes each command the journeys run most may start, as a
// ceiling a test fails over, so a change that asks git the same thing over
// again shows up where it is made and not in the next suite's wall clock. It
// counts what git's own trace sees (every process, the ones git starts in turn
// included), the way internal/gitbudget reports it for the whole suite.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gittest"
)

// processCeilings is, per command, the most git processes one run of it may
// start: the count measured when git was last asked each question once per
// command, plus 2 (a small fixed slack, never a percentage). A raise needs its
// reason in the row, and says what the command now asks that it did not.
var processCeilings = map[string]struct {
	max int
	why string
}{
	"status": {2, "status reads the store and the branch from the files, and starts no git process"},
	"claim":  {9, "the identity, the config read for the writer, the claim line's add, check and commit, and the two ancestry questions of the claim check"},
	"verify": {9, "the identity, a worktree add and its checkout, a status, the two ancestry questions about the commit, and the one prune that takes the worktree away"},
	"gate":   {20, "the first lease's clone and checkout, the fetch of the branch (with the processes git starts for it), the restore at the copy and the one before the review, the scope diff and the hunks of the round, and the identity"},
	"setup":  {5, "the global and the system read of core.hooksPath, and the write of it"},

	"two-repo gate": {43, "what a gate does once for each repo, and each repo's remote URL and branch list once, not once per caller"},
}

// tracedHarness is h running its jig commands with git tracing into path.
func tracedHarness(h *fakeHarness, path string) *fakeHarness {
	c := *h
	c.env = h.env.with(gitTraceEnv + "=" + path)
	return &c
}

// checkCeiling fails the test when the git processes in trace are more than
// name's ceiling.
func checkCeiling(t *testing.T, name, trace string) []gittest.GitCall {
	t.Helper()
	calls := gittest.ReadTrace(t, trace)
	ceil, ok := processCeilings[name]
	if !ok {
		t.Fatalf("no process ceiling is held for %q", name)
	}
	t.Logf("%s: %d git processes (ceiling %d)", name, len(calls), ceil.max)
	if len(calls) > ceil.max {
		var lines []string
		for _, c := range calls {
			lines = append(lines, strings.Join(c.Argv[1:], " ")+"  in "+c.Dir)
		}
		t.Errorf("%s started %d git processes, over its ceiling of %d (%s):\n%s", name, len(calls), ceil.max, ceil.why, strings.Join(lines, "\n"))
	}
	return calls
}

// TestGitProcessCeilings runs the commands the journeys use most with git
// tracing on, and fails over a command's ceiling.
func TestGitProcessCeilings(t *testing.T) {
	t.Parallel()

	t.Run("one repository", func(t *testing.T) {
		t.Parallel()
		fx, env := newFixture(t, fixture.Opts{})
		dropEnvFromSlices(t, fx)
		h := newHarness(t, fx, env, "s-ceilings")
		ticket := fx.Ticket
		traces := t.TempDir()
		traceOf := func(name string) (*fakeHarness, string) {
			path := filepath.Join(traces, strings.ReplaceAll(name, " ", "-"))
			return tracedHarness(h, path), path
		}
		must := func(res jigResult, what string) {
			t.Helper()
			if res.Code != 0 {
				t.Fatalf("%s: exit %d\nstdout:\n%s\nstderr:\n%s", what, res.Code, res.Stdout, res.Stderr)
			}
		}

		th, path := traceOf("status")
		must(th.Jig("status", ticket), "jig status")
		checkCeiling(t, "status", path)

		th, path = traceOf("claim")
		must(th.Jig("claim", ticket, "--slice", "a"), "jig claim")
		checkCeiling(t, "claim", path)

		h.Branch(fx.Branch)
		h.CommitScenario("a", 1)

		th, path = traceOf("verify")
		must(th.Verify(ticket, "a"), "jig verify")
		checkCeiling(t, "verify", path)

		th, path = traceOf("gate")
		must(th.Jig("gate", ticket, "--early"), "jig gate")
		checkCeiling(t, "gate", path)

		path = filepath.Join(traces, "setup")
		must(runJig(t, env.with(gitTraceEnv+"="+path), fx.RepoDir, "setup", "--global", "--hooks", "--githook", "--harness", "claude", "--yes"), "jig setup")
		checkCeiling(t, "setup", path)
	})

	t.Run("two repositories", func(t *testing.T) {
		t.Parallel()
		b := buildOrchestratorTicket(t)
		path := filepath.Join(t.TempDir(), "gate")
		res := tracedHarness(b.api, path).Jig("gate", b.fx.Ticket)
		if res.Code != 0 {
			t.Fatalf("jig gate: exit %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
		}
		calls := checkCeiling(t, "two-repo gate", path)

		// Each repository's remote URL is asked of git once per command.
		asked := map[string]int{}
		for _, c := range calls {
			if !c.Child && strings.Contains(" "+strings.Join(c.Argv, " ")+" ", " get-url ") {
				asked[filepath.Clean(c.Dir)]++
			}
		}
		for dir, n := range asked {
			if n > 1 {
				t.Errorf("the gate asked git for the remote URL of %s %d times, want once", dir, n)
			}
		}
		if len(asked) == 0 {
			t.Log("the gate asked git for no remote URL")
		}
	})
}
