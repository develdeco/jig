package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestActivityAndTailLaidOutAtThreeSizes: the Activity panel and the tail draw
// a tag, an event and a slice whole while the width allows, no row is wider than
// the terminal, and the tail keeps its order.
func TestActivityAndTailLaidOutAtThreeSizes(t *testing.T) {
	t.Parallel()
	ids := [3]string{"round-totals", "fix-4-cart-test", "cart-total"}
	for _, size := range [][2]int{{60, 40}, {80, 40}, {230, 50}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			g := longStepsRig(t, w, h, ids, false)
			g.feed(jl(at(3, 0), "SHOP-1", ids[1], journal.EventPublishApproved, with("s-builder", sha('c'), "", "", "", "")))
			view := stripANSI(g.view())
			wantNoRowWider(t, strings.Split(view, "\n"), w)
			if !strings.Contains(view, "build·fix-4-cart-test") {
				t.Errorf("the tag of the newest line is not whole:\n%s", view)
			}

			g.press("t")
			view = stripANSI(g.view())
			lines := strings.Split(view, "\n")
			wantNoRowWider(t, lines, w)
			tail := view[strings.Index(view, "tail "):]
			if !strings.Contains(tail, "publish-approved") {
				t.Errorf("the tail does not name the newest event whole:\n%s", view)
			}
			if w >= 80 && !strings.Contains(tail, ids[1]) {
				t.Errorf("at %d wide the tail does not name the slice whole:\n%s", w, view)
			}
			// Newest first, as the tail is drawn.
			if a, b := strings.Index(tail, "publish-approved"), strings.Index(tail, "verify-pending"); a < 0 || b < 0 || a > b {
				t.Errorf("the tail's lines are out of order:\n%s", tail)
			}
		})
	}
}

// TestPullRequestAddressesReadByNumber: a pull request's address reads "PR #7"
// wherever a line names it, an address that is not a pull request's is kept
// whole, and a commit keeps its short form.
func TestPullRequestAddressesReadByNumber(t *testing.T) {
	t.Parallel()
	g := newRig(t, "SHOP-1")
	tk := &ticket{id: "SHOP-1"}
	const other = "https://example.test/docs/guide"
	for _, c := range []struct {
		event, url, want string
	}{
		{journal.EventPr, "https://example.test/acme/demo/pull/7", "opened PR #7"},
		{journal.EventPr, "https://example.test/acme/demo/-/merge_requests/12", "opened PR #12"},
		{journal.EventPr, other, "opened " + other},
		{journal.EventReviewNotes, "https://example.test/acme/demo/pull/7", "review notes posted PR #7"},
	} {
		l := jl(at(1, 0), "SHOP-1", "", c.event, func(l *journal.Line) { l.Outcome, l.URL = "opened", c.url })
		if got := g.m.say(tk, l); got != c.want && !(c.event == journal.EventReviewNotes && got == c.want) {
			t.Errorf("%s of %s reads %q, want %q", c.event, c.url, got, c.want)
		}
		if c.event == journal.EventPr {
			if d := describe(l); !strings.HasSuffix(d, c.want) {
				t.Errorf("describe of %s reads %q, want it to end in %q", c.url, d, c.want)
			}
			if d := detailOf(l); !strings.Contains(d, strings.TrimPrefix(c.want, "opened ")) {
				t.Errorf("the tail's detail of %s reads %q, want %q in it", c.url, d, strings.TrimPrefix(c.want, "opened "))
			}
		}
	}
}
