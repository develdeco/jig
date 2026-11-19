package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/recall"
)

// TestRecallReceiptsReachTheBlockAndTheJSON: a shipped ticket whose journal
// holds a change size and a builder's peak is a row of the receipts, in the
// rendered block and in the json; one that lacks either, and an open ticket,
// are not.
func TestRecallReceiptsReachTheBlockAndTheJSON(t *testing.T) {
	t.Parallel()
	st := planStore(t)
	add := func(id string, l journal.Line) {
		t.Helper()
		if err := journal.Append(st, id, l); err != nil {
			t.Fatal(err)
		}
	}
	shipped := func(id, at string, size *journal.ChangeSize) {
		putTicket(t, st, id, "Ticket "+id, false)
		add(id, journal.Line{TS: "2026-01-01T00:00:00Z", Event: journal.EventClaim, Session: "s", Slice: "a"})
		add(id, journal.Line{TS: at, Event: journal.EventPublishDone, Change: size})
	}
	built := func(id, agent string, peak int64) {
		add(id, journal.Line{TS: "2026-01-01T00:00:01Z", Event: journal.EventSpend, Session: "s", Agent: agent, Slice: "a", Spend: &journal.Spend{Input: 1, Peak: peak}})
	}
	shipped("T-1", "2026-01-01T10:00:00Z", &journal.ChangeSize{Files: 4, Bytes: 4096})
	built("T-1", "small", 90000)
	built("T-1", "big", 150000)
	shipped("T-2", "2026-01-02T10:00:00Z", &journal.ChangeSize{Files: 1, Bytes: 0})
	built("T-2", "x", 70000)
	shipped("T-3", "2026-01-03T10:00:00Z", nil) // no size recorded
	built("T-3", "x", 70000)
	shipped("T-4", "2026-01-04T10:00:00Z", &journal.ChangeSize{Files: 2, Bytes: 10}) // no peak recorded
	putTicket(t, st, "T-5", "Open", false)

	tickets, err := recallTickets(st)
	if err != nil {
		t.Fatal(err)
	}
	report := recall.Build(tickets, recall.Material{}, "")
	want := []recall.Receipt{{Ticket: "T-2", Files: 1, Bytes: 0, Peak: 70000}, {Ticket: "T-1", Files: 4, Bytes: 4096, Peak: 150000}}
	if len(report.Receipts) != len(want) || report.Receipts[0] != want[0] || report.Receipts[1] != want[1] {
		t.Fatalf("receipts = %+v, want %+v", report.Receipts, want)
	}

	var table string
	for _, b := range report.Blocks() {
		if strings.HasPrefix(b, "receipts") {
			table = b
		}
	}
	for _, row := range []string{"T-2,1,0,70000", "T-1,4,4096,150000"} {
		if !strings.Contains(table, row) {
			t.Errorf("block = %q, want the row %q", table, row)
		}
	}
	if strings.Contains(table, "T-3") || strings.Contains(table, "T-4") || strings.Contains(table, "T-5") {
		t.Errorf("block = %q, want no ticket without a size and a peak, and no open one", table)
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var back struct{ Receipts []recall.Receipt }
	if err := json.Unmarshal(data, &back); err != nil || len(back.Receipts) != 2 || back.Receipts[0] != want[0] {
		t.Errorf("json receipts = %+v, %v, want %+v", back.Receipts, err, want)
	}
}
