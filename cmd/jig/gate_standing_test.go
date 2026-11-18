package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// gateOpens counts the gate-open lines of the fixture ticket's journal.
func gateOpens(t *testing.T, fx *fixture.Fixture) int {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range lines {
		if l.Event == journal.EventGateOpen {
			n++
		}
	}
	return n
}

// TestSecondGateReportsTheStandingRound: a gate run while a round is open at
// the branch's head opens no round and writes no gate-open line; it reports
// that round. Once the round is recorded, a gate is not turned away by it.
func TestSecondGateReportsTheStandingRound(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)

	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 || strings.Contains(out, "stands at its head") {
		t.Fatalf("first gate: exit = %d, want 0 and an ordinary report\n%s", code, out)
	}
	before := gateOpens(t, fx)

	out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "round: 1 stands at its head (not opened again)") || !strings.Contains(out, "verdict: open") {
		t.Fatalf("second gate over an open round: exit = %d, want 0 and the standing round\n%s", code, out)
	}
	if after := gateOpens(t, fx); after != before {
		t.Errorf("gate-open lines = %d, want %d: the second gate opened a round", after, before)
	}

	// Record the round: a gate at the same head goes on as it always did.
	ctx, code := runMain(t, e, "", "review", "context", fx.Ticket, "--json", "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("review context: exit = %d\n%s", code, ctx)
	}
	var req struct {
		MustReview []string `json:"must_review"`
	}
	if err := json.Unmarshal([]byte(ctx), &req); err != nil {
		t.Fatalf("review context --json: %v\n%s", err, ctx)
	}
	result, err := json.Marshal(map[string]any{
		"findings": []any{}, "still_present": []any{}, "reviewed_paths": req.MustReview, "summary": "nothing found",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "clean.json")
	if err := os.WriteFile(path, result, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, e, "", "review", "done", fx.Ticket, "--result", path, "--store", fx.StoreDir); code != 0 || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("review done: exit = %d, want a clean verdict\n%s", code, out)
	}
	out, code = runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir)
	if strings.Contains(out, "stands at its head") {
		t.Fatalf("gate over a recorded round: exit = %d, a recorded round must not stand\n%s", code, out)
	}
}

// TestGateAfterARecordedRoundAppliesADecidedAsk: a person keeps an ask the
// closing round raised; a plain `jig gate` at the same head queues its fix
// slice, as it did before a standing round was reported.
func TestGateAfterARecordedRoundAppliesADecidedAsk(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer-no-workspace"})
	e := testEnv(fx.Home).atTerminal()
	buildFixtureTicket(t, fx)
	if out, code := roundThroughMain(t, e, fx, filepath.Join(fx.ScenarioDir, "gate", "round-1", "review-result.json")); code != 2 {
		t.Fatalf("round 1 exit = %d, want 2\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "triage", fx.Ticket, "r1-f1", "keep", "--reason", "Bump it.", "--workspace", "alpha", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig triage keep: exit = %d\n%s", code, out)
	}
	out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 || strings.Contains(out, "stands at its head") || !strings.Contains(out, "fix-2-q-alpha") {
		t.Fatalf("jig gate: exit = %d, want the kept ask's slice queued\n%s", code, out)
	}
}

// TestSecondGateWithRoundShapingFlagsGoesOnAsToday: a gate given a flag that
// shapes the round does not report the standing round, which would drop the
// flag; it goes on as it would with none standing.
func TestSecondGateWithRoundShapingFlagsGoesOnAsToday(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("first gate: exit = %d\n%s", code, out)
	}
	for _, flag := range []string{"--yes", "--early"} {
		out, code := runMain(t, e, "", "gate", fx.Ticket, flag, "--store", fx.StoreDir)
		if code != 0 || strings.Contains(out, "stands at its head") {
			t.Fatalf("gate with %s: exit = %d, want 0 and an ordinary report\n%s", flag, code, out)
		}
	}
}

// TestReviewContextOpensARoundWithNoGateRunning: review context opens a round
// when none is open, without a gate having run first.
func TestReviewContextOpensARoundWithNoGateRunning(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)
	if out, code := runMain(t, e, "", "review", "context", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("review context: exit = %d, want 0\n%s", code, out)
	}
	if n := gateOpens(t, fx); n != 1 {
		t.Errorf("gate-open lines = %d, want 1", n)
	}
}
