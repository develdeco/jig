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
	// The close lists the spent keep and who kept it; findings.yaml records it
	// beside cleared; status counts it in the rounds row.
	if !strings.Contains(out, "kept_fixed[1]") || !strings.Contains(out, "r1-f1") {
		t.Fatalf("round 2's report does not list the spent keep:\n%s", out)
	}
	ff, err := os.ReadFile(filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "gate", "round-2", "findings.yaml"))
	if err != nil || !strings.Contains(string(ff), "kept_fixed:") || !strings.Contains(string(ff), "by: person") {
		t.Fatalf("findings.yaml does not name who kept the spent keep (err %v):\n%s", err, ff)
	}
	md, err := os.ReadFile(filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "gate", "round-2", "findings.md"))
	if err != nil || !strings.Contains(string(md), "kept and fixed: r1-f1 (person)") {
		t.Fatalf("findings.md does not name who kept the spent keep (err %v):\n%s", err, md)
	}
	st, _ := runMain(t, e, "", "status", fx.Ticket, "--store", fx.StoreDir)
	var round2 string
	for _, ln := range strings.Split(st, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "2,") {
			round2 = strings.TrimSpace(ln)
		}
	}
	if cells := strings.Split(round2, ","); round2 == "" || cells[len(cells)-1] != "1" {
		t.Fatalf("status rounds row for round 2 does not count the spent keep (kept_fixed 1):\n%s", st)
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

// keptNoteRound2 runs the life of GATE-15's note case: round 1 notes
// beta/version.go, a person keeps the note, an unrelated commit changes go.mod,
// and round 2's reviewer reports nothing and read reviewed. It returns round
// 2's exit code and output, and the fixture.
func keptNoteRound2(t *testing.T, reviewed []string) (string, int, *fixture.Fixture) {
	t.Helper()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer-no-workspace"})
	e := testEnv(fx.Home).atTerminal()
	buildFixtureTicket(t, fx)
	round1 := verifydeliver.ReviewResult{
		Findings: []verifydeliver.ResultFinding{{
			File: "beta/version.go", Line: 1, Title: "A version string worth a second look", Detail: "Maybe.",
			Action: "note", Risk: "low", RiskRationale: "cosmetic", Oracle: "test",
		}},
		ReviewedPaths: []string{"alpha/alpha.go", "alpha/alpha_test.go", "alpha/percent.go", "alpha/percent_test.go", "beta/beta.go", "beta/beta_test.go", "beta/version.go", "go.mod", ".claude/jig.yaml"},
		Summary:       "round 1",
	}
	path1 := filepath.Join(t.TempDir(), "round-1.json")
	writeReviewResultAt(t, path1, round1)
	if out, code := roundThroughMain(t, e, fx, path1); code != 0 {
		t.Fatalf("round 1 exit = %d, want 0\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "triage", fx.Ticket, "r1-f1", "keep", "--reason", "Look at it.", "--workspace", "beta", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig triage keep: exit = %d\n%s", code, out)
	}
	if err := os.WriteFile(filepath.Join(fx.RepoDir, "go.mod"), []byte("module example.com/fixture\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitMust(t, fx.RepoDir, "add", "-A")
	gitMust(t, fx.RepoDir, "commit", "-m", "bump the toolchain")
	path2 := filepath.Join(t.TempDir(), "round-2.json")
	writeReviewResultAt(t, path2, verifydeliver.ReviewResult{Findings: []verifydeliver.ResultFinding{}, ReviewedPaths: reviewed, Summary: "round 2"})
	out, code := roundThroughMain(t, e, fx, path2)
	return out, code, fx
}

// A kept note whose file the closing round's reviewer did not read is not
// spent as fixed: its file is in must_review, so a report that skips it is
// refused and the round is not clean (GATE-15 round 1, r1-f1).
func TestKeptNoteTheClosingRoundDidNotReadIsNotSpentAsFixed(t *testing.T) {
	t.Parallel()
	out, code, fx := keptNoteRound2(t, []string{"go.mod"})
	if code == 0 || !strings.Contains(out, "REVIEW_INVALID") || !strings.Contains(out, "beta/version.go") {
		t.Fatalf("round 2: exit = %d, want REVIEW_INVALID naming beta/version.go\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "gate", "round-2", "findings.yaml")); err == nil {
		t.Fatal("round 2 was recorded without its reviewer reading the kept note's file")
	}
}

// A kept note the closing round's reviewer read and no longer reports, at a
// moved head, is spent as fixed and queues no slice.
func TestKeptNoteTheClosingRoundReadAndNoLongerReportsReadsFixed(t *testing.T) {
	t.Parallel()
	out, code, _ := keptNoteRound2(t, []string{"go.mod", "beta/version.go"})
	if code != 0 || strings.Contains(out, "fix-2-r1-f1") {
		t.Fatalf("round 2: exit = %d, want 0 and no fix-2-r1-f1\n%s", code, out)
	}
}
