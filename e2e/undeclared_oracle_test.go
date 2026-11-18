package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// TestAnUndeclaredOracleNameRunsAsALiteralCommand: a repo whose jig.yaml
// declares no oracles has no `test` oracle, whatever it holds, so a slice that
// names `test` runs the shell's own `test` with no argument: it exits 1 at once,
// no suite runs, and the run is a fail whose command is `test`. jig adds no
// refusal; the slice names a command the repo never declared.
func TestAnUndeclaredOracleNameRunsAsALiteralCommand(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{ScenarioBranch: "clean-round"})
	dropEnvFromSlices(t, fx)
	h := newHarness(t, fx, env, "s-undeclared")
	h.Branch(fx.Branch)
	sha := h.Commit("a", "declare no oracles", map[string]string{
		".claude/jig.yaml": "workspaces:\n  - id: alpha\n    path: alpha\n  - id: beta\n    path: beta\n",
	})
	h.Push(fx.Branch)

	res := h.Verify(fx.Ticket, "a")
	if res.Code != 1 || !strings.Contains(res.Stdout, "state: fail") {
		t.Fatalf("jig verify exit = %d, want 1 and a failure\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	line, ok := oracleLine(readJournal(t, fx, fx.Ticket), "a", sha, "fail")
	if !ok {
		t.Fatalf("no failed oracle line for %s", sha)
	}
	if line.Command != "test" {
		t.Errorf("oracle line command = %q, want the literal %q", line.Command, "test")
	}
	if strings.Contains(res.Stdout, "go test") {
		t.Errorf("a suite ran for an undeclared oracle:\n%s", res.Stdout)
	}
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventVerified {
			t.Errorf("a failed verification wrote %+v", l)
		}
	}
}
