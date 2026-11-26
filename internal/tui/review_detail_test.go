package tui

import (
	"strings"
	"testing"
)

// TestReviewScreenIsARowPerRoundAndTheCursorRoundWhole: at 100x30 the review of a
// ticket in its fix round has a row for each of its two rounds and for its ask,
// the cursor goes down through the rounds and then the asks, and the detail under
// the lists is the cursor row's, whole, in the first screenful.
func TestReviewScreenIsARowPerRoundAndTheCursorRoundWhole(t *testing.T) {
	t.Parallel()
	g := reviewFixRound(t, 100, 30)
	view := g.view()
	rounds := between(view, "Rounds", "╰")
	if !strings.Contains(rounds, "round 2") || !strings.Contains(rounds, "round 1") {
		t.Errorf("the Rounds list has no row for each round:\n%s", view)
	}
	if !strings.Contains(between(view, "Asks", "╰"), "Retry loop never ends") {
		t.Errorf("the Asks list has no row for the ask:\n%s", view)
	}
	if ref := g.m.selected(); ref == nil || ref.kind != rowAsk || ref.id != "r2-f2" {
		t.Fatalf("opening the review puts the cursor on %+v, want the ask", ref)
	}
	g.press("up", "up")
	if ref := g.m.selected(); ref == nil || ref.kind != rowRound || ref.id != "2" {
		t.Fatalf("two ups from the ask put the cursor on %+v, want round 2", ref)
	}
	view = g.view()
	detail := readWords(between(view, "╭─ Detail · round 2", "╰"))
	for _, want := range []string{
		"reviewed by reviewer (Claude Code · Opus 5) · independent",
		"range delta ccccccc..ddddddd · 3 files read",
		"medium alpha/c.go:7 Check the returned error → to fix",
		"the error is dropped and the caller carries on",
		"low alpha/c.go:2 Rename the helper → noted",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the detail of round 2 lacks %q:\n%s", want, view)
		}
	}
	if h := len(strings.Split(view, "\n")); h != 30 {
		t.Errorf("the screen is %d rows, want 30", h)
	}
	for _, gone := range []string{"Sent back", "Findings", "unprobed", "probes"} {
		if strings.Contains(view, gone) {
			t.Errorf("the screen still has %q:\n%s", gone, view)
		}
	}
	g.press("down")
	if !strings.Contains(g.view(), "╭─ Detail · round 1") {
		t.Errorf("down from round 2 does not move the detail to round 1:\n%s", g.view())
	}
}

// TestARoundDetailPairsEachFindingWithTheFixSliceItBecame: under a finding the
// detail names the fix slice whose recorded finding ids name it and the state
// that slice is in; a finding that became none has none.
func TestARoundDetailPairsEachFindingWithTheFixSliceItBecame(t *testing.T) {
	t.Parallel()
	g := reviewFixRound(t, 100, 40)
	g.press("up", "up")
	detail := readWords(between(g.view(), "╭─ Detail · round 2", "╰"))
	// The finding, its reason, the fix slice it became and the next finding, in
	// that order.
	at := -1
	for _, want := range []string{"Check the returned error → to fix", "the error is dropped and the caller carries on", "fix slice fix-2-alpha · queued", "Retry loop never ends"} {
		next := strings.Index(detail, want)
		if next <= at {
			t.Errorf("%q is not after what comes before it in the detail:\n%s", want, detail)
		}
		at = next
	}
	if strings.Contains(detail, "fix-1-alpha") {
		t.Errorf("a fix slice of another round's finding is in the detail:\n%s", detail)
	}
	note := detail[strings.Index(detail, "Rename the helper"):]
	if strings.Contains(note, "fix slice") {
		t.Errorf("a note that became no fix slice has one:\n%s", detail)
	}
}

// TestAnAskRowsDetailIsItsFindingWhole: the detail of the ask under the cursor is
// its finding whole, and k and d decide it from there.
func TestAnAskRowsDetailIsItsFindingWhole(t *testing.T) {
	t.Parallel()
	g := reviewFixRound(t, 80, 24)
	view := g.view()
	detail := readWords(between(view, "╭─ Detail · r2-f2", "╰"))
	for _, want := range []string{
		"finding high · alpha/c.go:30 · Retry loop never ends",
		"reason a failing call is retried forever",
		"waits because the fix budget is not reached, so a person decides",
		"decision ! undecided",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the detail of the ask lacks %q:\n%s", want, view)
		}
	}
	g.press("k")
	if got := triaged(g); len(got) != 1 || !strings.Contains(got[0], "triage JIG-2 r2-f2 keep") {
		t.Errorf("k on the ask ran %q, notice %q", got, g.m.notice)
	}
	g.press("d")
	if got := triaged(g); len(got) != 2 || !strings.Contains(got[1], "triage JIG-2 r2-f2 dismiss") {
		t.Errorf("d on the ask ran %q, notice %q", got, g.m.notice)
	}
}

// TestARoundTheStatusHasNoFindingsOfSaysItsCounts: the status carries the findings
// of the latest round only, so an earlier round says what it raised in counts and
// invents no finding.
func TestARoundTheStatusHasNoFindingsOfSaysItsCounts(t *testing.T) {
	t.Parallel()
	g := reviewFixRound(t, 100, 40)
	g.press("up")
	view := g.view()
	detail := readWords(between(view, "╭─ Detail · round 1", "╰"))
	_, detail, _ = strings.Cut(detail, "╮") // the rows, not the verdict on the top edge
	if !strings.Contains(detail, "raised 1 fix · 1 ask · 1 note") {
		t.Errorf("round 1 does not say its counts:\n%s", view)
	}
	for _, invented := range []string{"findings", "→", "fix slice", "alpha/"} {
		if strings.Contains(detail, invented) {
			t.Errorf("round 1 names a finding the status does not carry (%q):\n%s", invented, view)
		}
	}
}
