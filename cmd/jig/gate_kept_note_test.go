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
	if code != 0 || !strings.Contains(out, "fix_slices") || !strings.Contains(out, "fix-2-beta-test") {
		t.Fatalf("jig gate: exit = %d, want 0 and the kept note's slice fix-2-beta-test in fix_slices\n%s", code, out)
	}
	if strings.Contains(out, "fix-2-alpha-test") {
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
	if !hasSlice(slices, "fix-2-beta-test") || hasSlice(slices, "fix-2-alpha-test") {
		t.Fatalf("slices = %+v, want fix-2-beta-test and no slice for the dismissed note", slices)
	}

	// The slice is open work: a second gate before it is green refuses and
	// queues nothing more.
	if out, code = runMain(t, e, "", "gate", ticket, "--store", fx.StoreDir); code != 1 || !strings.Contains(out, "GATE_NOT_GREEN") {
		t.Fatalf("jig gate with the kept slice unbuilt: exit = %d, want 1 GATE_NOT_GREEN\n%s", code, out)
	}

	buildQueuedSlice(t, fx, "fix-2-beta-test", "beta/beta.go")
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
		if s.ID != "fix-2-beta-test" && len(s.Findings) > 0 && s.Findings[0] == "r1-f1" {
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
	if code != 0 || !strings.Contains(out, "fix-2-beta-test") {
		t.Fatalf("jig gate exit = %d, want 0 and the fix slice fix-2-beta-test built from the kept note\n%s", code, out)
	}
	if strings.Contains(out, "fix-2-alpha-test") {
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
		t.Errorf("fix budget used = %d, want 1: the keep queued before round 2 counts", used)
	}

	buildQueuedSlice(t, fx, "fix-2-beta-test", "beta/beta.go")
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

// TestReviewContextAfterAKeepNamesTheQueuedSlice: the route a session takes
// after a keep (jig next says review, the review skill reads the round with
// `jig review context`) must not queue the slice and then fail: it names the
// slice and says no round is open.
func TestReviewContextAfterAKeepNamesTheQueuedSlice(t *testing.T) {
	t.Parallel()
	fx, e := keptNoteAfterRound1(t)
	if out, code := runMain(t, e, "", "triage", fx.Ticket, "r1-f1", "keep", "--reason", "Worth doing now.", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig triage keep: exit = %d\n%s", code, out)
	}
	out, code := runMain(t, e, "", "review", "context", fx.Ticket, "--model", "m", "--store", fx.StoreDir)
	if code != 0 || strings.Contains(out, "REVIEW_NOT_OPEN") || !strings.Contains(out, "fix-2-beta-test") {
		t.Fatalf("jig review context after a keep: exit = %d, want 0 naming the queued slice fix-2-beta-test\n%s", code, out)
	}
}

// TestAKeptAskQueuedByTheGateIsStillDecided: once the gate queued a kept ask's
// slice, the ask is decided work, not a decision still owed: `jig next` does
// not stop at undecided asks and `jig status` does not ask for the decision
// again.
func TestAKeptAskQueuedByTheGateIsStillDecided(t *testing.T) {
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
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 || !strings.Contains(out, "fix-2-alpha-test") {
		t.Fatalf("jig gate: exit = %d, want the slice queued\n%s", code, out)
	}
	buildQueuedSlice(t, fx, "fix-2-alpha-test", "alpha/alpha.go")
	next, _ := runMain(t, e, "", "next", fx.Ticket, "--store", fx.StoreDir)
	if strings.Contains(next, "PHASE_ASKS_UNDECIDED") {
		t.Fatalf("jig next stops at an undecided ask after the keep was queued:\n%s", next)
	}
	status, _ := runMain(t, e, "", "status", fx.Ticket, "--store", fx.StoreDir)
	if strings.Contains(status, "undecided ask") {
		t.Fatalf("jig status asks for a decision again after the keep was queued:\n%s", status)
	}
}

// TestALaterGateLeavesASpentKeepAloneWhileItQueuesAnother: a keep a gate
// queued is spent. Keeping a second note later queues only the second note's
// slice; the first keep, still a note until a round closes, is not queued again.
func TestALaterGateLeavesASpentKeepAloneWhileItQueuesAnother(t *testing.T) {
	t.Parallel()
	fx, e := keptNoteAfterRound1(t)
	ticket := fx.Ticket
	keep := func(id string) {
		t.Helper()
		if out, code := runMain(t, e, "", "triage", ticket, id, "keep", "--reason", "Do it.", "--store", fx.StoreDir); code != 0 {
			t.Fatalf("jig triage keep %s: exit = %d\n%s", id, code, out)
		}
	}
	keep("r1-f1")
	if out, code := runMain(t, e, "", "gate", ticket, "--store", fx.StoreDir); code != 0 || !strings.Contains(out, "fix-2-beta-test") {
		t.Fatalf("first gate: exit = %d, want fix-2-beta-test queued\n%s", code, out)
	}
	buildQueuedSlice(t, fx, "fix-2-beta-test", "beta/beta.go")
	keep("r1-f2")
	out, code := runMain(t, e, "", "gate", ticket, "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(out, "fix-2-alpha-test") {
		t.Fatalf("second gate: exit = %d, want fix-2-alpha-test queued\n%s", code, out)
	}
	if strings.Contains(out, "fix-2-beta-test") {
		t.Fatalf("second gate queued the spent keep again:\n%s", out)
	}
}

// TestTheFixBudgetCountsTheRoundAKeepWasQueuedForAfterItCloses: the keep queued
// before round 2 opened counts as that round's fix, and still does once round
// 2 is recorded with no fix slice of its own.
func TestTheFixBudgetCountsTheRoundAKeepWasQueuedForAfterItCloses(t *testing.T) {
	t.Parallel()
	fx, e := keptNoteAfterRound1(t)
	if out, code := runMain(t, e, "", "triage", fx.Ticket, "r1-f1", "keep", "--reason", "Do it.", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig triage keep: exit = %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig gate: exit = %d\n%s", code, out)
	}
	buildQueuedSlice(t, fx, "fix-2-beta-test", "beta/beta.go")
	round2 := filepath.Join(t.TempDir(), "round-2.json")
	writeReviewResultAt(t, round2, verifydeliver.ReviewResult{ReviewedPaths: []string{"beta/beta.go"}, Summary: "round 2: nothing new"})
	if out, code := roundThroughMain(t, e, fx, round2); code != 0 {
		t.Fatalf("round 2 exit = %d\n%s", code, out)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if used, _ := verifydeliver.UsedFixBudget(st, fx.Ticket, 3); used != 1 {
		t.Errorf("fix budget used after round 2 = %d, want 1", used)
	}
}

// TestAQueuedNoteWhoseFixLandsInAnotherFileIsNotQueuedTwice: the built slice
// changed a file other than the note's, so the note's own file is still read
// (must_review) and the keep reads as built, not as a note to queue again.
func TestAQueuedNoteWhoseFixLandsInAnotherFileIsNotQueuedTwice(t *testing.T) {
	t.Parallel()
	fx, e := keptNoteAfterRound1(t)
	if out, code := runMain(t, e, "", "triage", fx.Ticket, "r1-f1", "keep", "--reason", "Do it.", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig triage keep: exit = %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig gate: exit = %d\n%s", code, out)
	}
	buildQueuedSlice(t, fx, "fix-2-beta-test", "alpha/alpha.go")
	round2 := filepath.Join(t.TempDir(), "round-2.json")
	writeReviewResultAt(t, round2, verifydeliver.ReviewResult{ReviewedPaths: []string{"alpha/alpha.go", "beta/beta.go"}, Summary: "round 2: nothing new"})
	out, code := roundThroughMain(t, e, fx, round2)
	if code != 0 || strings.Contains(out, "fix-2-beta-test-2") || strings.Contains(out, "fix-3-beta-test") {
		t.Fatalf("round 2 exit = %d, want 0 and no second slice for r1-f1\n%s", code, out)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	_, notes, _, err := verifydeliver.OutstandingFindings(st, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n.ID == "r1-f1" {
			t.Errorf("r1-f1 is still a note after its slice was built and round 2 read its file")
		}
	}
}

// TestAQueuedKeepIsSpentOnceItsRoundCloses: while the round a keep was queued
// for is open, the request's open row for the note carries the keep; after the
// round closes with the note still reported, the keep is spent: `jig status`
// no longer lists it as a decided note.
func TestAQueuedKeepIsSpentOnceItsRoundCloses(t *testing.T) {
	t.Parallel()
	fx, e := keptNoteAfterRound1(t)
	if out, code := runMain(t, e, "", "triage", fx.Ticket, "r1-f1", "keep", "--reason", "Do it.", "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig triage keep: exit = %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig gate: exit = %d\n%s", code, out)
	}
	buildQueuedSlice(t, fx, "fix-2-beta-test", "beta/beta.go")
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("jig gate (round 2 opens): exit = %d\n%s", code, out)
	}
	ctx, code := runMain(t, e, "", "review", "context", fx.Ticket, "--json", "--store", fx.StoreDir)
	if code != 0 || !strings.Contains(ctx, `"decision": "keep"`) && !strings.Contains(ctx, `"decision":"keep"`) {
		t.Fatalf("review context: exit = %d, want the queued keep on the note's open row\n%s", code, ctx)
	}
	// Round 2 reports the note again and closes; the keep is spent.
	round2 := filepath.Join(t.TempDir(), "round-2.json")
	writeReviewResultAt(t, round2, verifydeliver.ReviewResult{
		ReviewedPaths: []string{"beta/beta.go"},
		StillPresent:  []verifydeliver.StillPresentEntry{{Prior: "r1-f1", Line: 6}},
		Summary:       "round 2: the note still stands",
	})
	if out, code := runMain(t, e, "", "review", "done", fx.Ticket, "--result", round2, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("round 2 exit = %d\n%s", code, out)
	}
	status, code := runMain(t, e, "", "status", fx.Ticket, "--store", fx.StoreDir)
	if code != 0 || strings.Contains(status, "decided_notes") {
		t.Fatalf("status after round 2: exit = %d, want no decided_notes for a spent keep\n%s", code, status)
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
