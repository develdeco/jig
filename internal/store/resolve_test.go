package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestResolveTicketConcurrentUseIsRaceFree exercises the memoized alias scan
// from many goroutines sharing one Store at once, as a frontier run's
// per-slice fan-out does (internal/frontier): run under -race, an unlocked
// read/write of aliasClaimsCache here would be flagged.
func TestResolveTicketConcurrentUseIsRaceFree(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("JIG-%d", i)
		if err := st.CreateTicketRecord(id, Ticket{Title: id}); err != nil {
			t.Fatalf("CreateTicketRecord: %v", err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("JIG-%d", n%5+1)
			if _, err := st.ResolveTicket(id); err != nil {
				t.Errorf("ResolveTicket: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

// TestSyncPullDropsAliasCache covers the critical path "an alias that arrives
// by a pull resolves at once": a memoized scan taken before the pull (ticket
// JIG-1's alias not seen yet) must not shadow one the pull itself just
// brought in.
func TestSyncPullDropsAliasCache(t *testing.T) {
	st, _, remote := newTestRemoteStore(t)
	if err := st.CreateTicketRecord("JIG-1", Ticket{Title: "First"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	if err := st.Push("add JIG-1"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// Prime the memoized scan before anything claims OLD-9.
	if got, err := st.ResolveTicket("OLD-9"); err != nil || got != "OLD-9" {
		t.Fatalf("ResolveTicket(OLD-9) = (%q, %v), want (OLD-9, nil)", got, err)
	}

	// Simulate another clone recording the alias and pushing it directly to
	// the remote, never through this Store.
	otherClone := filepath.Join(t.TempDir(), "other")
	runGit(t, "", "clone", remote, otherClone)
	runGit(t, otherClone, "config", "user.name", "tester")
	runGit(t, otherClone, "config", "user.email", "tester@example.invalid")
	ticketYAML := filepath.Join(otherClone, "tickets", "JIG-1", "ticket.yaml")
	if err := os.WriteFile(ticketYAML, []byte("schema_version: 1\ntitle: First\naliases:\n  - OLD-9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, otherClone, "add", "-A")
	runGit(t, otherClone, "commit", "-m", "alias")
	runGit(t, otherClone, "push", "origin", "main")

	if err := st.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	got, err := st.ResolveTicket("OLD-9")
	if err != nil {
		t.Fatalf("ResolveTicket: %v", err)
	}
	if got != "JIG-1" {
		t.Fatalf("ResolveTicket(OLD-9) after Sync's pull = %q, want JIG-1 (the pre-pull memoized scan must be dropped)", got)
	}
}
