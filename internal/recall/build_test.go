package recall

import (
	"encoding/json"
	"reflect"
	"testing"
)

func sampleTickets() []Ticket {
	return []Ticket{
		{ID: "JIG-1", Title: "Fix Clamp", State: "done", Shipped: true},
		{ID: "JIG-2", Title: "Add retry with backoff to the fetch client", Body: "The client gives up on the first error.", State: "ready"},
		{ID: "JIG-3", Title: "Redesign the settings page", Body: "Group the options by area.", State: "new",
			BlockedBy: []Blocker{{Ticket: "JIG-1", Kind: "merged"}, {Ticket: "JIG-2", Kind: "stacked"}, {Ticket: "JIG-99", Kind: "merged"}}},
		{ID: "JIG-4", Title: "Retry fetch client with backoff", State: "active"},
	}
}

func TestBuildListsOpenTicketsAndTheirEdges(t *testing.T) {
	t.Parallel()
	r := Build(sampleTickets(), Material{}, "")
	var ids []string
	for _, o := range r.Tickets {
		ids = append(ids, o.ID+":"+o.State)
	}
	if want := []string{"JIG-2:ready", "JIG-3:new", "JIG-4:active"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("open tickets = %q, want %q", ids, want)
	}
	if r.Shipped != 1 {
		t.Errorf("shipped = %d, want 1", r.Shipped)
	}
	want := []Edge{
		{Ticket: "JIG-3", BlockedBy: "JIG-1", Kind: "merged", BlockerShipped: true},
		{Ticket: "JIG-3", BlockedBy: "JIG-2", Kind: "stacked"},
		{Ticket: "JIG-3", BlockedBy: "JIG-99", Kind: "merged", Dangling: true},
	}
	if !reflect.DeepEqual(r.Edges, want) {
		t.Errorf("edges = %+v, want %+v", r.Edges, want)
	}
}

func TestDuplicatesAreRankedBestFirst(t *testing.T) {
	t.Parallel()
	got := Duplicates("retry fetch client backoff", sampleTickets()[1:])
	if len(got) != 2 || got[0].Ticket != "JIG-4" || got[1].Ticket != "JIG-2" {
		t.Fatalf("duplicates = %+v, want JIG-4 (all four words) before JIG-2 (four of five)", got)
	}
	if got[0].Score != 1 || got[1].Score != 0.8 || got[0].In != "title" {
		t.Errorf("scores = %v and %v in %q, want 1 and 0.8 in the title", got[0].Score, got[1].Score, got[0].In)
	}
}

func TestDuplicatesAtAndBelowTheThreshold(t *testing.T) {
	t.Parallel()
	tk := []Ticket{{ID: "T-1", Title: "retry fetch backoff client"}}
	if got := Duplicates("retry fetch", tk); len(got) != 1 || got[0].Score != 0.5 {
		t.Errorf("a score of exactly 0.5 = %+v, want it named", got)
	}
	if got := Duplicates("retry", tk); len(got) != 0 {
		t.Errorf("a score of 0.25 = %+v, want nothing", got)
	}
	if got := Duplicates("", tk); len(got) != 0 || got == nil {
		t.Errorf("an empty query = %#v, want an empty list", got)
	}
}

func TestDuplicatesReadTheBodyToo(t *testing.T) {
	t.Parallel()
	tk := []Ticket{{ID: "T-1", Title: "Settings", Body: "Group the options by area."}}
	got := Duplicates("group options area", tk)
	if len(got) != 1 || got[0].In != "body" || got[0].Score != 1 {
		t.Errorf("duplicates = %+v, want T-1 by its body", got)
	}
}

func TestDuplicatesOfEqualScoreKeepTheStoreOrder(t *testing.T) {
	t.Parallel()
	tk := []Ticket{{ID: "T-3", Title: "retry fetch"}, {ID: "T-1", Title: "retry fetch"}, {ID: "T-2", Title: "fetch retry"}}
	var ids []string
	for _, d := range Duplicates("retry fetch", tk) {
		ids = append(ids, d.Ticket)
	}
	if want := []string{"T-3", "T-1", "T-2"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("order = %q, want %q", ids, want)
	}
}

func TestReportMarshalsEmptyListsAsArrays(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(Build(nil, Material{}, ""))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"tickets":[],"shipped":0,"edges":[],"duplicates":[],"fog":[],"platform":[],"answers":[]}`
	if string(data) != want {
		t.Errorf("an empty report = %s, want %s", data, want)
	}
}
