package verifydeliver

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestGateDoesNotReuseAnUncoveredPass: a run `jig verify` recorded but that
// covers no commit (a dirty tree, another command) is no run of the head's
// suite: with the right command, tree and env class and nothing else beside
// it, the gate still runs the oracle.
func TestGateDoesNotReuseAnUncoveredPass(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	rec := &oracleRecorder{}
	d.Oracle = rec.run
	head := commitInBuildLease(t, fx, map[string]string{"alpha/note.txt": "uncovered\n"})
	for _, ws := range []string{"alpha", "beta"} {
		line := journal.Line{Slice: "a", Event: journal.EventOracle, Outcome: "pass", Commit: head, Attempt: 1,
			Command: testCmd(t, fx, ws), Env: "rig", Uncovered: journal.UncoveredDirtyBefore}
		if err := journal.Append(d.Store, fx.Ticket, line); err != nil {
			t.Fatal(err)
		}
	}

	got := reusedByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
	if got["alpha"] != "" || got["beta"] != "" {
		t.Errorf("reused_from by workspace = %v, want both run: the recorded passes cover nothing", got)
	}
	if ran := rec.ran(); len(ran) != 2 {
		t.Errorf("the gate ran %q, want both oracles run", ran)
	}
}

// TestOracleRanSkipsAnUncoveredPass: the pull request body names the oracle
// runs of the reviewed head, and a pass that covers nothing is not one.
func TestOracleRanSkipsAnUncoveredPass(t *testing.T) {
	t.Parallel()
	dir, commit := lightRepo(t)
	pass := commit(map[string]string{".claude/jig.yaml": "oracles:\n  test: go test ./...\n", "a.go": "package a\n"})
	built := []store.Slice{{ID: "s", Oracle: "test", Workspace: "root"}}
	line := journal.Line{Event: journal.EventOracle, Outcome: "pass", Slice: "s", Commit: pass, Command: "go test ./..."}

	if commands, _ := oracleRan([]journal.Line{line}, built, dir, pass); len(commands["test"]) != 1 {
		t.Fatalf("commands = %v, want the covering pass named", commands)
	}
	line.Uncovered = journal.UncoveredOtherCmd
	if commands, _ := oracleRan([]journal.Line{line}, built, dir, pass); len(commands) != 0 {
		t.Errorf("commands = %v, want none: the pass covers nothing", commands)
	}
}
