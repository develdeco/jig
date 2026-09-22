package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestGateReviewerNonTerminalTriage covers the non-terminal path of the
// gate reviewer's triage (design 6.4): a real jig subprocess (runJig
// never sets cmd.Stdin, so the child's stdin is not a terminal) runs a
// reviewer round through the fake backend and gets DefaultTriage - every
// fix kept, every workspace ask kept (no decision text), notes only
// listed - without a single prompt. TestGateReviewerRoundsThroughMain in
// cmd/jig covers the interactive terminal path (dismissing a fix, keeping
// an ask with a decision) that this scenario's round 1 also exercises.
func TestGateReviewerNonTerminalTriage(t *testing.T) {
	fx, _ := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	ticket := fx.Ticket

	r1 := runJig(t, fx.StoreDir, "run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r1.Code != 2 {
		t.Fatalf("run 1 exit = %d, want 2 (paused at q-001)\nstdout:\n%s\nstderr:\n%s", r1.Code, r1.Stdout, r1.Stderr)
	}
	r2 := runJig(t, fx.StoreDir, "run", ticket, "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r2.Code != 0 {
		t.Fatalf("run 2 (answer) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r2.Code, r2.Stdout, r2.Stderr)
	}

	// No --yes, no terminal: cmdGate's own triageFor must fall back to
	// DefaultTriage and print the non-terminal note line, never prompting.
	r3 := runJig(t, fx.StoreDir, "gate", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r3.Code != 0 {
		t.Fatalf("gate (round 1, non-terminal) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r3.Code, r3.Stdout, r3.Stderr)
	}
	if !strings.Contains(r3.Stdout, "verdict: fix-slices") {
		t.Fatalf("gate stdout missing verdict: fix-slices:\n%s", r3.Stdout)
	}
	// DefaultTriage keeps every fix (r1-f1 and r1-f2, in different
	// workspaces, so two separate fix-slice groups) and every workspace
	// ask (r1-f3) - nothing is dismissed without a human at the prompt.
	for _, want := range []string{"r1-f1,open,", "r1-f2,open,", "r1-f3,open,", "r1-f4,noted,"} {
		if !strings.Contains(r3.Stdout, want) {
			t.Fatalf("gate stdout missing %q:\n%s", want, r3.Stdout)
		}
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	for _, want := range []string{"fix-1-alpha-test", "fix-1-beta-test", "fix-1-r1-f3"} {
		if !hasSlice(slices, want) {
			t.Fatalf("expected slice %s from default (non-terminal) triage; got %+v", want, slices)
		}
	}
}
