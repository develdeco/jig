package recall

import (
	"reflect"
	"testing"
)

func TestCoveredByNamesShippedWorkTheGoalComesNear(t *testing.T) {
	t.Parallel()
	shipped := []Ticket{
		{ID: "JIG-1", Title: "Fix Clamp so the upper bound holds"},
		{ID: "JIG-2", Title: "Greeting tone"},
	}
	got := CoveredBy("Fix Clamp so TestClamp passes with the upper bound", shipped, nil, "")
	want := []Covered{{Ticket: "JIG-1", Title: "Fix Clamp so the upper bound holds", Score: 0.57, Source: "ticket"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CoveredBy = %+v, want %+v", got, want)
	}
	if got := CoveredBy("Smoke-test the rig environment class", shipped, nil, ""); len(got) != 0 {
		t.Errorf("a goal about something else = %+v, want none", got)
	}
}

func TestCoveredByReadsLedgerHeadingsAndKeepsOnePerTicket(t *testing.T) {
	t.Parallel()
	shipped := []Ticket{{ID: "JIG-1", Title: "Add retry"}}
	ledger := []LedgerEntry{
		{Ticket: "JIG-1", Title: "Add retry to the fetch client"},
		{Ticket: "JIG-2", Title: "Add retry to the fetch client"},
		{Ticket: "JIG-3", Title: "Unrelated"},
	}
	got := CoveredBy("add retry to fetch client", shipped, ledger, "")
	if len(got) != 2 || got[0].Ticket != "JIG-1" || got[0].Source != "ledger" || got[0].Score != 1 || got[1].Ticket != "JIG-2" {
		t.Errorf("CoveredBy = %+v, want JIG-1 once, by its better ledger score, then JIG-2", got)
	}
}

func TestCoveredByLeavesOutTheTicketTheGoalBelongsToAndCapsTheList(t *testing.T) {
	t.Parallel()
	var ledger []LedgerEntry
	for _, id := range []string{"JIG-1", "JIG-2", "JIG-3", "JIG-4", "JIG-5"} {
		ledger = append(ledger, LedgerEntry{Ticket: id, Title: "add retry"})
	}
	got := CoveredBy("add retry", nil, ledger, "JIG-1")
	if len(got) != MaxCovered || got[0].Ticket != "JIG-2" {
		t.Errorf("CoveredBy = %+v, want %d entries without JIG-1", got, MaxCovered)
	}
	if got := CoveredBy("add retry", nil, []LedgerEntry{{Title: "add retry"}}, ""); len(got) != 0 {
		t.Errorf("an entry with no ticket = %+v, want it left out", got)
	}
}
