package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestOutstandingAskIsDecidedWithJigTriageAndAppliedByTheNextRound pins the
// life of an ask the reviewer left for a person: a round records it undecided
// and exits 2, `jig status` lists it with the command that decides it, `jig
// triage` journals the decision (refusing an id that is no ask), and the next
// round - whose reviewer reports nothing new there - applies it: the kept ask
// becomes the fix slice, carrying the person's reason and the workspace they
// chose, and the ask is no longer outstanding.
//
// The ask is on go.mod, which lies in no declared workspace, so keeping it
// needs the person's workspace; its oracle resolved to the manifest's sole
// oracle in round 1.
func TestOutstandingAskIsDecidedWithJigTriageAndAppliedByTheNextRound(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer-no-workspace"})
	e := testEnv(fx.Home).atTerminal() // the person at their terminal
	ticket := fx.Ticket

	// Build the frontier to green first: a, b, d go straight through; c
	// pauses on q-001.
	runArgs := []string{"run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	answerArgs := []string{"run", ticket, "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	out, code := runMain(t, e, "", runArgs...)
	if code != 2 {
		t.Fatalf("run 1 exit = %d, want 2 (paused at q-001)\n%s", code, out)
	}
	out, code = runMain(t, e, "", answerArgs...)
	if code != 0 {
		t.Fatalf("run (answer) exit = %d, want 0\n%s", code, out)
	}

	// Round 1: nothing decides the no-workspace ask on go.mod, so the round
	// records it undecided and jig exits 2.
	round1Path := filepath.Join(fx.ScenarioDir, "gate", "round-1", "review-result.json")
	out, code = roundThroughMain(t, e, fx, round1Path)
	if code != 2 {
		t.Fatalf("round 1 exit = %d, want 2 (needs a human)\n%s", code, out)
	}
	if !strings.Contains(out, "needs_a_human") || !strings.Contains(out, "r1-f1") || !strings.Contains(out, "jig triage "+ticket) {
		t.Fatalf("round 1 output missing the undecided ask r1-f1 under needs_a_human, or `jig triage` as the way to decide it:\n%s", out)
	}

	// Between rounds: `jig status` must show the waiting decision, and the
	// command that decides it.
	statusOut, code := runMain(t, e, "", "status", ticket, "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("status exit = %d, want 0\n%s", code, statusOut)
	}
	if !strings.Contains(statusOut, "outstanding_asks[1]") {
		t.Fatalf("status output missing outstanding_asks:\n%s", statusOut)
	}
	if !strings.Contains(statusOut, "r1-f1") || !strings.Contains(statusOut, "go.mod:1") {
		t.Fatalf("status output's outstanding_asks row missing r1-f1/go.mod:1:\n%s", statusOut)
	}
	if !strings.Contains(statusOut, "jig triage "+ticket) {
		t.Fatalf("status output is missing the deciding command:\n%s", statusOut)
	}

	// An id that is no ask is refused, and says which are; a decision other
	// than keep or dismiss is no decision.
	triageArgs := func(args ...string) []string {
		return append(append([]string{"triage", ticket}, args...), "--store", fx.StoreDir)
	}
	out, code = runMain(t, e, "", triageArgs("r1-f9", "keep")...)
	if code != 1 || !strings.Contains(out, "TRIAGE_NOT_AN_ASK") || !strings.Contains(out, "r1-f1") {
		t.Fatalf("jig triage of an id that is no ask: exit = %d, want 1, TRIAGE_NOT_AN_ASK and the asks there are\n%s", code, out)
	}
	out, code = runMain(t, e, "", triageArgs("r1-f1", "maybe")...)
	if code != 2 || !strings.Contains(out, "VALIDATION_ERROR") {
		t.Fatalf("jig triage maybe: exit = %d, want 2\n%s", code, out)
	}

	// The person keeps the ask, says why, and supplies the workspace go.mod
	// lies in none of. Nothing is applied yet: the decision is a journal line.
	out, code = runMain(t, e, "", triageArgs("r1-f1", "keep", "--reason", "Bumping the toolchain is intentional.", "--workspace", "alpha")...)
	if code != 0 || !strings.Contains(out, "decision: keep") || !strings.Contains(out, "provenance: person") {
		t.Fatalf("jig triage keep: exit = %d, want 0, the decision and a person\n%s", code, out)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	lines, err := journal.Read(st, ticket)
	if err != nil {
		t.Fatal(err)
	}
	var decided journal.Line
	for _, l := range lines {
		if l.Event == journal.EventTriage {
			decided = l
		}
	}
	if decided.Ref != "r1-f1" || decided.Outcome != "keep" || decided.Summary != "Bumping the toolchain is intentional." || decided.Provenance != "person" || !strings.Contains(decided.Command, "workspace=alpha") {
		t.Fatalf("triage line = %+v, want the decision, its reason, the workspace and a person", decided)
	}
	if _, err := os.Stat(filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-2")); err == nil {
		t.Fatalf("jig triage opened a round: the decision is applied by the next one")
	}

	// Round 2's reviewer covers go.mod (must_review, since r1-f1 is still
	// outstanding) but reports nothing new there - it does not re-report r1-f1
	// at all. The recorded decision is applied all the same.
	round2 := verifydeliver.ReviewResult{
		Findings:      []verifydeliver.ResultFinding{},
		ReviewedPaths: []string{"go.mod"},
		Summary:       "round 2: nothing new to report",
	}
	round2Path := filepath.Join(t.TempDir(), "round-2.json")
	writeReviewResultAt(t, round2Path, round2)
	out, code = roundThroughMain(t, e, fx, round2Path)
	if code != 0 {
		t.Fatalf("round 2 exit = %d, want 0 (the person decided the only outstanding ask)\n%s", code, out)
	}
	if !strings.Contains(out, "r1-f1") {
		t.Fatalf("round 2 report never mentioned r1-f1:\n%s", out)
	}
	if !strings.Contains(out, "fix-2-r1-f1") {
		t.Fatalf("round 2 output missing the fix slice built from the kept ask:\n%s", out)
	}

	triage, decision, found := findingsYAMLEntry(t, filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-2", "findings.yaml"), "r1-f1")
	if !found {
		t.Fatalf("round 2 findings.yaml has no entry for r1-f1 (the decided outstanding ask was never recorded)")
	}
	if triage != verifydeliver.TriageHuman {
		t.Errorf("r1-f1 triage = %q, want %q", triage, verifydeliver.TriageHuman)
	}
	if decision != "Bumping the toolchain is intentional." {
		t.Errorf("r1-f1 decision = %q, want the reason given to jig triage", decision)
	}

	statusOut, code = runMain(t, e, "", "status", ticket, "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("status after round 2 exit = %d, want 0\n%s", code, statusOut)
	}
	if strings.Contains(statusOut, "outstanding_asks[1]") {
		t.Fatalf("status still lists r1-f1 as outstanding after it was decided:\n%s", statusOut)
	}
}
