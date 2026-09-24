package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestPrintGateReportShowsFindingsWithFileLineAndRationale pins the rule
// that findings are always shown sorted by risk, high first, each
// with its rationale: the gate report's own findings table and its
// needs_a_human table must carry file:line and risk_rationale, not just
// title - the triage prompt and notes/fixes tables are covered by
// triage_test.go.
func TestPrintGateReportShowsFindingsWithFileLineAndRationale(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	report := verifydeliver.GateReport{
		Round:   1,
		Verdict: "fix-slices",
		Scope:   "full",
		Findings: []verifydeliver.Finding{
			{ID: "r1-f1", File: "alpha/alpha.go", Line: 5, Title: "FIX-TITLE", Status: verifydeliver.StatusOpen, Risk: "high", RiskRationale: "FIX-RATIONALE"},
		},
		NeedsHuman: []verifydeliver.Finding{
			{ID: "r1-f2", File: "beta/beta.go", Line: 4, Title: "ASK-TITLE", Status: verifydeliver.StatusAsked, Risk: "medium", RiskRationale: "ASK-RATIONALE"},
		},
	}

	var out bytes.Buffer
	printGateReport(&out, st, fx.Ticket, report)
	text := out.String()

	for _, want := range []string{"alpha/alpha.go:5", "FIX-RATIONALE", "beta/beta.go:4", "ASK-RATIONALE"} {
		if !strings.Contains(text, want) {
			t.Fatalf("gate report missing %q:\n%s", want, text)
		}
	}
}

// TestGateReportHintNamesDecidingAsksNotFixSlices pins the rule that a
// round leaving any ask undecided always names a human decision at a
// terminal as the way forward, never "work the fix-slice round" - true
// even when the round built no fix slices at all (every routed finding
// was an undecided ask).
func TestGateReportHintNamesDecidingAsksNotFixSlices(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	report := verifydeliver.GateReport{
		Round:   1,
		Verdict: "fix-slices",
		Scope:   "full",
		NeedsHuman: []verifydeliver.Finding{
			{ID: "r1-f1", File: "beta/beta.go", Line: 4, Title: "ASK-TITLE", Status: verifydeliver.StatusAsked, Risk: "medium", RiskRationale: "ASK-RATIONALE"},
		},
	}

	hint := strings.Join(gateReportHint(st, fx.Ticket, report), "\n")
	if strings.Contains(hint, "fix-slice round") {
		t.Fatalf("hint = %q, must not point at fix-slice work when only an ask is undecided", hint)
	}
	if !strings.Contains(hint, "jig gate "+fx.Ticket) || !strings.Contains(hint, "decide the listed asks") {
		t.Fatalf("hint = %q, want it to point at a terminal `jig gate` to decide the listed asks", hint)
	}
}

// TestGateReportHintNamesTheOrderWhenFixSlicesAreQueuedToo pins the rule
// that when the same round both queues fix slices (which checkFrontier
// refuses the next `jig gate` until green) and leaves an ask undecided, the
// hint names the order: work the fix slices first, then gate at a
// terminal. The printed `jig gate <ticket>` command alone cannot work in
// this state, since checkFrontier would refuse it.
func TestGateReportHintNamesTheOrderWhenFixSlicesAreQueuedToo(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	report := verifydeliver.GateReport{
		Round:   1,
		Verdict: "fix-slices",
		Scope:   "full",
		NeedsHuman: []verifydeliver.Finding{
			{ID: "r1-f1", File: "beta/beta.go", Line: 4, Title: "ASK-TITLE", Status: verifydeliver.StatusAsked, Risk: "medium", RiskRationale: "ASK-RATIONALE"},
		},
		FixSlices: []string{"fix-1-alpha-test"},
	}

	lines := gateReportHint(st, fx.Ticket, report)
	joined := strings.Join(lines, "\n")
	if len(lines) < 2 {
		t.Fatalf("hint = %v, want several lines naming the order", lines)
	}
	runIdx := strings.Index(joined, "jig run "+fx.Ticket)
	gateIdx := strings.Index(joined, "jig gate "+fx.Ticket)
	if runIdx < 0 || gateIdx < 0 || runIdx > gateIdx {
		t.Fatalf("hint = %q, want `jig run` named before `jig gate`", joined)
	}
}

// TestGateReportHintReadsTheFrontierNotTheFixSliceCount pins that the hint
// decides from the same frontier state checkFrontier applies, not from a
// proxy for it. A round queuing fix slices is only one way for the frontier
// to be short of green: `jig gate --early` reviews an unfinished frontier
// and can leave an ask undecided having queued nothing at all, which a
// fix-slice count reads as green while the check does not. The bare
// `jig gate <ticket>` the old hint printed there was refused with
// GATE_NOT_GREEN the moment anyone ran it.
func TestGateReportHintReadsTheFrontierNotTheFixSliceCount(t *testing.T) {
	report := verifydeliver.GateReport{
		Round:   1,
		Verdict: "fix-slices",
		Scope:   "full",
		NeedsHuman: []verifydeliver.Finding{
			{ID: "r1-f1", File: "beta/beta.go", Line: 4, Title: "ASK-TITLE", Status: verifydeliver.StatusAsked, Risk: "medium", RiskRationale: "ASK-RATIONALE"},
		},
		// No fix slices this round: the only thing holding the frontier
		// back is the unfinished slice below.
	}

	t.Run("frontier short of green names the order", func(t *testing.T) {
		t.Setenv("JIG_HOME", t.TempDir())
		fx := fixture.Generate(t, fixture.Opts{})
		st, err := store.Open(fx.StoreDir)
		if err != nil {
			t.Fatalf("store.Open: %v", err)
		}
		if err := st.WriteSliceState(fx.Ticket, "c", store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001"}); err != nil {
			t.Fatalf("write slice state c: %v", err)
		}

		lines := gateReportHint(st, fx.Ticket, report)
		joined := strings.Join(lines, "\n")
		if len(lines) < 2 {
			t.Fatalf("hint = %v, want several lines naming the order", lines)
		}
		runIdx := strings.Index(joined, "jig run "+fx.Ticket)
		gateIdx := strings.Index(joined, "jig gate "+fx.Ticket)
		if runIdx < 0 || gateIdx < 0 || runIdx > gateIdx {
			t.Fatalf("hint = %q, want `jig run` named before `jig gate`", joined)
		}
	})

	// A frontier parked on an unanswered question does not advance on a
	// bare `jig run <ticket>`: what frees it is the answer, which is what
	// `jig status` prints for the same state. The gate's hint must not
	// send a person to a command that cannot move anything.
	t.Run("frontier parked on a question names the answer", func(t *testing.T) {
		t.Setenv("JIG_HOME", t.TempDir())
		fx := fixture.Generate(t, fixture.Opts{})
		st, err := store.Open(fx.StoreDir)
		if err != nil {
			t.Fatalf("store.Open: %v", err)
		}
		if err := st.WriteSliceState(fx.Ticket, "c", store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001"}); err != nil {
			t.Fatalf("write slice state c: %v", err)
		}
		if err := st.WriteQuestion(fx.Ticket, store.Question{ID: "q-001", Slice: "c", Status: "open", Body: "casual or formal?"}); err != nil {
			t.Fatalf("write question: %v", err)
		}

		lines := gateReportHint(st, fx.Ticket, report)
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "--answer q-001") {
			t.Fatalf("hint = %q, want the answer that actually frees the frontier", joined)
		}
		want, err := nextStepHint(st, fx.Ticket)
		if err != nil {
			t.Fatalf("nextStepHint: %v", err)
		}
		if lines[0] != want {
			t.Fatalf("hint first line = %q, want the same next step `jig status` prints (%q)", lines[0], want)
		}
	})

	t.Run("green frontier names the gate directly", func(t *testing.T) {
		t.Setenv("JIG_HOME", t.TempDir())
		fx := fixture.Generate(t, fixture.Opts{})
		st, err := store.Open(fx.StoreDir)
		if err != nil {
			t.Fatalf("store.Open: %v", err)
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			if err := st.WriteSliceState(fx.Ticket, id, store.SliceState{State: "green", Attempts: 1}); err != nil {
				t.Fatalf("write slice state %s: %v", id, err)
			}
		}

		lines := gateReportHint(st, fx.Ticket, report)
		if len(lines) != 1 {
			t.Fatalf("hint = %v, want one line: the gate runs now", lines)
		}
		if strings.Contains(lines[0], "jig run ") {
			t.Fatalf("hint = %q, must not send a person to the frontier when it is already green", lines[0])
		}
		if !strings.Contains(lines[0], "jig gate "+fx.Ticket) {
			t.Fatalf("hint = %q, want the deciding command", lines[0])
		}
	})
}

// TestPrintGateReportListsWhatTheHintPointsAt pins that the tables print
// whenever they hold anything, not only on a round that recorded a scope.
// A round with no review to run sets no scope, so gating all three tables
// on it left a round that exits 2 telling the reader to "decide the listed
// asks" with nothing listed, while `jig status` and the stored round both
// named the ask.
func TestPrintGateReportListsWhatTheHintPointsAt(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	ask := verifydeliver.Finding{
		ID: "r1-f1", File: "alpha/alpha.go", Line: 4, Title: "ASK-TITLE",
		Status: verifydeliver.StatusAsked, Risk: "high", RiskRationale: "ASK-RATIONALE",
	}
	report := verifydeliver.GateReport{
		Round:      2,
		Verdict:    "fix-slices",
		Findings:   []verifydeliver.Finding{ask},
		NeedsHuman: []verifydeliver.Finding{ask},
		// No Scope: nothing was reviewed this round.
	}

	var buf bytes.Buffer
	code := printGateReport(&buf, st, fx.Ticket, report)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (an ask is undecided)", code)
	}
	out := buf.String()
	for _, want := range []string{"needs_a_human[1]", "findings[1]", "r1-f1", "ASK-TITLE", "ASK-RATIONALE"} {
		if !strings.Contains(out, want) {
			t.Errorf("gate report is missing %q; the hint points at findings it never listed:\n%s", want, out)
		}
	}
}
