package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// mustMkdirTicket creates an empty ticket folder for id under st, the
// minimum a ticket.yaml check needs to find the ticket itself.
func mustMkdirTicket(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if err := os.MkdirAll(st.TicketDir(id), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestValidateTicketDepsAbsent covers a ticket without ticket.yaml: it
// validates as before, with no problems reported.
func TestValidateTicketDepsAbsent(t *testing.T) {
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")

	if got := validateTicketDeps(st, "T-1"); got != nil {
		t.Fatalf("validateTicketDeps on an absent ticket.yaml = %v, want nil", got)
	}
}

// TestValidateTicketDepsParseError covers a ticket.yaml that is not valid
// YAML.
func TestValidateTicketDepsParseError(t *testing.T) {
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	path := filepath.Join(st.TicketDir("T-1"), "ticket.yaml")
	if err := os.WriteFile(path, []byte("blocked_by: [this is not valid yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := validateTicketDeps(st, "T-1")
	if len(got) != 1 || !strings.Contains(got[0], "could not be read") {
		t.Fatalf("validateTicketDeps = %v, want a single \"could not be read\" problem", got)
	}
}

// TestValidateTicketDepsMissingTicket covers a blocked_by entry with no
// ticket field.
func TestValidateTicketDepsMissingTicket(t *testing.T) {
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	path := filepath.Join(st.TicketDir("T-1"), "ticket.yaml")
	if err := os.WriteFile(path, []byte("blocked_by:\n  - kind: merged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := validateTicketDeps(st, "T-1")
	if len(got) != 1 || !strings.Contains(got[0], "entry 1 has no ticket") {
		t.Fatalf("validateTicketDeps = %v, want an \"entry 1 has no ticket\" problem", got)
	}
}

// TestValidateTicketDepsUnknownBlocker covers a blocker the store has no
// ticket folder for.
func TestValidateTicketDepsUnknownBlocker(t *testing.T) {
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	if err := st.WriteTicketDeps("T-1", []store.TicketBlockedBy{{Ticket: "T-999", Kind: "merged"}}); err != nil {
		t.Fatal(err)
	}

	got := validateTicketDeps(st, "T-1")
	if len(got) != 1 || !strings.Contains(got[0], "T-999") || !strings.Contains(got[0], "not found") {
		t.Fatalf("validateTicketDeps = %v, want a problem naming the missing T-999 folder", got)
	}
}

// TestValidateTicketDepsBadKind covers a kind other than merged or stacked.
func TestValidateTicketDepsBadKind(t *testing.T) {
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	mustMkdirTicket(t, st, "T-2")
	if err := st.WriteTicketDeps("T-1", []store.TicketBlockedBy{{Ticket: "T-2", Kind: "bogus"}}); err != nil {
		t.Fatal(err)
	}

	got := validateTicketDeps(st, "T-1")
	if len(got) != 1 || !strings.Contains(got[0], `must be "merged" or "stacked"`) {
		t.Fatalf("validateTicketDeps = %v, want a bad-kind problem", got)
	}
}

// TestValidateTicketDepsCycle covers a cycle followed across two tickets'
// own ticket.yaml files, naming both tickets in the cycle.
func TestValidateTicketDepsCycle(t *testing.T) {
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	mustMkdirTicket(t, st, "T-2")
	if err := st.WriteTicketDeps("T-1", []store.TicketBlockedBy{{Ticket: "T-2", Kind: "merged"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTicketDeps("T-2", []store.TicketBlockedBy{{Ticket: "T-1", Kind: "merged"}}); err != nil {
		t.Fatal(err)
	}

	got := validateTicketDeps(st, "T-1")
	var cyc string
	for _, p := range got {
		if strings.Contains(p, "cycle") {
			cyc = p
		}
	}
	if cyc == "" || !strings.Contains(cyc, "T-1") || !strings.Contains(cyc, "T-2") {
		t.Fatalf("validateTicketDeps = %v, want a cycle problem naming T-1 and T-2", got)
	}
}

// TestValidateTicketDepsSelfCycle covers a ticket blocked_by itself.
func TestValidateTicketDepsSelfCycle(t *testing.T) {
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	if err := st.WriteTicketDeps("T-1", []store.TicketBlockedBy{{Ticket: "T-1", Kind: "merged"}}); err != nil {
		t.Fatal(err)
	}

	got := validateTicketDeps(st, "T-1")
	found := false
	for _, p := range got {
		if strings.Contains(p, "cycle") {
			found = true
		}
	}
	if !found {
		t.Fatalf("validateTicketDeps = %v, want a self-cycle problem", got)
	}
}

// TestValidateCommandReportsTicketDepsProblem drives `jig validate` end to
// end against a store with two minted tickets whose ticket.yaml files block
// each other, checking the CLI surfaces the cycle as a validation failure.
func TestValidateCommandReportsTicketDepsProblem(t *testing.T) {
	jig, storeRoot := setupGraduateStore(t)

	if code, out := jig("ticket", "new", "--title", "A"); code != 0 {
		t.Fatalf("jig ticket new A: exit %d\n%s", code, out)
	}
	if code, out := jig("ticket", "new", "--title", "B"); code != 0 {
		t.Fatalf("jig ticket new B: exit %d\n%s", code, out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), "T-") {
			ids = append(ids, e.Name())
		}
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 minted tickets, got %v", ids)
	}
	idA, idB := ids[0], ids[1]

	if err := st.WriteTicketDeps(idA, []store.TicketBlockedBy{{Ticket: idB, Kind: "merged"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTicketDeps(idB, []store.TicketBlockedBy{{Ticket: idA, Kind: "merged"}}); err != nil {
		t.Fatal(err)
	}

	code, out := jig("validate", idA)
	if code == 0 {
		t.Fatalf("jig validate %s: exit 0, want a refusal:\n%s", idA, out)
	}
	if !strings.Contains(out, "cycle") || !strings.Contains(out, idA) || !strings.Contains(out, idB) {
		t.Fatalf("jig validate %s: output missing a cycle problem naming both tickets:\n%s", idA, out)
	}
}
