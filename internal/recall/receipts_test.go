package recall

import (
	"fmt"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

func spendOn(session, agent, slice string, peak int64) journal.Line {
	return journal.Line{Event: journal.EventSpend, Session: session, Agent: agent, Slice: slice, Spend: &journal.Spend{Input: 1, Peak: peak}}
}

// TestRecallReceiptsBuilderIsTheLargestPeakOfTheAgentsOnSlices: the builder's
// peak is the largest among the sessions and agents whose lines were written
// on a slice, each kept at the largest of its lines; one with no slice, or no
// peak, adds nothing.
func TestRecallReceiptsBuilderIsTheLargestPeakOfTheAgentsOnSlices(t *testing.T) {
	t.Parallel()
	lines := []journal.Line{
		{TS: "2026-01-05T00:00:00Z", Event: journal.EventPublishDone, Change: &journal.ChangeSize{Files: 3, Bytes: 300}},
		spendOn("s", "a-1", "x", 50000),
		spendOn("s", "a-1", "x", 40000), // a later line never lowers it
		spendOn("s", "a-2", "y", 80000),
		spendOn("s", "a-3", "", 900000), // not on a slice
		spendOn("s", "a-4", "y", 0),     // an older line with no peak
	}
	f := FactsOf(lines)
	if f.Peak != 80000 || f.Change == nil || f.Change.Files != 3 || f.At != "2026-01-05T00:00:00Z" {
		t.Errorf("facts = %+v, want peak 80000, 3 files, stamped by the publish-done line", f)
	}
	if f := FactsOf(lines[1:3]); f.Peak != 50000 || f.Change != nil {
		t.Errorf("facts without a publish-done = %+v, want the peak 50000 and no change", f)
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
