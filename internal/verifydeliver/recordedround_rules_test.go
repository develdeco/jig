package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestRecordedRoundKeepsAFirstRoundFromCarryingAVerdict: a first round has no
// verdict before it to carry, whatever its repos say of their lines.
func TestRecordedRoundKeepsAFirstRoundFromCarryingAVerdict(t *testing.T) {
	t.Parallel()
	st := newReviewStore(t)
	prep := ReviewPrep{Repos: []RepoPrep{{Repo: "fixture-repo", Prior: "abc", Line: lineSame}}}
	origin, _, ok, err := carriedFrom(st, "JIG-1", 1, prep, "", "")
	if err != nil || ok || origin != 0 {
		t.Fatalf("carriedFrom of round 1 = origin %d, ok %v, err %v; want none", origin, ok, err)
	}
}

// TestRecordedRoundKeepsTheRecurrenceBoundToTheSliceThatFixedTheFinding: a
// finding's fix slice is the one that records its id, and only that one's
// state says whether the fix went green; a slice of other findings is not read.
func TestRecordedRoundKeepsTheRecurrenceBoundToTheSliceThatFixedTheFinding(t *testing.T) {
	t.Parallel()
	slices := []store.Slice{{ID: "fix-1-a", Findings: []string{"r1-f1"}}, {ID: "fix-1-b", Findings: []string{"r1-f2", "r1-f3"}}}
	var read []string
	green, err := findingHasGreenFixSlice("r1-f3", slices, func(id string) (bool, error) {
		read = append(read, id)
		return id == "fix-1-b", nil
	})
	if err != nil || !green || strings.Join(read, ",") != "fix-1-b" {
		t.Fatalf("findingHasGreenFixSlice = %v, %v, read %v; want green from fix-1-b alone", green, err, read)
	}
	if green, err := findingHasGreenFixSlice("r9-f9", slices, func(string) (bool, error) { return true, nil }); err != nil || green {
		t.Fatalf("findingHasGreenFixSlice of a finding no slice records = %v, %v; want false", green, err)
	}
}

// TestRecordedRoundKeepsOnlyTheFindingsNotKept: the outstanding findings of a
// round are those no kept fix slice took, in their order.
func TestRecordedRoundKeepsOnlyTheFindingsNotKept(t *testing.T) {
	t.Parallel()
	got := withoutIDs([]Finding{{ID: "r1-f1"}, {ID: "r1-f2"}, {ID: "r1-f3"}}, []string{"r1-f2"})
	if len(got) != 2 || got[0].ID != "r1-f1" || got[1].ID != "r1-f3" {
		t.Fatalf("withoutIDs = %+v, want r1-f1 and r1-f3", got)
	}
}

// TestRecordedRoundKeepsRisksInTheirOrder: the review notes list a round's
// findings critical first and an unknown risk last.
func TestRecordedRoundKeepsRisksInTheirOrder(t *testing.T) {
	t.Parallel()
	for i, risk := range []string{"critical", "High", "medium", "LOW", "whatever"} {
		if got := riskOrder(risk); got != i {
			t.Errorf("riskOrder(%q) = %d, want %d", risk, got, i)
		}
	}
}

// TestRecordedRoundKeepsAFixSliceSummaryEmptyUntilItHasAnAttempt: a slice
// that has not been attempted, or is not there, has no summary of a last attempt.
func TestRecordedRoundKeepsAFixSliceSummaryEmptyUntilItHasAnAttempt(t *testing.T) {
	t.Parallel()
	st := newReviewStore(t)
	if got := previousFixSliceSummary(st, "JIG-1", "fix-1-a"); got != "" {
		t.Fatalf("previousFixSliceSummary of an unattempted slice = %q, want none", got)
	}
	if err := st.WriteSliceState("JIG-1", "fix-1-a", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	if got := previousFixSliceSummary(st, "JIG-1", "fix-1-a"); got != "" {
		t.Fatalf("previousFixSliceSummary of an attempt with no result file = %q, want none", got)
	}
}

// TestRecordedRoundKeepsAPublishFromShippingABriefTheRoundNeverRead: a brief.md
// edited after the clean round that reviewed it is refused, naming the file,
// since the round reviewed the change against an intent that no longer stands.
func TestRecordedRoundKeepsAPublishFromShippingABriefTheRoundNeverRead(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	if report := recordedRound(t, d, GateOpts{Ticket: fx.Ticket}, func(p Prepared) ReviewResult { return cleanReport(p) }); report.Verdict != "clean" {
		t.Fatalf("round verdict = %q, want clean", report.Verdict)
	}
	briefPath := filepath.Join(d.Store.TicketDir(fx.Ticket), "brief.md")
	original, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatalf("read brief.md: %v", err)
	}
	if err := os.WriteFile(briefPath, append(original, []byte("\nEdited after the clean round.\n")...), 0o644); err != nil {
		t.Fatalf("edit brief.md: %v", err)
	}
	if err := d.Store.Push(fx.Ticket + ": edit brief.md after the clean round"); err != nil {
		t.Fatalf("push the edited brief.md: %v", err)
	}
	_, err = PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err == nil || !strings.Contains(err.Error(), briefPath) {
		t.Fatalf("PublishBody over an edited brief: err = %v, want a refusal naming %s", err, briefPath)
	}
}

// TestRecordedRoundKeepsAPublishFromReadingARoundThatIsNotThere: a ticket the
// gate has not reviewed has no latest round to publish from, and says so
// instead of publishing the branch unreviewed.
func TestRecordedRoundKeepsAPublishFromReadingARoundThatIsNotThere(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	_, err := PublishBody(d, PublishOpts{Ticket: fx.Ticket})
	if err == nil || !strings.Contains(err.Error(), "no gate rounds recorded") {
		t.Fatalf("PublishBody before any round: err = %v, want one saying no gate round is recorded", err)
	}
}
