package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/land"
)

// TestSuitePreconditionNextShowsTheRowsAndTheFixTask: an issue waiting on its
// suite shows the unmet row and the CI line; a failed suite also names the fix
// task (an entry blocked_by merged on the chart's last entries); a waived suite
// is shown with its reason; a task that is not an issue gets none of it.
func TestSuitePreconditionNextShowsTheRowsAndTheFixTask(t *testing.T) {
	t.Parallel()
	notRun := nextTicket{Ticket: "I-1", Kind: "issue", State: land.StateWaiting, Prompt: "Run it",
		Unmet: []nextUnmet{{Code: land.SuiteNotRun, Why: "no run at abc", Prompt: "Run it"}}}
	out := strings.Join(notRun.notATaskBlocks("next"), "\n")
	for _, want := range []string{"LAND_SUITE_NOT_RUN", "I-1 waits for no run at abc", "need not run the whole suite in CI"} {
		if !strings.Contains(out, want) {
			t.Errorf("not-run output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "fix task") {
		t.Errorf("a suite not run names a fix task:\n%s", out)
	}

	failed := nextTicket{Ticket: "I-1", Kind: "issue", State: land.StateWaiting, Prompt: "Add a fix task for I-1",
		Unmet: []nextUnmet{{Code: land.SuiteFailed, Why: "failed", Prompt: "Add a fix task for I-1"}}}
	out = strings.Join(failed.notATaskBlocks("next"), "\n")
	for _, want := range []string{"LAND_SUITE_FAILED", "add a fix task", "blocked_by is merged on the chart's last entries", "jig graduate"} {
		if !strings.Contains(out, want) {
			t.Errorf("failed output lacks %q:\n%s", want, out)
		}
	}

	waived := nextTicket{Ticket: "I-1", Kind: "issue", State: land.StateReady, Prompt: "Publish I-1", Waived: []string{"suite of app waived at abc by person: ci runs it"}}
	out = strings.Join(waived.notATaskBlocks("next"), "\n")
	if !strings.Contains(out, "waived:") || !strings.Contains(out, "ci runs it") {
		t.Errorf("waived output does not show the waiver:\n%s", out)
	}
}
