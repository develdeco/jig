package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// TestSolveStopsOnNeedsHumanInsteadOfRedispatching pins F3 (digest Q1,
// lens1 M-3/lens3 M2/lens2 N5): `jig solve` must stop and report exit 2 the
// first time a gate round leaves an ask undecided (a finding on a file in
// no declared workspace, kept neither by --yes/no-terminal DefaultTriage
// nor by a human, per Q1), rather than re-dispatching the reviewer every
// remaining round up to the cap.
//
// Q10's compatibility rule ties `jig solve`'s scripted-source decision to
// --scenario alone, ignoring --backend (unlike `jig gate`): passing
// --scenario always selects the old scripted GateSource for solve, which
// can never produce NeedsHuman. To drive solve's real reviewer wiring
// (--backend fake, no --scenario) through Main with the fake session
// backend still able to find its scripted gate round, this test builds the
// ticket's slices to green first (with --scenario, via `jig run`, which has
// no such restriction), then runs the process's working directory at the
// scenario dir for the solve call itself: the fake backend's own scenario
// reads are relative (scenarioDir + "/gate/round-<n>/..."), so an empty
// ScenarioDir resolves against the process cwd exactly like a real
// ScenarioDir would.
func TestSolveStopsOnNeedsHumanInsteadOfRedispatching(t *testing.T) {
	prevTerm := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	defer func() { stdinIsTerminal = prevTerm }()

	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "reviewer-no-workspace"})
	ticket := fx.Ticket

	runArgs := []string{"run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	answerArgs := []string{"run", ticket, "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}

	out, code := runMain(t, "", runArgs...)
	if code != 2 {
		t.Fatalf("run 1 exit = %d, want 2 (paused at q-001)\n%s", code, out)
	}
	out, code = runMain(t, "", answerArgs...)
	if code != 0 {
		t.Fatalf("run (answer) exit = %d, want 0\n%s", code, out)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	if err := os.Chdir(fx.ScenarioDir); err != nil {
		t.Fatalf("chdir to scenario dir: %v", err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	solveArgs := []string{"solve", ticket, "--yes", "--backend", "fake", "--store", fx.StoreDir}
	out, code = runMain(t, "", solveArgs...)
	if code != 2 {
		t.Fatalf("solve exit = %d, want 2 (needs a human)\n%s", code, out)
	}
	if !strings.Contains(out, "needs_a_human") {
		t.Fatalf("solve output missing needs_a_human:\n%s", out)
	}
	if !strings.Contains(out, "r1-f1") {
		t.Fatalf("solve output missing the undecided ask r1-f1:\n%s", out)
	}
	if !strings.Contains(out, "round: 1") {
		t.Fatalf("solve output missing round: 1:\n%s", out)
	}

	// Only one gate round must have run: a second round would mean solve
	// re-dispatched the reviewer on the same pending human decision instead
	// of stopping.
	if _, err := os.Stat(filepath.Join(fx.StoreDir, ticket, "gate", "round-2")); err == nil {
		t.Fatalf("solve dispatched a second gate round instead of stopping at round 1's needs_a_human")
	}
}
