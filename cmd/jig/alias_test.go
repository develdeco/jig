package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestStatusResolvesAnAliasToItsCurrentID is the critical path "jig run
// <alias> works on the ticket and names its current id", exercised at jig
// status (the cheapest command needing only a ticket folder to run): an
// alias recorded in some ticket's ticket.yaml resolves to that ticket's
// current id, printed on its own line before the rest of the report, which
// then names the current id throughout.
func TestStatusResolvesAnAliasToItsCurrentID(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("T-30", store.Ticket{Title: "Renamed", Aliases: []string{"T-1"}}); err != nil {
		t.Fatal(err)
	}

	code, out := jig("status", "T-1")
	if code != 0 {
		t.Fatalf("jig status T-1: exit %d\n%s", code, out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) == 0 || lines[0] != "T-1 is now T-30" {
		t.Fatalf("output's first line = %q, want \"T-1 is now T-30\"\nfull output:\n%s", lines[0], out)
	}
	if strings.Contains(out, "ticket: T-1") {
		t.Fatalf("output still names the alias as the working ticket:\n%s", out)
	}
}

// TestStatusOnAPlainIDPrintsNoResolutionLine covers the quiet case: a
// command given a ticket's own current id (no alias involved) prints
// nothing about resolution at all.
func TestStatusOnAPlainIDPrintsNoResolutionLine(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("T-1", store.Ticket{Title: "Plain"}); err != nil {
		t.Fatal(err)
	}

	code, out := jig("status", "T-1")
	if code != 0 {
		t.Fatalf("jig status T-1: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "is now") {
		t.Fatalf("output names a resolution that never happened:\n%s", out)
	}
}

// TestValidateRefusesACollidingAlias covers jig validate's own refusal of an
// alias claimed by two tickets, naming both.
func TestValidateRefusesACollidingAlias(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)
	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("T-1", store.Ticket{Title: "One", Aliases: []string{"T-9"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord("T-2", store.Ticket{Title: "Two", Aliases: []string{"T-9"}}); err != nil {
		t.Fatal(err)
	}
	writeBrief(t, st, "T-1")

	code, out := jig("validate", "T-1")
	if code == 0 {
		t.Fatalf("jig validate T-1: exit 0, want non-zero on a colliding alias\n%s", out)
	}
	if !strings.Contains(out, "T-1") || !strings.Contains(out, "T-2") {
		t.Fatalf("output does not name both colliding tickets:\n%s", out)
	}
}

// writeBrief writes the minimum brief.md validateTicket needs to get past
// its own check, so the alias collision is the only problem a test cares
// about surfaces.
func writeBrief(t *testing.T, st *store.Store, ticket string) {
	t.Helper()
	dir := st.TicketDir(ticket)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte("## Context\n\nSomething.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
