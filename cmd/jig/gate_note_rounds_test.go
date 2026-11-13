package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
	"gopkg.in/yaml.v3"
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

// roundFindings is the parsed findings.yaml of one round.
type roundFindings struct {
	Cleared  []string `yaml:"cleared"`
	Findings []struct {
		ID string `yaml:"id"`
	} `yaml:"findings"`
}

func readRoundFindings(t *testing.T, fx *fixture.Fixture, round string) roundFindings {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "gate", round, "findings.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc roundFindings
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
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

// TestAGateRoundClearsAFloorRoutedNoteAsAddressed drives the whole path: round
// 1 files a low fix on beta/beta.go (a note), the builder commits a change to
// that file, and round 2's reviewer reads it and reports nothing. The gate's
// own close records the note as cleared, and the published review notes read it
// addressed at round 2, counted apart from fixed and noted.
func TestAGateRoundClearsAFloorRoutedNoteAsAddressed(t *testing.T) {
	t.Parallel()
	fx, e := floorNoteRound1(t)

	changeBeta(t, fx)
	round2 := reviewRoundResult(t, e, fx, verifydeliver.ReviewResult{Summary: "round 2: the change fixes the note"})
	if out, code := runMain(t, e, "", "review", "done", fx.Ticket, "--result", round2, "--store", fx.StoreDir); code != 0 || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("round 2 exit = %d, want 0 and a clean verdict\n%s", code, out)
	}
	if got := readRoundFindings(t, fx, "round-2").Cleared; len(got) != 1 || got[0] != "r1-f1" {
		t.Fatalf("round 2 findings.yaml cleared = %v, want [r1-f1]: the change touched the note's file and the reviewer read it", got)
	}

	out, code := runMain(t, e, "", "publish", fx.Ticket, "--body", "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("publish --body: exit = %d, want 0\n%s", code, out)
	}
	notes, err := os.ReadFile(filepath.Join(fx.StoreDir, "tickets", fx.Ticket, "pr", "review-notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(notes), "Status: addressed at round 2") {
		t.Errorf("review notes do not read the note addressed at round 2:\n%s", notes)
	}
}

// TestAKeptNoteIsAppliedOnceInTheRoundItsReviewerReportsItAgain: the review
// skill tells a reviewer to report a standing note again, so the round that
// applies a kept note usually has its reviewer reporting it too. The note must
// stay one fix slice (the one the gate queued when the keep was recorded) and
// one entry in the round's findings.yaml, not two.
func TestAKeptNoteIsAppliedOnceInTheRoundItsReviewerReportsItAgain(t *testing.T) {
	t.Parallel()
	fx, e := floorNoteRound1(t)
	if out, code := runMain(t, e, "", "triage", fx.Ticket, "r1-f1", "keep", "--reason", "Worth doing now.", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig triage keep: exit = %d\n%s", code, out)
	}

	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 || !strings.Contains(out, "fix-2-q-beta-test") {
		t.Fatalf("jig gate: exit = %d, want 0 and the kept note's slice queued\n%s", code, out)
	}
	buildQueuedSlice(t, fx, "fix-2-q-beta-test", "beta/beta.go")

	round2 := reviewRoundResult(t, e, fx, verifydeliver.ReviewResult{
		Summary:      "round 2: the note still stands",
		StillPresent: []verifydeliver.StillPresentEntry{{Prior: "r1-f1", Line: 6}},
	})
	if out, code := runMain(t, e, "", "review", "done", fx.Ticket, "--result", round2, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("round 2 exit = %d, want 0\n%s", code, out)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	slices, err := st.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	fixes := 0
	for _, s := range slices {
		if s.FromGate == 2 {
			fixes++
		}
	}
	if fixes != 1 {
		t.Errorf("round 2 queued %d fix slices, want 1 for the kept note", fixes)
	}
	entries := 0
	for _, f := range readRoundFindings(t, fx, "round-2").Findings {
		if f.ID == "r1-f1" {
			entries++
		}
	}
	if entries != 1 {
		t.Errorf("round 2 findings.yaml lists r1-f1 %d times, want once", entries)
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
	sha := gitMust(t, fx.RepoDir, "rev-parse", "HEAD")
	if out, code := runMain(t, cloneEnv(fx), "", "verify", fx.Ticket, sha, "--slice", "d", "--wait", "--store", fx.StoreDir); code != 0 || !strings.Contains(out, "state: pass") {
		t.Fatalf("jig verify of the change: exit = %d, want a pass\n%s", code, out)
	}
}

// reviewRoundResult opens the next round and writes res to a result file whose
// reviewed_paths are the round's must_review paths, as a reviewer's are.
func reviewRoundResult(t *testing.T, e env, fx *fixture.Fixture, res verifydeliver.ReviewResult) string {
	t.Helper()
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig gate: exit = %d, want 0 (the round opens)\n%s", code, out)
	}
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
	res.ReviewedPaths = req.MustReview
	path := filepath.Join(t.TempDir(), "round-2.json")
	writeReviewResultAt(t, path, res)
	return path
}
