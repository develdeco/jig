package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// runMain runs Main in-process with a scripted stdin and returns its stdout
// and exit code. It never fails the test on a non-zero code: several steps
// below (the deliberately broken round 1 attempt) expect one.
func runMain(t *testing.T, stdin string, args ...string) (string, int) {
	t.Helper()
	var buf bytes.Buffer
	code := Main(args, &buf, strings.NewReader(stdin))
	return buf.String(), code
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

// writeReviewResultAt marshals res and writes it to path.
func writeReviewResultAt(t *testing.T, path string, res verifydeliver.ReviewResult) {
	t.Helper()
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

// TestGateReviewerRoundsThroughMain is S4's decisive test (digest B1-S4,
// design Q3): the real reviewer path (`--backend fake`, playing back
// testdata/fixture/scenario-branches/reviewer/), driven entirely through
// cmd/jig's own Main with stdinIsTerminal forced true and a scripted stdin,
// exactly as a real terminal session would answer the triage prompts.
//
// It drives three full gate rounds:
//   - round 1 (full): a fix kept, a second fix dismissed at the batch
//     prompt, an ask kept with a decision, and a note - after a first
//     attempt whose reviewed_paths is missing a must_review path fails
//     REVIEW_INVALID and a corrected retry (same round number) succeeds;
//     the two fix slices it appends are driven green by `jig run`.
//   - round 2 (delta): the kept fix recurs through prior (its new fix
//     slice's goal names the previous one), the kept ask's file clears
//     since nothing new is reported there, and the dismissed fix is
//     re-reported through prior and reaches no one (stays dismissed,
//     never appears in this round's own triage or report); the
//     recurrence's new fix slice is driven green by `jig run`.
//   - round 3: clean, with the round 1 note still on record.
func TestGateReviewerRoundsThroughMain(t *testing.T) {
	prevTerm := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	defer func() { stdinIsTerminal = prevTerm }()

	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "reviewer"})
	ticket := fx.Ticket

	gateArgs := func(extra ...string) []string {
		base := []string{"gate", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
		return append(base, extra...)
	}
	runArgs := []string{"run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}

	// --- build a, b, c, d to green (same shape as the e2e package's own
	// TestEndToEndTwice): a, b, d go straight through; c pauses on q-001.
	out, code := runMain(t, "", runArgs...)
	if code != 2 {
		t.Fatalf("run 1 exit = %d, want 2 (paused at q-001)\n%s", code, out)
	}
	answerArgs := []string{"run", ticket, "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}
	out, code = runMain(t, "", answerArgs...)
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
	writeReviewResultAt(t, round1Path, broken)

	out, code = runMain(t, "", gateArgs()...)
	if code != 1 {
		t.Fatalf("round 1 first (broken) attempt: exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "code: REVIEW_INVALID") {
		t.Fatalf("round 1 first (broken) attempt: expected REVIEW_INVALID, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(fx.StoreDir, ticket, "gate", "round-1")); err == nil {
		t.Fatalf("a failed round must not create gate/round-1 on disk")
	}

	// The failed attempt above still wrote review.json to the store's
	// working copy (verifydeliver.Gate returns before its own Push, so
	// nothing was committed or pushed). Store.Sync's leftover-commit fix
	// lives in PR A, not this branch (digest commit map, a02e1d9), so a
	// dirty local store here would otherwise fail the retry's own Sync
	// with "cannot pull with rebase: you have unstaged changes" - discard
	// it directly, exactly as an operator would with `git checkout .`
	// before rerunning a failed command.
	if _, err := gitx.Run(fx.StoreDir, "checkout", "--", "."); err != nil {
		t.Fatalf("discard the failed round's leftover store changes: %v", err)
	}
	if _, err := gitx.Run(fx.StoreDir, "clean", "-fd"); err != nil {
		t.Fatalf("clean the failed round's leftover untracked store files: %v", err)
	}

	// --- round 1, corrected retry (still round 1: the failed attempt above
	// wrote nothing to gate/round-1/): the real triage script - dismiss
	// fix r1-f2 at the batch prompt, keep ask r1-f3 with a decision.
	writeReviewResultAt(t, round1Path, correct)
	out, code = runMain(t, "r1-f2\nUse a warm, casual tone; no exclamation marks.\n", gateArgs()...)
	if code != 0 {
		t.Fatalf("round 1 (corrected) exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "round: 1") || !strings.Contains(out, "verdict: fix-slices") || !strings.Contains(out, "scope: full") {
		t.Fatalf("round 1 report missing round/verdict/scope kv lines:\n%s", out)
	}
	for _, want := range []string{"r1-f1,open,", "r1-f2,dismissed,", "r1-f3,open,", "r1-f4,noted,"} {
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
		t.Fatalf("round 1 must leave nothing needing a human (the ask was decided):\n%s", out)
	}

	slices1, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatalf("ReadSlices after round 1: %v", err)
	}
	if !hasSlice(slices1, "fix-1-alpha-test") || !hasSlice(slices1, "fix-1-r1-f3") {
		t.Fatalf("expected slices fix-1-alpha-test and fix-1-r1-f3; got %+v", slices1)
	}
	if hasSlice(slices1, "fix-1-beta-test") {
		t.Fatalf("the dismissed fix (beta workspace) must not build its own slice; got %+v", slices1)
	}

	// --- drive both round 1 fix slices to green.
	out, code = runMain(t, "", runArgs...)
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

	// --- round 2 (delta): the doc-comment fix recurs through prior (Enter
	// accepts the batch, since it is the round's only fix); the tone ask's
	// file is reviewed with nothing new reported there and clears; the
	// trim fix is re-reported through prior and, since its prior is
	// dismissed, stays dismissed and reaches no one.
	out, code = runMain(t, "\n", gateArgs()...)
	if code != 0 {
		t.Fatalf("round 2 exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "round: 2") || !strings.Contains(out, "verdict: fix-slices") || !strings.Contains(out, "scope: delta") {
		t.Fatalf("round 2 report missing round/verdict/scope kv lines:\n%s", out)
	}
	if !strings.Contains(out, "r1-f1,open,") {
		t.Fatalf("round 2 findings table missing the recurring r1-f1 still open:\n%s", out)
	}
	if !strings.Contains(out, "r1-f2,dismissed,") {
		t.Fatalf("round 2 findings table missing the dismissed r1-f2 repeat:\n%s", out)
	}
	if strings.Contains(out, "r1-f3") {
		t.Fatalf("round 2 must not re-report the cleared ask r1-f3:\n%s", out)
	}
	if !strings.Contains(out, "fix-2-alpha-test") {
		t.Fatalf("round 2 report missing the recurrence's new fix slice fix-2-alpha-test:\n%s", out)
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
	out, code = runMain(t, "", runArgs...)
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
	out, code = runMain(t, "", gateArgs()...)
	if code != 0 {
		t.Fatalf("round 3 exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "round: 3") || !strings.Contains(out, "verdict: clean") {
		t.Fatalf("round 3 report missing round/verdict kv lines:\n%s", out)
	}

	// The round 1 note is never routed, never cleared, and never blocks
	// clean: it must still be on record after round 3.
	round1FindingsMD, err := os.ReadFile(filepath.Join(fx.StoreDir, ticket, "gate", "round-1", "findings.md"))
	if err != nil {
		t.Fatalf("read gate/round-1/findings.md: %v", err)
	}
	if !strings.Contains(string(round1FindingsMD), "ClampPercent and Clamp could share a bounds-check helper later") {
		t.Fatalf("round 1 findings.md missing the note text:\n%s", round1FindingsMD)
	}
}

func hasSlice(slices []store.Slice, id string) bool {
	for _, s := range slices {
		if s.ID == id {
			return true
		}
	}
	return false
}
