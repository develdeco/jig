package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestSolveStopsOnNeedsHumanInsteadOfRedispatching pins the rule that
// `jig solve` must stop and report exit 2 the
// first time a gate round leaves an ask undecided (a finding on a file in
// no declared workspace, kept neither by --yes/no-terminal DefaultTriage
// nor by a human), rather than re-dispatching the reviewer every
// remaining round up to the cap.
//
// The compatibility rule ties `jig solve`'s scripted-source decision to
// --scenario alone, ignoring --backend (unlike `jig gate`): passing
// --scenario always selects the old scripted GateSource for solve, which
// can never produce NeedsHuman. To drive solve's reviewer wiring
// (--backend fake, no --scenario) with the fake session backend still able
// to find its scripted gate round, this test builds the ticket's slices to
// green first (with --scenario, via `jig run`, which has no such
// restriction), then runs solve with no --scenario. A fake backend built
// with no ScenarioDir reads its scenario relative to the process's working
// directory, which a parallel test cannot change, so the solve gate source
// seam hands gateSourceForSolve - the real choice, which with no scenario
// gives the reviewer - a fake backend whose ScenarioDir is the scenario's
// absolute path.
func TestSolveStopsOnNeedsHumanInsteadOfRedispatching(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer-no-workspace"})
	e := testEnv(fx.Home).atTerminal()
	e.solveGateSource = func(scenario string, solves session.Backend) verifydeliver.GateSource {
		if scenario != "" {
			t.Errorf("solve handed its gate source scenario %q, want none (the test passes no --scenario)", scenario)
		}
		backend, err := session.New("fake", session.Options{ScenarioDir: fx.ScenarioDir})
		if err != nil {
			t.Fatalf("session.New(fake): %v", err)
		}
		// The backend solve built from --backend fake is the one the reviewer is
		// meant to run on; this swaps in one that can find the scenario, so check
		// that solve handed over a backend of that kind and not nil or another.
		if solves == nil || reflect.TypeOf(solves) != reflect.TypeOf(backend) {
			t.Errorf("solve handed its gate source backend %T, want the fake backend it built from --backend fake (%T)", solves, backend)
		}
		return gateSourceForSolve(scenario, backend)
	}
	ticket := fx.Ticket

	runArgs := []string{"run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	answerArgs := []string{"run", ticket, "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}

	out, code := runMain(t, e, "", runArgs...)
	if code != 2 {
		t.Fatalf("run 1 exit = %d, want 2 (paused at q-001)\n%s", code, out)
	}
	out, code = runMain(t, e, "", answerArgs...)
	if code != 0 {
		t.Fatalf("run (answer) exit = %d, want 0\n%s", code, out)
	}

	solveArgs := []string{"solve", ticket, "--yes", "--backend", "fake", "--store", fx.StoreDir}
	out, code = runMain(t, e, "", solveArgs...)
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
