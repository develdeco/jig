package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// keptAskRound2 runs the life of GATE-15: round 1 asks about go.mod, a person
// keeps the ask, and then moveHead changes the clone (or not) before round 2,
// whose reviewer reports nothing (or, with again, reports r1-f1 again through
// --still-present's prior). It returns round 2's exit code and output, and the
// fixture.
func keptAskRound2(t *testing.T, moveHead, again bool) (string, int, *fixture.Fixture) {
	t.Helper()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer-no-workspace"})
	e := testEnv(fx.Home).atTerminal()
	buildFixtureTicket(t, fx)
	if out, code := roundThroughMain(t, e, fx, filepath.Join(fx.ScenarioDir, "gate", "round-1", "review-result.json")); code != 2 {
		t.Fatalf("round 1 exit = %d, want 2\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "triage", fx.Ticket, "r1-f1", "keep", "--reason", "Bump it.", "--workspace", "alpha", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig triage keep: exit = %d\n%s", code, out)
	}
	if moveHead {
		// The work the ask wanted, committed on the ticket's branch.
		if err := os.WriteFile(filepath.Join(fx.RepoDir, "go.mod"), []byte("module example.com/fixture\n\ngo 1.22\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitMust(t, fx.RepoDir, "add", "-A")
		gitMust(t, fx.RepoDir, "commit", "-m", "bump the toolchain")
	}
	round2 := verifydeliver.ReviewResult{
		Findings:      []verifydeliver.ResultFinding{},
		ReviewedPaths: []string{"go.mod"},
		Summary:       "round 2",
	}
	if again {
		round2.StillPresent = []verifydeliver.StillPresentEntry{{Prior: "r1-f1"}}
	}
	path := filepath.Join(t.TempDir(), "round-2.json")
	writeReviewResultAt(t, path, round2)
	out, code := roundThroughMain(t, e, fx, path)
	return out, code, fx
}

// TestKeptAskTheClosingRoundNoLongerReportsAtAMovedHeadReadsFixed is the repro
// of GATE-15 and its rule: the code moved after round 1, round 2 does not
// report the kept ask again, so the keep is spent as a fix and queues no slice.
func TestKeptAskTheClosingRoundNoLongerReportsAtAMovedHeadReadsFixed(t *testing.T) {
	t.Parallel()
	out, code, fx := keptAskRound2(t, true, false)
	if code != 0 {
		t.Fatalf("round 2 exit = %d, want 0\n%s", code, out)
	}
	if strings.Contains(out, "fix-2-r1-f1") {
		t.Fatalf("round 2 queued a fix slice for an ask the closing round no longer reports at a moved head:\n%s", out)
	}
	e := testEnv(fx.Home).atTerminal()
	if st, code := runMain(t, e, "", "status", fx.Ticket, "--store", fx.StoreDir); code != 0 || strings.Contains(st, "outstanding_asks[1]") {
		t.Fatalf("status: exit = %d, the kept ask must no longer be outstanding\n%s", code, st)
	}
}

// At the same head nothing can have fixed the ask, so the keep applies.
func TestKeptAskAtTheSameHeadStillQueuesItsFixSlice(t *testing.T) {
	t.Parallel()
	out, code, _ := keptAskRound2(t, false, false)
	if code != 0 || !strings.Contains(out, "fix-2-r1-f1") {
		t.Fatalf("round 2: exit = %d, want 0 and fix-2-r1-f1\n%s", code, out)
	}
}

// A round that reports the ask again keeps it alive: the keep applies.
func TestKeptAskTheClosingRoundReportsAgainStillQueuesItsFixSlice(t *testing.T) {
	t.Parallel()
	out, code, _ := keptAskRound2(t, true, true)
	if code != 0 || !strings.Contains(out, "fix-2-r1-f1") {
		t.Fatalf("round 2: exit = %d, want 0 and fix-2-r1-f1\n%s", code, out)
	}
}
