package phase

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

func refusedLine(code, summary string) journal.Line {
	return journal.Line{Event: journal.EventGateRefused, Code: code, Summary: summary}
}

// TestReviewGateRefused: a gate-refused line is the review's word while no
// later line supersedes it, and says why the round did not open; a round that
// is open or clean reads as before.
func TestReviewGateRefused(t *testing.T) {
	t.Parallel()
	review := func(f Facts, lines []journal.Line) Phase { return Derive(f, lines).Of(Review) }
	refused := append(built(), refusedLine("INTENT_CONFLICT", "ticket SHOP-1 already has a brief.md"))

	t.Run("text and sub-step", func(t *testing.T) {
		got := review(allGreen(), refused)
		if got.Text != "gate refused: ticket SHOP-1 already has a brief.md" {
			t.Errorf("text = %q", got.Text)
		}
		if s := sub(t, got, "round:1"); s.State != SubNeedsYou || s.Detail != "INTENT_CONFLICT" {
			t.Errorf("round 1 = %+v, want needs-you with the code", s)
		}
	})
	t.Run("each code's state", func(t *testing.T) {
		for code, want := range map[string]string{"INTENT_CONFLICT": SubNeedsYou, "GATE_NOT_GREEN": SubNeedsYou, "BRANCH_NOT_FOUND": SubWaiting} {
			got := review(allGreen(), append(built(), refusedLine(code, "x")))
			if s := sub(t, got, "round:1"); s.State != want || s.Detail != code {
				t.Errorf("%s: round 1 = %+v, want state %s", code, s, want)
			}
		}
	})
	t.Run("the latest refusal replaces the earlier", func(t *testing.T) {
		got := review(allGreen(), append(refused, refusedLine("BRANCH_NOT_FOUND", "branch gone")))
		if got.Text != "gate refused: branch gone" {
			t.Errorf("text = %q", got.Text)
		}
	})
	t.Run("a refusal before the build is read as the build's", func(t *testing.T) {
		got := review(approved(), []journal.Line{refusedLine("GATE_NOT_GREEN", "slice a is queued, not green")})
		if got.Text != "gate refused: slice a is queued, not green" {
			t.Errorf("text = %q", got.Text)
		}
	})

	t.Run("a line that moves the gate or the build supersedes it", func(t *testing.T) {
		for _, l := range []journal.Line{
			ln(journal.EventGateOpen, attempt(1)),
			ln(journal.EventClaim, onSlice("a")),
			ln(journal.EventOracle, onSlice("a")),
			ln(journal.EventVerified, onSlice("a"), commit("abcdef0123")),
			ln(journal.EventVerifyError, onSlice("a")),
			ln(journal.EventFixSlice, onSlice("r1-fix-1")),
			ln(journal.EventRequeue, onSlice("a")),
			ln(journal.EventRestamp),
		} {
			got := review(allGreen(), append(append([]journal.Line{}, refused...), l))
			if strings.HasPrefix(got.Text, "gate refused") {
				t.Errorf("after %s the text is still %q", l.Event, got.Text)
			}
		}
	})
	t.Run("a note, an ask or a question does not", func(t *testing.T) {
		for _, ev := range []string{journal.EventNote, journal.EventQuestion, journal.EventAnswer, journal.EventTriage} {
			got := review(allGreen(), append(append([]journal.Line{}, refused...), ln(ev)))
			if !strings.HasPrefix(got.Text, "gate refused") {
				t.Errorf("after %s the text is %q, want the refusal", ev, got.Text)
			}
		}
	})

	t.Run("an open round reads as before", func(t *testing.T) {
		open := allGreen()
		open.Open = &OpenRound{Round: 1}
		got := review(open, append(append([]journal.Line{}, refused...), ln(journal.EventGateOpen, attempt(1))))
		if got.Text != "round 1 open · waiting for a reviewer" {
			t.Errorf("text = %q", got.Text)
		}
		// A refusal line the open round's own journal still carries (an
		// older one) does not outrank the open round.
		got = review(open, refused)
		if got.Text != "round 1 open · waiting for a reviewer" {
			t.Errorf("text with the refusal after the open round = %q", got.Text)
		}
	})
	t.Run("a clean round reads as before", func(t *testing.T) {
		lines := append(built(), ln(journal.EventGateOpen, attempt(1)), ln(journal.EventGateClean, attempt(1)), refusedLine("INTENT_CONFLICT", "x"))
		got := review(allGreen(), lines)
		if got.Text != "round 1 clean" {
			t.Errorf("text = %q", got.Text)
		}
	})

	t.Run("the next reason says it", func(t *testing.T) {
		a := Derive(allGreen(), refused).For(Publish)
		if a.Code != CodeNotReviewed || a.Reason != "gate refused: ticket SHOP-1 already has a brief.md" {
			t.Errorf("publish waits for %s: %q", a.Code, a.Reason)
		}
	})
}
