package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// floorNoteRound1 builds the fixture ticket and closes round 1 clean with one
// low fix on beta/beta.go, which the risk floor routes to a note (r1-f1).
func floorNoteRound1(t *testing.T) (*fixture.Fixture, env) {
	t.Helper()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	e := testEnv(fx.Home).atTerminal()
	buildFixtureTicket(t, fx)

	round1 := reviewResultAt(t, filepath.Join(fx.ScenarioDir, "gate", "round-1", "review-result.json"))
	round1.Findings = []verifydeliver.ResultFinding{
		{File: "beta/beta.go", Line: 6, Title: "Greet does not trim surrounding whitespace", Detail: "A padded name renders padded.", Action: "fix", Risk: "low", RiskRationale: "cosmetic", Oracle: "test"},
	}
	path := filepath.Join(t.TempDir(), "round-1.json")
	writeReviewResultAt(t, path, round1)
	out, code := roundThroughMain(t, e, fx, path)
	if code != 0 || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("round 1 exit = %d, want 0 and a clean verdict (the fix is below the floor)\n%s", code, out)
	}
	return fx, e
}

// TestNextAfterKeepingANoteOnACleanRoundNamesReview: a note kept after a clean
// round waits for the next round, as a kept ask does, so `jig next` names the
// review that applies it and not the publish that would drop the keep. A note
// nobody decided leaves the clean round clean.
func TestNextAfterKeepingANoteOnACleanRoundNamesReview(t *testing.T) {
	t.Parallel()
	fx, e := floorNoteRound1(t)
	next := func() string {
		out, code := runMain(t, e, "", "next", fx.Ticket, "--store", fx.StoreDir)
		if code != 0 {
			t.Fatalf("jig next: exit = %d, want 0\n%s", code, out)
		}
		return out
	}

	if out := next(); !strings.Contains(out, "phase: publish") {
		t.Fatalf("jig next on a clean round with an undecided note should name publish:\n%s", out)
	}
	if out, code := runMain(t, e, "", "triage", fx.Ticket, "r1-f1", "keep", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig triage keep: exit = %d\n%s", code, out)
	}
	out := next()
	if !strings.Contains(out, "phase: review") || strings.Contains(out, "phase: publish") || !strings.Contains(out, "Review "+fx.Ticket) {
		t.Fatalf("jig next after keeping a note on a clean round should name review, the round that applies it:\n%s", out)
	}
}

// changeBeta is the builder's commit to beta/beta.go after round 1, verified
// under the slice that already owns the file.
func changeBeta(t *testing.T, fx *fixture.Fixture) {
	t.Helper()
	path := filepath.Join(fx.RepoDir, "beta", "beta.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("\n// Greet trims nothing yet.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	gitBuilder(t).Commit(fx.RepoDir, fx.Ticket+" d: touch beta"+"\n\n"+"Jig-Slice: d")
	verifyHere(t, fx, "d")
}
