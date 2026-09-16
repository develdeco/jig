package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/develdeco/jig/axi"
	"github.com/develdeco/jig/fixture"
	makepkg "github.com/develdeco/jig/make"
	"github.com/develdeco/jig/store"
)

// TestSolveShouldPublish checks the pure gate on solve's fall-through to
// Publish: only a "clean" last verdict permits it. Before this helper
// existed, cmdSolve fell through to Publish unconditionally once the round
// loop ended, whether it ended by "clean" or by exhausting maxSolveRounds.
func TestSolveShouldPublish(t *testing.T) {
	if err := solveShouldPublish("clean"); err != nil {
		t.Fatalf("solveShouldPublish(clean) = %v, want nil", err)
	}

	err := solveShouldPublish("fix-slices")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "GATE_ROUNDS_EXHAUSTED" {
		t.Fatalf("solveShouldPublish(fix-slices) = %v, want *axi.Error GATE_ROUNDS_EXHAUSTED", err)
	}

	// An empty verdict (the loop never ran, e.g. maxSolveRounds == 0) must
	// also refuse rather than default to permitting publish.
	err = solveShouldPublish("")
	if !errors.As(err, &ae) || ae.Code != "GATE_ROUNDS_EXHAUSTED" {
		t.Fatalf("solveShouldPublish(\"\") = %v, want *axi.Error GATE_ROUNDS_EXHAUSTED", err)
	}
}

// TestReportExitCodeStalledOrEnvBlocked checks that reportExitCode (used by
// cmdSolve to decide whether to stop the run/gate chain) returns non-zero
// whenever the report's Stalled or EnvBlocked tables are non-empty, even
// when Stopped is false (a slice left over from an earlier invocation).
func TestReportExitCodeStalledOrEnvBlocked(t *testing.T) {
	cases := []struct {
		name   string
		report makepkg.RunReport
		want   int
	}{
		{"all-green", makepkg.RunReport{Green: []string{"a"}}, 0},
		{"stalled-not-stopped", makepkg.RunReport{Stalled: []string{"b"}}, 1},
		{"env-blocked-not-stopped", makepkg.RunReport{EnvBlocked: []string{"c"}}, 1},
		{"stopped", makepkg.RunReport{Stopped: true}, 1},
		{"pending-question-wins", makepkg.RunReport{Stalled: []string{"b"}, PendingQuestion: "q-1"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reportExitCode(c.report); got != c.want {
				t.Errorf("reportExitCode(%+v) = %d, want %d", c.report, got, c.want)
			}
		})
	}
}

// TestPrintRunReportStalledExitsNonZero is printRunReport's counterpart to
// TestReportExitCodeStalledOrEnvBlocked: `jig run` must not exit 0 on a
// ticket that already has stalled or env-blocked slices left over from a
// previous invocation, even though this call's own Stopped flag is false.
func TestPrintRunReportStalledExitsNonZero(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	var buf bytes.Buffer
	code := printRunReport(&buf, st, fx.Ticket, makepkg.RunReport{Stalled: []string{"b"}})
	if code != 1 {
		t.Fatalf("printRunReport with a stalled slice, Stopped=false: exit code = %d, want 1", code)
	}

	buf.Reset()
	code = printRunReport(&buf, st, fx.Ticket, makepkg.RunReport{EnvBlocked: []string{"c"}})
	if code != 1 {
		t.Fatalf("printRunReport with an env-blocked slice, Stopped=false: exit code = %d, want 1", code)
	}
}
