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

// keptNoteAfterRound1 runs round 1 of a built ticket whose reviewer reports two
// low fixes, which the risk floor routes to notes: r1-f1 in beta/beta.go and
// r1-f2 in alpha/percent.go. The round is clean. It returns the fixture and a
// terminal env.
func keptNoteAfterRound1(t *testing.T) (*fixture.Fixture, env) {
	t.Helper()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	e := testEnv(fx.Home).atTerminal()
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
	return fx, e
}

// gateOpenLines counts the gate-open lines of the ticket's journal.
func gateOpenLines(t *testing.T, fx *fixture.Fixture) int {
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

// buildQueuedSlice builds a queued fix slice the way an agent does: a change
// to file committed on the ticket's branch with the slice's trailer, verified.
func buildQueuedSlice(t *testing.T, fx *fixture.Fixture, slice, file string) {
	t.Helper()
	path := filepath.Join(fx.RepoDir, filepath.FromSlash(file))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("\n// trimmed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	gitMust(t, fx.RepoDir, "add", "-A")
	gitMust(t, fx.RepoDir, "commit", "-m", "build "+slice, "-m", "Jig-Slice: "+slice)
	sha := gitMust(t, fx.RepoDir, "rev-parse", "HEAD")
	out, code := runMain(t, cloneEnv(fx), "", "verify", fx.Ticket, sha, "--slice", slice, "--wait", "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "state: pass") {
		t.Fatalf("jig verify %s: exit = %d, want 0 and a pass\n%s", slice, code, out)
	}
}

// TestGateQueuesAKeptNoteBeforeTheNextRoundOpens pins the point at which a
// kept note is built: round 1 reports a low note, `jig triage` keeps it with a
// reason and dismisses a second note, and the next `jig gate` queues the kept
// note's fix slice at once and opens no round (no gate-open line, no suite, no
// reviewer); the dismissed note gets no slice. Once the slice is green the
// next round opens and does not queue the keep again.
func TestGateQueuesAKeptNoteBeforeTheNextRoundOpens(t *testing.T) {
	t.Parallel()
	fx, e := keptNoteAfterRound1(t)
	ticket := fx.Ticket
	triageArgs := func(args ...string) []string {
		return append(append([]string{"triage", ticket}, args...), "--store", fx.StoreDir)
	}
	if out, code := runMain(t, e, "", triageArgs("r1-f1", "keep", "--reason", "Worth doing now.")...); code != 0 {
		t.Fatalf("jig triage keep: exit = %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", triageArgs("r1-f2", "dismiss")...); code != 0 {
		t.Fatalf("jig triage dismiss: exit = %d\n%s", code, out)
	}

	opened := gateOpenLines(t, fx)
	out, code := runMain(t, e, "", "gate", ticket, "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "fix_slices") || !strings.Contains(out, "fix-2-r1-f1") {
		t.Fatalf("jig gate: exit = %d, want 0 and the kept note's slice fix-2-r1-f1 in fix_slices\n%s", code, out)
	}
	if strings.Contains(out, "fix-2-r1-f2") {
		t.Fatalf("jig gate queued a slice for the dismissed note:\n%s", out)
	}
	if got := gateOpenLines(t, fx); got != opened {
		t.Fatalf("gate-open lines = %d, want %d: queuing a keep opens no round", got, opened)
	}
	roundDir := filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-2")
	if _, err := os.Stat(roundDir); err == nil {
		t.Fatalf("%s exists: queuing a keep records no round", roundDir)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSlice(slices, "fix-2-r1-f1") || hasSlice(slices, "fix-2-r1-f2") {
		t.Fatalf("slices = %+v, want fix-2-r1-f1 and no slice for the dismissed note", slices)
	}

	// The slice is open work: a second gate before it is green refuses and
	// queues nothing more.
	if out, code = runMain(t, e, "", "gate", ticket, "--store", fx.StoreDir); code != 1 || !strings.Contains(out, "GATE_NOT_GREEN") {
		t.Fatalf("jig gate with the kept slice unbuilt: exit = %d, want 1 GATE_NOT_GREEN\n%s", code, out)
	}

	buildQueuedSlice(t, fx, "fix-2-r1-f1", "beta/beta.go")
	round2Path := filepath.Join(t.TempDir(), "round-2.json")
	writeReviewResultAt(t, round2Path, verifydeliver.ReviewResult{ReviewedPaths: []string{"beta/beta.go"}, Summary: "round 2: nothing new"})
	out, code = roundThroughMain(t, e, fx, round2Path)
	if code != 0 {
		t.Fatalf("round 2 exit = %d, want 0\n%s", code, out)
	}
	if slices, err = st.ReadSlices(ticket); err != nil {
		t.Fatal(err)
	}
	for _, s := range slices {
		if s.ID != "fix-2-r1-f1" && len(s.Findings) > 0 && s.Findings[0] == "r1-f1" {
			t.Errorf("slice %s was queued for r1-f1 a second time", s.ID)
		}
	}
}

// TestTriageHelpSaysWhenTheKeepIsQueued pins the help of `jig triage`: a keep
// is queued by the next `jig gate`, before it opens a round.
func TestTriageHelpSaysWhenTheKeepIsQueued(t *testing.T) {
	t.Parallel()
	out, code := runMain(t, testEnv(t.TempDir()), "", "triage", "--help")
	if code != 0 {
		t.Fatalf("jig triage --help: exit = %d\n%s", code, out)
	}
	flat := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(flat, "next `jig gate` queues") || !strings.Contains(flat, "before it opens a round") {
		t.Fatalf("jig triage --help does not say when a keep is queued:\n%s", out)
	}
}

// TestTriageTakesANoteAndTheNextRoundAppliesIt pins the life of a note a person
// takes up: round 1 reports one low fix, which the risk floor routes to a
// note, and the round is clean; `jig triage` names an id that is neither an
// ask nor a note in an error listing both, then keeps the note; `jig status`
// lists the decision beside the notes; the next `jig gate` queues the fix
// slice, carrying the person's reason, before round 2 opens. A second note
// dismissed the same way reads dismissed by a human at round 2, which, once
// the slice is built, leaves no note behind.
func TestTriageTakesANoteAndTheNextRoundAppliesIt(t *testing.T) {
	t.Parallel()
	fx, e := keptNoteAfterRound1(t)
	ticket := fx.Ticket

	triageArgs := func(args ...string) []string {
		return append(append([]string{"triage", ticket}, args...), "--store", fx.StoreDir)
	}
	out, code := runMain(t, e, "", triageArgs("r1-f9", "keep")...)
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

	out, code = runMain(t, e, "", "gate", ticket, "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "fix-2-r1-f1") {
		t.Fatalf("jig gate exit = %d, want 0 and the fix slice fix-2-r1-f1 built from the kept note\n%s", code, out)
	}
	if strings.Contains(out, "fix-2-r1-f2") {
		t.Fatalf("jig gate queued a slice for the dismissed note:\n%s", out)
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
		if s.ID == "fix-2-r1-f1" {
			goal = s.Goal
			if s.FromGate != 2 || len(s.Findings) != 1 || s.Findings[0] != "r1-f1" {
				t.Errorf("slice = %+v, want a fix slice of round 2 for r1-f1", s)
			}
		}
	}
	if !strings.Contains(goal, "Worth doing now.") || !strings.Contains(goal, "kept by the human") {
		t.Errorf("fix-2-r1-f1 goal = %q, want the person's reason and who kept it", goal)
	}
	if used, _ := verifydeliver.UsedFixBudget(st, ticket, 3); used != 1 {
		t.Errorf("fix budget used = %d, want 1: the keep queued before round 2 counts", used)
	}

	buildQueuedSlice(t, fx, "fix-2-r1-f1", "beta/beta.go")
	round2Path := filepath.Join(t.TempDir(), "round-2.json")
	writeReviewResultAt(t, round2Path, verifydeliver.ReviewResult{ReviewedPaths: []string{"beta/beta.go"}, Summary: "round 2: nothing new"})
	if out, code = roundThroughMain(t, e, fx, round2Path); code != 0 {
		t.Fatalf("round 2 exit = %d, want 0\n%s", code, out)
	}
	_, notes, _, err := verifydeliver.OutstandingFindings(st, ticket)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("notes after round 2 = %+v, want none: r1-f1 is built and r1-f2 dismissed", notes)
	}
	findingsFile := filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-2", "findings.yaml")
	if triage, _, found := findingsYAMLEntry(t, findingsFile, "r1-f2"); !found || triage != verifydeliver.TriageHuman {
		t.Errorf("round 2 findings.yaml r1-f2 = (%q, %v), want a human's dismissal recorded", triage, found)
	}
}
