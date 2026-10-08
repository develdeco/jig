package store

import (
	"strings"
	"testing"
)

// TestResolveTicketPassesThroughAPlainID covers the base case: an id with no
// alias anywhere in the store resolves to itself.
func TestResolveTicketPassesThroughAPlainID(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("JIG-1", Ticket{Title: "First"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}

	got, err := st.ResolveTicket("JIG-1")
	if err != nil {
		t.Fatalf("ResolveTicket: %v", err)
	}
	if got != "JIG-1" {
		t.Fatalf("ResolveTicket(JIG-1) = %q, want JIG-1", got)
	}
}

// TestResolveTicketPassesThroughUnknownID covers an id or alias nothing in
// the store claims: ResolveTicket returns it unchanged, leaving a caller's
// own "not found" check (requireTicket) to fire on it.
func TestResolveTicketPassesThroughUnknownID(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	got, err := st.ResolveTicket("JIG-404")
	if err != nil {
		t.Fatalf("ResolveTicket: %v", err)
	}
	if got != "JIG-404" {
		t.Fatalf("ResolveTicket(JIG-404) = %q, want JIG-404", got)
	}
}

// TestResolveTicketFollowsAnAlias is the critical path "jig run <alias>
// works on the ticket and names its current id", at the resolver's own
// seam: an alias on a ticket's ticket.yaml resolves to that ticket's id.
func TestResolveTicketFollowsAnAlias(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("STORE-3", Ticket{Title: "Keyed", Aliases: []string{"T-5"}}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}

	got, err := st.ResolveTicket("T-5")
	if err != nil {
		t.Fatalf("ResolveTicket: %v", err)
	}
	if got != "STORE-3" {
		t.Fatalf("ResolveTicket(T-5) = %q, want STORE-3", got)
	}
}

// TestResolveTicketRefusesTwoTicketsClaimingOneAlias covers one half of the
// collision rule: an alias listed on two different tickets' ticket.yaml is
// refused, naming both.
func TestResolveTicketRefusesTwoTicketsClaimingOneAlias(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("STORE-1", Ticket{Title: "One", Aliases: []string{"T-9"}}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	if err := st.CreateTicketRecord("STORE-2", Ticket{Title: "Two", Aliases: []string{"T-9"}}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}

	_, err := st.ResolveTicket("T-9")
	if err == nil {
		t.Fatal("ResolveTicket: want an error, got nil")
	}
	for _, want := range []string{"STORE-1", "STORE-2"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err.Error(), want)
		}
	}
}

// TestResolveTicketRefusesAnAliasEqualToAnotherTicketsID covers the other
// half of the collision rule: an alias that is also another ticket's own id
// is refused, naming both tickets, rather than silently preferring one.
func TestResolveTicketRefusesAnAliasEqualToAnotherTicketsID(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("STORE-1", Ticket{Title: "One", Aliases: []string{"STORE-2"}}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	if err := st.CreateTicketRecord("STORE-2", Ticket{Title: "Two"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}

	_, err := st.ResolveTicket("STORE-2")
	if err == nil {
		t.Fatal("ResolveTicket: want an error, got nil")
	}
	for _, want := range []string{"STORE-1", "STORE-2"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err.Error(), want)
		}
	}
}

// TestCheckAliasesReportsACollisionNeitherSideIsAskedAbout covers
// CheckAliases finding a collision across the whole store, regardless of
// which ticket a caller happens to be looking at.
func TestCheckAliasesReportsACollisionNeitherSideIsAskedAbout(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("STORE-1", Ticket{Title: "One", Aliases: []string{"T-9"}}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	if err := st.CreateTicketRecord("STORE-2", Ticket{Title: "Two", Aliases: []string{"T-9"}}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}

	problems, err := st.CheckAliases()
	if err != nil {
		t.Fatalf("CheckAliases: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("CheckAliases = %v, want exactly one problem", problems)
	}
}

// TestCheckAliasesFindsNothingWrongInAnOrdinaryStore covers the quiet case:
// a store with no aliases, or aliases that collide with nothing, reports no
// problems.
func TestCheckAliasesFindsNothingWrongInAnOrdinaryStore(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("STORE-1", Ticket{Title: "One", Aliases: []string{"T-9"}}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}

	problems, err := st.CheckAliases()
	if err != nil {
		t.Fatalf("CheckAliases: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("CheckAliases = %v, want none", problems)
	}
}
