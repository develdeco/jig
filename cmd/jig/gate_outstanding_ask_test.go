package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestOutstandingAskIsOfferedEveryRoundUntilDecided pins the fix for the
// review finding that an ask left undecided in one round was decidable
// only if the next reviewer round happened to report it again: the triage
// hook used to see only findings the reviewer reported that round, so an
// ask nobody re-reports could sit under needs_a_human forever with no
// prompt ever asking about it.
//
// Round 1 (no terminal) leaves a no-workspace ask undecided and jig exits
// 2. Between rounds, `jig status` lists it under outstanding_asks with the
// command that decides it. Round 2, at a terminal, offers that same ask
// even though the scripted reviewer does not report it again this round,
// and keeping it (with the workspace the human supplies) builds its fix
// slice.
func TestOutstandingAskIsOfferedEveryRoundUntilDecided(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "reviewer-no-workspace"})
	ticket := fx.Ticket

	gateArgs := []string{"gate", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}

	// Build the frontier to green first (same shape as
	// TestSolveStopsOnNeedsHumanInsteadOfRedispatching): a, b, d go
	// straight through; c pauses on q-001.
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

	// Round 1: stdin is a strings.Reader, never a terminal, so triage runs
	// DefaultTriage and the no-workspace ask on go.mod stays undecided.
	out, code = runMain(t, "", gateArgs...)
	if code != 2 {
		t.Fatalf("round 1 exit = %d, want 2 (needs a human)\n%s", code, out)
	}
	if !strings.Contains(out, "needs_a_human") || !strings.Contains(out, "r1-f1") {
		t.Fatalf("round 1 output missing the undecided ask r1-f1 under needs_a_human:\n%s", out)
	}

	// Between rounds: `jig status` must show the waiting decision, not
	// "questions: none" and nothing else.
	statusOut, code := runMain(t, "", "status", ticket, "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("status exit = %d, want 0\n%s", code, statusOut)
	}
	if !strings.Contains(statusOut, "outstanding_asks[1]") {
		t.Fatalf("status output missing outstanding_asks:\n%s", statusOut)
	}
	if !strings.Contains(statusOut, "r1-f1") || !strings.Contains(statusOut, "go.mod:1") {
		t.Fatalf("status output's outstanding_asks row missing r1-f1/go.mod:1:\n%s", statusOut)
	}
	if !strings.Contains(statusOut, "jig gate "+ticket) {
		t.Fatalf("status output's outstanding_asks row missing the deciding command:\n%s", statusOut)
	}

	// Round 2's scripted reviewer covers go.mod (must_review, since r1-f1
	// is still outstanding) but reports nothing new there - it does not
	// re-report r1-f1 at all. Before the fix, the terminal prompt below
	// would never mention r1-f1.
	round2 := verifydeliver.ReviewResult{
		Findings:      []verifydeliver.ResultFinding{},
		ReviewedPaths: []string{"go.mod"},
		Summary:       "round 2: nothing new to report",
	}
	round2Path := filepath.Join(fx.ScenarioDir, "gate", "round-2", "review-result.json")
	if err := os.MkdirAll(filepath.Dir(round2Path), 0o755); err != nil {
		t.Fatalf("mkdir round 2 scenario dir: %v", err)
	}
	writeReviewResultAt(t, round2Path, round2)

	prevTerm := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	defer func() { stdinIsTerminal = prevTerm }()

	// keep the ask, give a decision, then supply the workspace prompt
	// (go.mod is in no declared workspace) with "alpha". Its oracle
	// already resolved to the manifest's sole oracle in round 1, so no
	// oracle prompt follows.
	stdin := "keep\nBumping the toolchain is intentional.\nalpha\n"
	out, code = runMain(t, stdin, gateArgs...)
	if code != 0 {
		t.Fatalf("round 2 exit = %d, want 0 (the human decided the only outstanding ask)\n%s", code, out)
	}
	if !strings.Contains(out, "r1-f1") {
		t.Fatalf("round 2 prompt/report never mentioned r1-f1:\n%s", out)
	}
	if !strings.Contains(out, "fix-2-r1-f1") {
		t.Fatalf("round 2 output missing the fix slice built from the kept ask:\n%s", out)
	}

	triage, decision, found := findingsYAMLEntry(t, filepath.Join(fx.StoreDir, ticket, "gate", "round-2", "findings.yaml"), "r1-f1")
	if !found {
		t.Fatalf("round 2 findings.yaml has no entry for r1-f1 (the decided outstanding ask was never recorded)")
	}
	if triage != verifydeliver.TriageHuman {
		t.Errorf("r1-f1 triage = %q, want %q", triage, verifydeliver.TriageHuman)
	}
	if decision != "Bumping the toolchain is intentional." {
		t.Errorf("r1-f1 decision = %q, want the text typed at the prompt", decision)
	}

	statusOut, code = runMain(t, "", "status", ticket, "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("status after round 2 exit = %d, want 0\n%s", code, statusOut)
	}
	if strings.Contains(statusOut, "outstanding_asks[1]") {
		t.Fatalf("status still lists r1-f1 as outstanding after it was decided:\n%s", statusOut)
	}
}
