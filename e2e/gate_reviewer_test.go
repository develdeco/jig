package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// recordRound records one gate round through the jig binary the way a
// reviewer's agent does: `jig gate` (with openFlags) opens it and `jig review
// done --result` closes it with the report in the scenario's file for the
// round (gate/round-<n>/review-result.json). It returns what `review done`
// printed; the opening must succeed.
func recordRound(t *testing.T, env jigEnv, fx *fixture.Fixture, n string, openFlags ...string) jigResult {
	t.Helper()
	args := append([]string{"gate", fx.Ticket}, openFlags...)
	if open := runJig(t, env, fx.StoreDir, args...); open.Code != 0 {
		t.Fatalf("gate (round %s) exit = %d, want 0 (the round opens)\nstdout:\n%s\nstderr:\n%s", n, open.Code, open.Stdout, open.Stderr)
	}
	result := filepath.Join(fx.ScenarioDir, "gate", "round-"+n, "review-result.json")
	return runJig(t, env, fx.StoreDir, "review", "done", fx.Ticket, "--result", result)
}

// TestGateReviewerNonTerminalTriage covers the unprompted path of the
// recorded gate round: a real jig subprocess (stdin is not a terminal, and
// jig prompts at none) closes a reviewer's round opened with `jig gate --yes`
// and gets DefaultTriage - every fix kept, every workspace ask kept (no
// decision text), notes only listed - without a single prompt.
// TestRecordedGateRoundsThroughMain in cmd/jig covers the same rounds
// in-process (the refused report, the delta scope, the recurrence), and
// TestOutstandingAskIsDecidedWithJigTriageAndAppliedByTheNextRound the ask a
// person decides; this test needs its own "reviewer-nonterminal" branch
// (identical except the beta.go trim finding is risk medium, not low) so
// that DefaultTriage still has two fix findings to keep without a human -
// a low-risk fix never reaches triage at all under the default risk
// floor, which the "reviewer" branch's own low-risk finding is there to
// pin, not this one.
func TestGateReviewerNonTerminalTriage(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer-nonterminal"})
	ticket := fx.Ticket
	// The ticket built by hand: the builder session committed the scenario's
	// patches in the operator clone and had each commit verified.
	b := buildTicketOn(t, fx, env, true)

	// --yes, no terminal: the round is recorded with DefaultTriage, never
	// prompting.
	r3 := recordRound(t, env, fx, "1", "--yes")
	if r3.Code != 0 {
		t.Fatalf("review done (round 1) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r3.Code, r3.Stdout, r3.Stderr)
	}
	if !strings.Contains(r3.Stdout, "verdict: fix-slices") {
		t.Fatalf("review done stdout missing verdict: fix-slices:\n%s", r3.Stdout)
	}
	// DefaultTriage keeps every fix (r1-f1 and r1-f2, in different
	// workspaces, so two separate fix-slice groups) and every workspace
	// ask (r1-f3) - nothing is dismissed without a human at the prompt.
	for _, want := range []string{"r1-f1,open,", "r1-f2,open,", "r1-f3,open,", "r1-f4,noted,"} {
		if !strings.Contains(r3.Stdout, want) {
			t.Fatalf("review done stdout missing %q:\n%s", want, r3.Stdout)
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
			t.Fatalf("expected slice %s from default (unprompted) triage; got %+v", want, slices)
		}
	}

	// The documented default path keeps every fix, so fix-1-beta-test is not
	// merely appended to slices.yaml, it is the ticket's own queued work: the
	// builder must actually be able to take it to green through this
	// fixture, the same as fix-1-alpha-test and fix-1-r1-f3. Without a
	// scripted attempt for it the slice could only fail every verification
	// and wedge the ticket - a clean gate is then unreachable no matter how
	// many more rounds run.
	b.buildFix(t, "fix-1-alpha-test", "fix-1-beta-test", "fix-1-r1-f3")
	for _, s := range []string{"fix-1-alpha-test", "fix-1-beta-test", "fix-1-r1-f3"} {
		state, err := st.ReadSliceState(ticket, s)
		if err != nil {
			t.Fatalf("read slice state %s: %v", s, err)
		}
		if state.State != "green" {
			t.Fatalf("slice %s state = %q, want green (the default path must not stall)", s, state.State)
		}
	}

	// The same obligation holds for every later round this scenario
	// scripts, not only the first: round 2 re-reports both fix findings,
	// so it queues fix-2-alpha-test and fix-2-beta-test, and a scenario
	// missing an attempt for either one stalls the ticket exactly as a
	// missing round 1 attempt would. Round 3 is clean, so the default path
	// ends where the scenario says it ends.
	r5 := recordRound(t, env, fx, "2", "--yes")
	if r5.Code != 0 {
		t.Fatalf("review done (round 2) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r5.Code, r5.Stdout, r5.Stderr)
	}
	b.buildFix(t, "fix-2-alpha-test", "fix-2-beta-test")
	for _, s := range []string{"fix-2-alpha-test", "fix-2-beta-test"} {
		state, err := st.ReadSliceState(ticket, s)
		if err != nil {
			t.Fatalf("read slice state %s: %v", s, err)
		}
		if state.State != "green" {
			t.Fatalf("slice %s state = %q, want green (the default path must not stall)", s, state.State)
		}
	}

	r7 := recordRound(t, env, fx, "3")
	if r7.Code != 0 {
		t.Fatalf("review done (round 3) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r7.Code, r7.Stdout, r7.Stderr)
	}
	if !strings.Contains(r7.Stdout, "verdict: clean") {
		t.Fatalf("round 3 is the scenario's clean round, but the default path never reached it:\n%s", r7.Stdout)
	}
}
