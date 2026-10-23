package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// findingsYAMLCleared reads only the "cleared" list out of a gate round's
// findings.yaml, for asserting what a round actually cleared: the
// round's own report/findings tables list only findings reported that
// round, so they can never show whether an unreported open finding cleared
// or merely went unmentioned.
func findingsYAMLCleared(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Cleared []string `yaml:"cleared"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc.Cleared
}

// findingsYAMLEntry reads one finding's Triage and Decision fields from a
// gate round's findings.yaml, for asserting that a human's actual keep
// decision at the triage prompt - not merely its downstream effect on the
// finding's status - reached the persisted record.
func findingsYAMLEntry(t *testing.T, path, id string) (triage, decision string, found bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Findings []struct {
			ID       string `yaml:"id"`
			Triage   string `yaml:"triage"`
			Decision string `yaml:"decision"`
		} `yaml:"findings"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, f := range doc.Findings {
		if f.ID == id {
			return f.Triage, f.Decision, true
		}
	}
	return "", "", false
}

// reviewResultAt reads and parses one scenario round's review-result.json.
func reviewResultAt(t *testing.T, path string) verifydeliver.ReviewResult {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var res verifydeliver.ReviewResult
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return res
}

// writeReviewResultAt marshals res and writes it to path, filling a nil
// Findings, StillPresent or ReviewedPaths with [] first: ParseReviewResult
// rejects a JSON null for any of the three, and a struct literal built by
// hand - where a nil slice with no omitempty tag would otherwise marshal as
// null - has to write the empty list out explicitly.
func writeReviewResultAt(t *testing.T, path string, res verifydeliver.ReviewResult) {
	t.Helper()
	if res.Findings == nil {
		res.Findings = []verifydeliver.ResultFinding{}
	}
	if res.StillPresent == nil {
		res.StillPresent = []verifydeliver.StillPresentEntry{}
	}
	if res.ReviewedPaths == nil {
		res.ReviewedPaths = []string{}
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		t.Fatalf("marshal review result: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// withoutPrefix returns paths with every entry starting with prefix removed.
func withoutPrefix(paths []string, prefix string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !strings.HasPrefix(p, prefix) {
			out = append(out, p)
		}
	}
	return out
}

func hasSlice(slices []store.Slice, id string) bool {
	for _, s := range slices {
		if s.ID == id {
			return true
		}
	}
	return false
}

// roundThroughMain records one gate round through main the way a reviewer
// does it: `jig gate` opens it (with the flags given), and `jig review done
// --result` closes it with the reviewer's report in the file at result. It
// returns the output and exit code of `review done`; the opening must succeed.
func roundThroughMain(t *testing.T, e env, fx *fixture.Fixture, result string, openFlags ...string) (string, int) {
	t.Helper()
	open := append([]string{"gate", fx.Ticket}, openFlags...)
	open = append(open, "--store", fx.StoreDir)
	if out, code := runMain(t, e, "", open...); code != 0 {
		t.Fatalf("jig gate: exit = %d, want 0 (the round opens)\n%s", code, out)
	}
	return runMain(t, e, "", "review", "done", fx.Ticket, "--result", result, "--store", fx.StoreDir)
}

// TestRecordedGateRoundsThroughMain is the decisive end-to-end test of the
// findings bookkeeping through the recorded verbs: the reviewer's reports are
// the scenario's review-result.json files (testdata/fixture/scenario-branches/
// reviewer/), handed to `jig review done --result`, and the rounds are opened
// with `jig gate --yes`, which keeps what jig can route on its own, so the
// rounds keep the shape the interactive test they replace gave them.
//
// It drives three full gate rounds:
//   - round 1 (full): a fix kept, a second (low-risk) fix routed straight to a
//     note by the risk floor, an ask kept, and a genuine note - after a first
//     attempt whose reviewed_paths is missing a must_review path fails
//     REVIEW_INVALID, leaving the round open, and a corrected report (same
//     round number) succeeds; the two fix slices it appends are driven green
//     by `jig run`.
//   - round 2 (delta): the kept fix recurs through prior (its new fix
//     slice's goal names the previous one), the kept ask's file clears
//     since nothing new is reported there, and the low-risk fix is
//     re-reported through prior and, below the floor again, routes
//     straight to a note once more; the recurrence's new fix slice is driven
//     green by `jig run`.
//   - round 3: clean, with the round 1 note still on record.
func TestRecordedGateRoundsThroughMain(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	e := testEnv(fx.Home)
	ticket := fx.Ticket
	runArgs := []string{"run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}

	// --- build a, b, c, d to green (same shape as the e2e package's own
	// TestEndToEndTwice): a, b, d go straight through; c pauses on q-001.
	out, code := runMain(t, e, "", runArgs...)
	if code != 2 {
		t.Fatalf("run 1 exit = %d, want 2 (paused at q-001)\n%s", code, out)
	}
	answerArgs := []string{"run", ticket, "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	out, code = runMain(t, e, "", answerArgs...)
	if code != 0 {
		t.Fatalf("run (answer) exit = %d, want 0\n%s", code, out)
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	for _, s := range []string{"a", "b", "c", "d"} {
		state, err := st.ReadSliceState(ticket, s)
		if err != nil {
			t.Fatalf("read slice state %s: %v", s, err)
		}
		if state.State != "green" {
			t.Fatalf("slice %s state = %q, want green", s, state.State)
		}
	}

	// --- round 1, first attempt: reviewed_paths deliberately omits every
	// alpha/ path, which the scope diff (a and b both touched alpha.go)
	// definitely requires coverage of - REVIEW_INVALID, no round written.
	round1Path := filepath.Join(fx.ScenarioDir, "gate", "round-1", "review-result.json")
	correct := reviewResultAt(t, round1Path)
	broken := correct
	broken.ReviewedPaths = withoutPrefix(correct.ReviewedPaths, "alpha/")
	brokenPath := filepath.Join(t.TempDir(), "broken.json")
	writeReviewResultAt(t, brokenPath, broken)

	out, code = roundThroughMain(t, e, fx, brokenPath, "--yes")
	if code != 1 {
		t.Fatalf("round 1 first (broken) attempt: exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "code: REVIEW_INVALID") {
		t.Fatalf("round 1 first (broken) attempt: expected REVIEW_INVALID, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-1")); err == nil {
		t.Fatalf("a failed report must not create gate/round-1 on disk")
	}

	// The round is open, not lost: its request and its open record are in the
	// store, and everything opening it wrote is committed and pushed already
	// (the report's refusal wrote nothing more).
	for _, name := range []string{"gate.round-1.review.json", "gate.round-1.open.yaml"} {
		if _, err := os.Stat(filepath.Join(fx.StoreDir, "tickets", ticket, "work", name)); err != nil {
			t.Fatalf("the open round has no %s: %v", name, err)
		}
	}
	if status, err := gitx.Run(fx.StoreDir, "status", "--porcelain"); err != nil {
		t.Fatalf("store status after the refused report: %v", err)
	} else if status != "" {
		t.Fatalf("store working copy is dirty after the refused report:\n%s", status)
	}
	local, err := gitx.Run(fx.StoreDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("store rev-parse HEAD after the refused report: %v", err)
	}
	remote, err := gitx.Run(fx.StoreDir, "ls-remote", "origin", "refs/heads/main")
	if err != nil {
		t.Fatalf("store ls-remote origin main after the refused report: %v", err)
	}
	if fields := strings.Fields(remote); len(fields) == 0 || fields[0] != local {
		t.Fatalf("store local HEAD %s was not pushed to origin main (ls-remote: %q)", local, remote)
	}
	if subject, err := gitx.Run(fx.StoreDir, "log", "-1", "--format=%s"); err != nil {
		t.Fatalf("store log after the refused report: %v", err)
	} else if want := ticket + ": gate round 1 opened"; subject != want {
		t.Fatalf("store latest commit subject = %q, want %q", subject, want)
	}

	// --- round 1, corrected report (still round 1: the refused one wrote
	// nothing to gate/round-1/): the round was opened with --yes, so the fix
	// r1-f1 and the ask r1-f3, which both have a full build target, are kept;
	// r1-f2 never reaches the fix batch at all (the risk floor already routed
	// it to a note).
	out, code = runMain(t, e, "", "review", "done", ticket, "--result", round1Path, "--store", fx.StoreDir)
	if code != 0 {
		t.Fatalf("round 1 (corrected) exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "round: 1") || !strings.Contains(out, "verdict: fix-slices") || !strings.Contains(out, "scope: full") {
		t.Fatalf("round 1 report missing round/verdict/scope kv lines:\n%s", out)
	}
	for _, want := range []string{"r1-f1,open,", "r1-f2,noted,note,", "r1-f3,open,", "r1-f4,noted,"} {
		if !strings.Contains(out, want) {
			t.Fatalf("round 1 findings table missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "fix-1-alpha-test") {
		t.Fatalf("round 1 report missing the kept fix's slice fix-1-alpha-test:\n%s", out)
	}
	if !strings.Contains(out, "fix-1-r1-f3") {
		t.Fatalf("round 1 report missing the kept ask's slice fix-1-r1-f3:\n%s", out)
	}
	if strings.Contains(out, "needs_a_human[") && !strings.Contains(out, "needs_a_human[0]") {
		t.Fatalf("round 1 must leave nothing needing a human (the ask was kept):\n%s", out)
	}

	// The round's own report table only ever shows a finding's status, not
	// how it got there - assert directly against findings.yaml that the
	// ask was kept without a human: nobody was asked, and no decision text
	// was made up.
	triage, decision, found := findingsYAMLEntry(t, filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-1", "findings.yaml"), "r1-f3")
	if !found {
		t.Fatalf("round 1 findings.yaml missing r1-f3")
	}
	if triage != verifydeliver.TriageAuto || decision != "" {
		t.Fatalf("round 1 findings.yaml r1-f3 triage = %q, decision = %q, want auto and no decision text", triage, decision)
	}

	slices1, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatalf("ReadSlices after round 1: %v", err)
	}
	if !hasSlice(slices1, "fix-1-alpha-test") || !hasSlice(slices1, "fix-1-r1-f3") {
		t.Fatalf("expected slices fix-1-alpha-test and fix-1-r1-f3; got %+v", slices1)
	}
	if hasSlice(slices1, "fix-1-beta-test") {
		t.Fatalf("the noted (below-floor) fix (beta workspace) must not build its own slice; got %+v", slices1)
	}

	// --- drive both round 1 fix slices to green.
	out, code = runMain(t, e, "", runArgs...)
	if code != 0 {
		t.Fatalf("run (fix-1 slices) exit = %d, want 0\n%s", code, out)
	}
	for _, s := range []string{"fix-1-alpha-test", "fix-1-r1-f3"} {
		state, err := st.ReadSliceState(ticket, s)
		if err != nil {
			t.Fatalf("read slice state %s: %v", s, err)
		}
		if state.State != "green" {
			t.Fatalf("slice %s state = %q, want green", s, state.State)
		}
	}

	// --- round 2 (delta): the doc-comment fix recurs through prior; the tone
	// ask's file is reviewed with nothing new reported there and clears; the
	// low-risk fix is re-reported through prior and, below the floor again,
	// routes straight to a note once more.
	round2Path := filepath.Join(fx.ScenarioDir, "gate", "round-2", "review-result.json")
	out, code = roundThroughMain(t, e, fx, round2Path, "--yes")
	if code != 0 {
		t.Fatalf("round 2 exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "round: 2") || !strings.Contains(out, "verdict: fix-slices") || !strings.Contains(out, "scope: delta") {
		t.Fatalf("round 2 report missing round/verdict/scope kv lines:\n%s", out)
	}
	if !strings.Contains(out, "r1-f1,open,") {
		t.Fatalf("round 2 findings table missing the recurring r1-f1 still open:\n%s", out)
	}
	if !strings.Contains(out, "r1-f2,noted,note,") {
		t.Fatalf("round 2 findings table missing the noted r1-f2 repeat:\n%s", out)
	}
	if strings.Contains(out, "r1-f3") {
		t.Fatalf("round 2 must not re-report the cleared ask r1-f3:\n%s", out)
	}
	if !strings.Contains(out, "fix-2-alpha-test") {
		t.Fatalf("round 2 report missing the recurrence's new fix slice fix-2-alpha-test:\n%s", out)
	}

	// The round 2 report and findings table only ever list findings
	// reported that round, so r1-f3's absence from them (checked above)
	// proves nothing on its own - it is exactly as absent whether it
	// cleared or simply went unmentioned. Read the round's own findings.yaml
	// cleared list, which is the only place that distinguishes the two, and
	// which a mutant that lets a noted repeat (r1-f2, same file) block
	// clearing actually breaks.
	cleared2 := findingsYAMLCleared(t, filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-2", "findings.yaml"))
	foundCleared := false
	for _, id := range cleared2 {
		if id == "r1-f3" {
			foundCleared = true
		}
	}
	if !foundCleared {
		t.Fatalf("round 2 findings.yaml cleared = %v, want it to contain r1-f3 (the kept ask, cleared)", cleared2)
	}

	slices2, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatalf("ReadSlices after round 2: %v", err)
	}
	var fix2Goal string
	for _, s := range slices2 {
		if s.ID == "fix-2-alpha-test" {
			fix2Goal = s.Goal
		}
	}
	if fix2Goal == "" {
		t.Fatal("fix-2-alpha-test not found in slices.yaml")
	}
	if !strings.Contains(fix2Goal, "fix-1-alpha-test") {
		t.Fatalf("fix-2-alpha-test's goal must name the previous fix slice fix-1-alpha-test:\n%s", fix2Goal)
	}

	// --- drive the round 2 recurrence's fix slice to green.
	out, code = runMain(t, e, "", runArgs...)
	if code != 0 {
		t.Fatalf("run (fix-2-alpha-test) exit = %d, want 0\n%s", code, out)
	}
	state, err := st.ReadSliceState(ticket, "fix-2-alpha-test")
	if err != nil {
		t.Fatalf("read slice state fix-2-alpha-test: %v", err)
	}
	if state.State != "green" {
		t.Fatalf("slice fix-2-alpha-test state = %q, want green", state.State)
	}

	// --- round 3: clean. The scenario's own review-result.json reports
	// nothing new, and nothing is left open or asked, so the round clears.
	round3Path := filepath.Join(fx.ScenarioDir, "gate", "round-3", "review-result.json")
	out, code = roundThroughMain(t, e, fx, round3Path)
	if code != 0 {
		t.Fatalf("round 3 exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "round: 3") || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("round 3 report missing round/verdict kv lines:\n%s", out)
	}

	// The round 1 note is never routed, never cleared, and never blocks
	// clean: it must still be on record after round 3.
	round1FindingsMD, err := os.ReadFile(filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-1", "findings.md"))
	if err != nil {
		t.Fatalf("read gate/round-1/findings.md: %v", err)
	}
	if !strings.Contains(string(round1FindingsMD), "ClampPercent and Clamp could share a bounds-check helper later") {
		t.Fatalf("round 1 findings.md missing the note text:\n%s", round1FindingsMD)
	}
}

// TestFindingsDraftedWithJigFindingBecomeTheRound: a reviewer that reports as it
// goes - `jig finding` for each problem, `jig review done` with the paths it
// read and a summary - gets the same round as one that hands over a result
// file: the drafts are checked as they are taken (a finding with no title is
// refused at once), and the round is recorded from them.
func TestFindingsDraftedWithJigFindingBecomeTheRound(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	e := testEnv(fx.Home)
	ticket := fx.Ticket
	buildFixtureTicket(t, fx)
	flags := []string{"--store", fx.StoreDir}
	withFlags := func(args ...string) []string { return append(args, flags...) }

	// Nothing is open yet: a draft has no round to join.
	out, code := runMain(t, e, "", withFlags("finding", ticket, "--file", "alpha/alpha.go", "--line", "5", "--title", "t", "--action", "note", "--risk", "low", "--risk-rationale", "r")...)
	if code != 1 || !strings.Contains(out, "REVIEW_NOT_OPEN") {
		t.Fatalf("jig finding before a round is open: exit = %d, want 1 and REVIEW_NOT_OPEN\n%s", code, out)
	}

	if out, code := runMain(t, e, "", withFlags("gate", ticket)...); code != 0 {
		t.Fatalf("jig gate: exit = %d\n%s", code, out)
	}
	ctx, code := runMain(t, e, "", withFlags("review", "context", ticket, "--json")...)
	if code != 0 {
		t.Fatalf("review context: exit = %d\n%s", code, ctx)
	}
	var req struct {
		MustReview []string `json:"must_review"`
	}
	if err := json.Unmarshal([]byte(ctx), &req); err != nil {
		t.Fatalf("review context --json: %v\n%s", err, ctx)
	}

	out, code = runMain(t, e, "", withFlags("finding", ticket, "--file", "alpha/alpha.go", "--line", "5", "--action", "note", "--risk", "low", "--risk-rationale", "r")...)
	if code != 1 || !strings.Contains(out, "REVIEW_INVALID") || !strings.Contains(out, "empty title") {
		t.Fatalf("jig finding with no title: exit = %d, want 1 and REVIEW_INVALID naming the title\n%s", code, out)
	}
	out, code = runMain(t, e, "", withFlags("finding", ticket, "--file", "alpha/alpha.go", "--line", "5", "--title", "Add never says how it overflows", "--detail", "Say what happens past the int range.", "--action", "note", "--risk", "low", "--risk-rationale", "documentation only", "--oracle", "test")...)
	if code != 0 || !strings.Contains(out, "drafts: 1") {
		t.Fatalf("jig finding: exit = %d, want 0 and one draft\n%s", code, out)
	}
	out, code = runMain(t, e, "", withFlags("finding", ticket, "--still-present", "r9-f9", "--line", "3")...)
	if code != 1 || !strings.Contains(out, "does not name a finding") {
		t.Fatalf("jig finding --still-present of an id the round does not know: exit = %d, want 1\n%s", code, out)
	}
	out, code = runMain(t, e, "", withFlags("finding", ticket, "--still-present", "r1-f1", "--title", "t")...)
	if code != 2 || !strings.Contains(out, "VALIDATION_ERROR") {
		t.Fatalf("jig finding --still-present with a finding's flags: exit = %d, want 2\n%s", code, out)
	}

	// The drafts and a result file are two ways to report, not both.
	resultPath := filepath.Join(t.TempDir(), "r.json")
	writeReviewResultAt(t, resultPath, verifydeliver.ReviewResult{ReviewedPaths: req.MustReview})
	out, code = runMain(t, e, "", withFlags("review", "done", ticket, "--result", resultPath)...)
	if code != 2 || !strings.Contains(out, "VALIDATION_ERROR") {
		t.Fatalf("jig review done --result over drafts: exit = %d, want 2\n%s", code, out)
	}
	out, code = runMain(t, e, "", withFlags("review", "done", ticket, "--summary", "one note")...)
	if code != 1 || !strings.Contains(out, "reviewed_paths is missing must_review path") {
		t.Fatalf("jig review done without the paths read: exit = %d, want 1 naming the path missing\n%s", code, out)
	}
	out, code = runMain(t, e, "", withFlags("review", "done", ticket, "--reviewed", strings.Join(req.MustReview, ","), "--summary", "one note")...)
	if code != 0 || !strings.Contains(out, "verdict: clean") || !strings.Contains(out, "r1-f1,noted,") {
		t.Fatalf("jig review done from the drafts: exit = %d, want 0, clean, with the note r1-f1\n%s", code, out)
	}
	md, err := os.ReadFile(filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-1", "findings.md"))
	if err != nil || !strings.Contains(string(md), "Add never says how it overflows") {
		t.Fatalf("findings.md = %q (err %v), want the drafted note", md, err)
	}
}
