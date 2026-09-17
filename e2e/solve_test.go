package e2e

import (
	"testing"

	"github.com/develdeco/jig/fixture"
	"github.com/develdeco/jig/gitx"
)

// TestSolveOneProcess drives `jig solve` end to end in exactly two process
// invocations: the first pauses once on slice c's scripted question, the
// second answers it and rides the chain (run -> gate -> fix round -> gate
// clean -> publish) to completion in one call.
//
// NOTE: the CLI surface table in CONTRACTS.md lists only --yes/--answer for
// solve, but DoD-5 cannot run deterministically without threading the fake
// backend through solve's internal run/gate calls the same way the run and
// gate subcommands take them directly. This test assumes solve also accepts
// --backend/--scenario; see the package doc in main_test.go.
//
// NOTE: this chain reaches gate/publish oracle execution, so on a machine
// where Go lives under a space-containing path it hits the same
// envrun.Shell / cmd-quoting bug documented at length in e2e_test.go's
// runEndToEndOnce; see that comment before treating a failure here as a
// solve-specific regression.
func TestSolveOneProcess(t *testing.T) {
	fx, _ := newFixture(t, fixture.Opts{})

	r1 := runJig(t, fx.StoreDir, "solve", fx.Ticket, "--yes", "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r1.Code != 2 {
		t.Fatalf("first jig solve exit = %d, want 2 (paused once)\nstdout:\n%s\nstderr:\n%s", r1.Code, r1.Stdout, r1.Stderr)
	}

	r2 := runJig(t, fx.StoreDir, "solve", fx.Ticket, "--yes", "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r2.Code != 0 {
		t.Fatalf("second jig solve exit = %d, want 0 (chain completed to publish)\nstdout:\n%s\nstderr:\n%s", r2.Code, r2.Stdout, r2.Stderr)
	}

	if _, err := gitx.Run(fx.RepoRemote, "rev-parse", "--verify", "refs/heads/jig/"+fx.Ticket); err != nil {
		t.Fatalf("expected published branch jig/%s on the repo remote: %v", fx.Ticket, err)
	}
}
