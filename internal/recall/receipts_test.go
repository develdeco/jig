package recall

import (
	"fmt"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

func spendAt(ts, session, agent, slice string, peak int64) journal.Line {
	return journal.Line{TS: ts, Event: journal.EventSpend, Session: session, Agent: agent, Slice: slice, Spend: &journal.Spend{Input: 1, Peak: peak}}
}

// TestRecallReceiptsBuilderIsWhoSpentFromTheFirstClaim: the builder's peak is
// the largest among the sessions and agents with a spend line at or after the
// ticket's first claim, whatever slice the line names, less the subagents a
// round names as its reviewers; a session that reviewed and also claimed is
// still a builder, and what was placed at the ticket before the claim is not.
func TestRecallReceiptsBuilderIsWhoSpentFromTheFirstClaim(t *testing.T) {
	t.Parallel()
	const before, claim, after, later = "2026-01-01T09:00:00Z", "2026-01-01T10:00:00Z", "2026-01-01T11:00:00Z", "2026-01-01T12:00:00Z"
	base := func(extra ...journal.Line) []journal.Line {
		lines := []journal.Line{
			{TS: "2026-01-01T10:30:00Z", Event: journal.EventClaim, Session: "own"}, // a later claim is not the first
			{TS: claim, Event: journal.EventClaim, Session: "own"},
			{TS: later, Event: journal.EventPublishDone, Change: &journal.ChangeSize{Files: 3, Bytes: 300}},
		}
		return append(lines, extra...)
	}
	cases := []struct {
		name  string
		extra []journal.Line
		want  int64
	}{
		{"a builder with no slice on its line", []journal.Line{spendAt(after, "own", "", "", 60000)}, 60000},
		{"the largest of several builders", []journal.Line{spendAt(after, "own", "", "", 60000), spendAt(after, "s", "a-1", "x", 80000)}, 80000},
		{"a later line never lowers it", []journal.Line{spendAt(after, "s", "a-1", "x", 50000), spendAt(later, "s", "a-1", "", 40000)}, 50000},
		{"a line at the first claim counts", []journal.Line{spendAt(claim, "s", "a-1", "", 7000)}, 7000},
		{"a line only before the claim does not", []journal.Line{spendAt(before, "s", "a-9", "", 900000), spendAt(after, "own", "", "", 1000)}, 1000},
		{"a reviewer named by a round does not", []journal.Line{
			spendAt(after, "s", "a-2", "x", 40000), spendAt(after, "s", "rev", "x", 900000),
			{TS: later, Event: journal.EventGateRound, Session: "s", Agent: "rev"},
		}, 40000},
		{"a reviewer named by a clean round does not", []journal.Line{
			spendAt(after, "s", "a-2", "", 40000), spendAt(after, "s", "rev", "", 900000),
			{TS: later, Event: journal.EventGateClean, Session: "s", Agent: "rev"},
		}, 40000},
		{"a session named as reviewer is a builder", []journal.Line{
			spendAt(after, "s", "", "x", 70000),
			{TS: later, Event: journal.EventGateRound, Session: "s"},
		}, 70000},
		{"a stamp that does not parse is not proof of before", []journal.Line{spendAt("not a time", "s", "a-1", "", 33000)}, 33000},
		{"no spend after the claim is no peak", []journal.Line{spendAt(before, "s", "", "x", 5)}, 0},
	}
	for _, c := range cases {
		f := FactsOf(base(c.extra...))
		if f.Peak != c.want || f.Change == nil || f.Change.Files != 3 || f.At != later {
			t.Errorf("%s: facts = %+v, want peak %d, 3 files, stamped by the publish-done line", c.name, f, c.want)
		}
	}
	if f := FactsOf(base(spendAt(after, "own", "", "", 5))[1:]); f.Peak != 5 {
		t.Errorf("one claim: peak = %d, want 5", f.Peak)
	}
	// A ticket with no claim line was built outside jig: no builder is named.
	if f := FactsOf([]journal.Line{spendAt(after, "s", "", "x", 5)}); f.Peak != 0 {
		t.Errorf("no claim: peak = %d, want 0", f.Peak)
	}
}

// TestRecallReceiptsListTheLastTenNewestFirst: of many shipped tickets the
// block lists the 10 newest, and with fewer the ones it has.
func TestRecallReceiptsListTheLastTenNewestFirst(t *testing.T) {
	t.Parallel()
	var tickets []Ticket
	for i := 1; i <= 12; i++ {
		tickets = append(tickets, Ticket{
			ID: fmt.Sprintf("T-%d", i), State: "done", Shipped: true,
			Facts: &Facts{At: fmt.Sprintf("2026-01-%02dT00:00:00Z", i), Change: &journal.ChangeSize{Files: i, Bytes: int64(i) * 10}, Peak: int64(i) * 1000},
		})
	}
	got := Build(tickets, Material{}, "").Receipts
	if len(got) != MaxReceipts || got[0].Ticket != "T-12" || got[9].Ticket != "T-3" {
		t.Errorf("receipts = %+v, want T-12 down to T-3", got)
	}
	if got[0] != (Receipt{Ticket: "T-12", Files: 12, Bytes: 120, Peak: 12000}) {
		t.Errorf("first row = %+v", got[0])
	}
	if few := Build(tickets[:2], Material{}, "").Receipts; len(few) != 2 || few[0].Ticket != "T-2" {
		t.Errorf("with two shipped = %+v, want both, newest first", few)
	}
}

// TestRecallReceiptsAreLeftOutWhenUnknown: a ticket with no size or no peak is
// absent, not zero, and with none the block is absent while the json holds an
// empty list.
func TestRecallReceiptsAreLeftOutWhenUnknown(t *testing.T) {
	t.Parallel()
	tickets := []Ticket{
		{ID: "T-1", Shipped: true, Facts: &Facts{Peak: 5}},
		{ID: "T-2", Shipped: true, Facts: &Facts{Change: &journal.ChangeSize{Files: 1}}},
		{ID: "T-3", Shipped: true},
		{ID: "T-4", Facts: &Facts{Change: &journal.ChangeSize{Files: 1}, Peak: 5}}, // open
	}
	r := Build(tickets, Material{}, "")
	if r.Receipts == nil || len(r.Receipts) != 0 {
		t.Errorf("receipts = %#v, want an empty list", r.Receipts)
	}
	for _, b := range r.Blocks() {
		if len(b) >= 8 && b[:8] == "receipts" {
			t.Errorf("block %q present with nothing to list", b)
		}
	}
}
