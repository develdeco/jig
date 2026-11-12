package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestTriageTakesANoteAndTheNextRoundAppliesIt pins the life of a note a
// person takes up: round 1 reports one low fix, which the risk floor routes
// to a note, and the round is clean; `jig triage` names an id that is neither
// an ask nor a note in an error listing both, then keeps the note; `jig
// status` lists the decision beside the notes; the next round, whose reviewer
// has nothing to report and no file to read, still closes (a pending keep is
// work) and queues the fix slice, carrying the person's reason, and the note
// is an open finding from then on. A second note dismissed the same way reads
// dismissed by a human.
func TestTriageTakesANoteAndTheNextRoundAppliesIt(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	e := testEnv(fx.Home).atTerminal()
	ticket := fx.Ticket
	buildFixtureTicket(t, fx)

	round1 := reviewResultAt(t, filepath.Join(fx.ScenarioDir, "gate", "round-1", "review-result.json"))
	round1.Findings = []verifydeliver.ResultFinding{
		{File: "beta/beta.go", Line: 6, Title: "Greet does not trim surrounding whitespace", Detail: "A padded name renders padded.", Action: "fix", Risk: "low", RiskRationale: "cosmetic", Oracle: "test"},
		{File: "alpha/percent.go", Line: 3, Title: "Clamp helpers could share a check", Detail: "Later.", Action: "fix", Risk: "low", RiskRationale: "maintainability", Oracle: "test"},
	}
	round1Path := filepath.Join(t.TempDir(), "round-1.json")
	writeReviewResultAt(t, round1Path, round1)
	out, code := roundThroughMain(t, e, fx, round1Path)
	if code != 0 || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("round 1 exit = %d, want 0 and a clean verdict (both fixes are below the floor)\n%s", code, out)
	}

	triageArgs := func(args ...string) []string {
		return append(append([]string{"triage", ticket}, args...), "--store", fx.StoreDir)
	}
	out, code = runMain(t, e, "", triageArgs("r1-f9", "keep")...)
	if code != 1 || !strings.Contains(out, "TRIAGE_NOT_AN_ASK") || !strings.Contains(out, "it has no asks") || !strings.Contains(out, "r1-f1") || !strings.Contains(out, "r1-f2") {
		t.Fatalf("jig triage of an id that is neither: exit = %d, want 1 naming that it has no asks and its notes r1-f1, r1-f2\n%s", code, out)
	}
	out, code = runMain(t, e, "", triageArgs("r1-f1", "keep", "--reason", "Worth doing now.")...)
	if code != 0 || !strings.Contains(out, "decision: keep") || !strings.Contains(out, "provenance: person") {
		t.Fatalf("jig triage keep of a note: exit = %d, want 0\n%s", code, out)
	}
	if out, code = runMain(t, e, "", triageArgs("r1-f2", "dismiss")...); code != 0 {
		t.Fatalf("jig triage dismiss of a note: exit = %d, want 0\n%s", code, out)
	}

	statusOut, code := runMain(t, e, "", "status", ticket, "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(statusOut, "decided_notes[2]") || !strings.Contains(statusOut, "keep") || !strings.Contains(statusOut, "dismiss") {
		t.Fatalf("status exit = %d, want 0 and decided_notes listing both decisions\n%s", code, statusOut)
	}

	round2Path := filepath.Join(t.TempDir(), "round-2.json")
	// The kept note's file is in must_review, so the reviewer reads it.
	writeReviewResultAt(t, round2Path, verifydeliver.ReviewResult{ReviewedPaths: []string{"beta/beta.go"}, Summary: "round 2: nothing new"})
	out, code = roundThroughMain(t, e, fx, round2Path)
	if code != 0 || !strings.Contains(out, "fix-2-beta-test") {
		t.Fatalf("round 2 exit = %d, want 0 and the fix slice fix-2-beta-test built from the kept note\n%s", code, out)
	}
	if strings.Contains(out, "fix-2-r1-f2") {
		t.Fatalf("round 2 queued a slice for the dismissed note:\n%s", out)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	var goal string
	for _, s := range slices {
		if s.ID == "fix-2-beta-test" {
			goal = s.Goal
			if s.FromGate != 2 || len(s.Findings) != 1 || s.Findings[0] != "r1-f1" {
				t.Errorf("slice = %+v, want a fix slice of round 2 for r1-f1", s)
			}
		}
	}
	if !strings.Contains(goal, "Worth doing now.") || !strings.Contains(goal, "kept by the human") {
		t.Errorf("fix-2-beta-test goal = %q, want the person's reason and who kept it", goal)
	}
	if used, _ := verifydeliver.UsedFixBudget(st, ticket, 3); used != 1 {
		t.Errorf("fix budget used = %d, want 1: the round that queued the kept note's slice counts", used)
	}
	_, notes, _, err := verifydeliver.OutstandingFindings(st, ticket)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("notes after round 2 = %+v, want none: r1-f1 is open work and r1-f2 dismissed", notes)
	}
	notesFile := filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-2", "findings.yaml")
	if triage, decision, found := findingsYAMLEntry(t, notesFile, "r1-f1"); !found || triage != verifydeliver.TriageHuman || decision != "Worth doing now." {
		t.Errorf("round 2 findings.yaml r1-f1 = (%q, %q, %v), want a human's decision recorded", triage, decision, found)
	}
}

// TestTwoKeptNotesInOneWorkspaceAreOneFixSlice pins the grouping through the
// binary: two notes of one workspace and oracle, both kept with `jig triage`,
// are built as the one fix slice of the next round, which lists both findings
// and carries each person's reason in its goal.
func TestTwoKeptNotesInOneWorkspaceAreOneFixSlice(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	e := testEnv(fx.Home).atTerminal()
	ticket := fx.Ticket
	buildFixtureTicket(t, fx)

	round1 := reviewResultAt(t, filepath.Join(fx.ScenarioDir, "gate", "round-1", "review-result.json"))
	round1.Findings = []verifydeliver.ResultFinding{
		{File: "alpha/alpha.go", Line: 5, Title: "Add could say what overflow does", Detail: "The comment is silent.", Action: "fix", Risk: "low", RiskRationale: "cosmetic", Oracle: "test"},
		{File: "alpha/percent.go", Line: 3, Title: "Clamp helpers could share a check", Detail: "Later.", Action: "fix", Risk: "low", RiskRationale: "maintainability", Oracle: "test"},
	}
	round1Path := filepath.Join(t.TempDir(), "round-1.json")
	writeReviewResultAt(t, round1Path, round1)
	out, code := roundThroughMain(t, e, fx, round1Path)
	if code != 0 || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("round 1 exit = %d, want 0 and a clean verdict (both fixes are below the floor)\n%s", code, out)
	}

	for id, reason := range map[string]string{"r1-f1": "Say it now.", "r1-f2": "Share the check now."} {
		out, code = runMain(t, e, "", "triage", ticket, id, "keep", "--reason", reason, "--store", fx.StoreDir)
		if code != 0 {
			t.Fatalf("jig triage keep %s: exit = %d, want 0\n%s", id, code, out)
		}
	}

	round2Path := filepath.Join(t.TempDir(), "round-2.json")
	writeReviewResultAt(t, round2Path, verifydeliver.ReviewResult{ReviewedPaths: []string{"alpha/alpha.go", "alpha/percent.go"}, Summary: "round 2: nothing new"})
	out, code = roundThroughMain(t, e, fx, round2Path)
	if code != 0 || !strings.Contains(out, "fix_slices[1]") || !strings.Contains(out, "fix-2-alpha-test") {
		t.Fatalf("round 2 exit = %d, want 0 and the one fix slice fix-2-alpha-test\n%s", code, out)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	var found int
	for _, s := range slices {
		if s.FromGate != 2 {
			continue
		}
		found++
		if s.ID != "fix-2-alpha-test" || len(s.Findings) != 2 || s.Findings[0] != "r1-f1" || s.Findings[1] != "r1-f2" {
			t.Errorf("round 2 slice = %+v, want fix-2-alpha-test listing r1-f1 and r1-f2", s)
		}
		for _, want := range []string{"Say it now.", "Share the check now.", "Gate finding r1-f1, kept by the human:", "Gate finding r1-f2, kept by the human:"} {
			if !strings.Contains(s.Goal, want) {
				t.Errorf("goal missing %q:\n%s", want, s.Goal)
			}
		}
	}
	if found != 1 {
		t.Errorf("round 2 appended %d slices, want 1", found)
	}
}
