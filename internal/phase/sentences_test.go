package phase

import (
	"fmt"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestRoundSentenceNamesFixesAndAsks: a round that left asks to decide says how
// many fixes it sent back and how many asks wait for the person, with the verb
// agreeing, and drops the fix clause when it sent none.
func TestRoundSentenceNamesFixesAndAsks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		fixes, asks int
		want        string
	}{
		{1, 1, "round 1: 1 fix sent back, 1 ask waits for you"},
		{2, 1, "round 1: 2 fixes sent back, 1 ask waits for you"},
		{1, 2, "round 1: 1 fix sent back, 2 asks wait for you"},
		{0, 2, "round 1: 2 asks wait for you"},
		{0, 1, "round 1: 1 ask waits for you"},
	}
	for _, c := range cases {
		f := allGreen()
		for i := 1; i <= c.fixes; i++ {
			f.Slices = append(f.Slices, Slice{ID: fmt.Sprintf("r1-fix-%d", i), State: "green", FromGate: 1})
		}
		for i := 1; i <= c.asks; i++ {
			f.Asks = append(f.Asks, Ask{ID: fmt.Sprintf("r1-a%d", i)})
		}
		lines := append(built(), ln(journal.EventGateOpen, attempt(1)), ln(journal.EventGateRound, attempt(1)))
		if got := Derive(f, lines).Of(Review).Text; got != c.want {
			t.Errorf("%d fixes, %d asks: text = %q, want %q", c.fixes, c.asks, got, c.want)
		}
	}

	// An ask already decided is not waiting: the count is the undecided ones.
	f := allGreen()
	f.Asks = []Ask{{ID: "r1-a1", Decision: "keep"}, {ID: "r1-a2"}}
	lines := append(built(), ln(journal.EventGateOpen, attempt(1)), ln(journal.EventGateRound, attempt(1)))
	if got := Derive(f, lines).Of(Review).Text; got != "round 1: 1 ask waits for you" {
		t.Errorf("one of two asks decided: text = %q", got)
	}
}

// TestChangedBriefSentenceSaysApproveAgain: a brief changed after its approval
// says so and what to do in words short enough to show whole; who approved it
// stays in the sub-step.
func TestChangedBriefSentenceSaysApproveAgain(t *testing.T) {
	t.Parallel()
	changed := approved()
	changed.Approval = ApprovalChanged
	got := Derive(changed, nil).Of(Brief)
	if got.Text != "brief changed after approval: approve again" {
		t.Errorf("text = %q", got.Text)
	}
	if s := sub(t, got, "approved"); s.Detail != "changed after Dana (person) approved it" {
		t.Errorf("approved sub = %+v, want the approver named", s)
	}
}
