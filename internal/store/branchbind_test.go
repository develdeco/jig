package store

import (
	"errors"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestTicketForBranch: a branch binds to the ticket whose id it leads with -
// jig/JIG-1, JIG-1 and JIG-1-foo all to JIG-1, by id; a branch led by an
// alias binds to the ticket that alias was renamed to, by alias; everything
// else binds to nothing.
func TestTicketForBranch(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for id, rec := range map[string]Ticket{
		"JIG-1": {Title: "First"},
		"JIG-2": {Title: "Renamed from T-5", Aliases: []string{"OLD-5"}},
		"AB1-7": {Title: "Digits in the key"},
	} {
		if err := st.CreateTicketRecord(id, rec); err != nil {
			t.Fatalf("CreateTicketRecord(%s): %v", id, err)
		}
	}

	for _, tc := range []struct {
		branch  string
		wantID  string
		wantHow string
	}{
		{"jig/JIG-1", "JIG-1", "id"},
		{"JIG-1", "JIG-1", "id"},
		{"JIG-1-foo", "JIG-1", "id"},
		{"jig/JIG-1-foo-bar", "JIG-1", "id"},
		{"jig/AB1-7-digits", "AB1-7", "id"},
		{"jig/OLD-5", "JIG-2", "alias"},
		{"OLD-5-still-called-that", "JIG-2", "alias"},
		// A branch that leads with no ticket's id binds to nothing.
		{"main", "", ""},
		{"", "", ""},
		{"jig/", "", ""},
		{"feature/JIG-1", "", ""},
		{"JIG-9", "", ""},     // an id nobody claims
		{"JIG-1x", "", ""},    // not an id followed by a slug
		{"JIG-12", "", ""},    // JIG-12, not JIG-1
		{"JIG-0-foo", "", ""}, // numbers count from 1
		{"jig-1-lowercase", "", ""},
		{"jig/jig/JIG-1", "", ""}, // only one prefix is stripped
	} {
		id, how, err := st.TicketForBranch(tc.branch)
		if err != nil {
			t.Errorf("TicketForBranch(%q): %v", tc.branch, err)
			continue
		}
		if id != tc.wantID || how != tc.wantHow {
			t.Errorf("TicketForBranch(%q) = %q, %q; want %q, %q", tc.branch, id, how, tc.wantID, tc.wantHow)
		}
	}
}

// TestTicketForBranchRefusesAnAmbiguousLead: a lead two tickets claim is the
// alias-collision refusal ResolveTicket gives, not a guess.
func TestTicketForBranchRefusesAnAmbiguousLead(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for id, alias := range map[string]string{"JIG-1": "T-9", "JIG-2": "T-9"} {
		if err := st.CreateTicketRecord(id, Ticket{Title: id, Aliases: []string{alias}}); err != nil {
			t.Fatal(err)
		}
	}
	id, how, err := st.TicketForBranch("jig/T-9-foo")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_ALIAS_COLLISION" {
		t.Fatalf("TicketForBranch = %q, %q, %v; want TICKET_ALIAS_COLLISION", id, how, err)
	}
}

// TestBranchLeadID: the lead alone, without asking the store.
func TestBranchLeadID(t *testing.T) {
	for branch, want := range map[string]string{
		"jig/JIG-1":         "JIG-1",
		"JIG-1":             "JIG-1",
		"JIG-1-foo":         "JIG-1",
		"jig/STORE-123-x-y": "STORE-123",
		"main":              "",
		"JIG-1x":            "",
		"jig/jig/JIG-1":     "",
		"ABCDEFGHIJK-1":     "", // a key is at most ten characters
		"ABCDEFGHIJ-1":      "ABCDEFGHIJ-1",
	} {
		if got := BranchLeadID(branch); got != want {
			t.Errorf("BranchLeadID(%q) = %q, want %q", branch, got, want)
		}
	}
	// Whatever is an id by ParseTicketID leads its own branch.
	for _, id := range []string{"JIG-1", "A-1", "ABCDEFGHIJ-99", "X9-10"} {
		if _, _, ok := ParseTicketID(id); !ok {
			t.Fatalf("test setup: %q is not an id", id)
		}
		if got := BranchLeadID(id); got != id {
			t.Errorf("BranchLeadID(%q) = %q, want the id itself", id, got)
		}
		if got := BranchLeadID(branchPrefix + id + "-slug"); got != id {
			t.Errorf("BranchLeadID(jig/%s-slug) = %q, want %q", id, got, id)
		}
	}
}
