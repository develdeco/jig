package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMintFirstID covers the base case: an empty store root mints key's
// first id and writes the ticket's folder and ticket.yaml, nothing else.
func TestMintFirstID(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	id, err := st.Mint("JIG", Ticket{Title: "First"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if id != "JIG-1" {
		t.Fatalf("id = %q, want JIG-1", id)
	}
	got, err := st.ReadTicket(id)
	if err != nil {
		t.Fatalf("ReadTicket: %v", err)
	}
	if got.Title != "First" {
		t.Fatalf("ticket.yaml title = %q, want %q", got.Title, "First")
	}
	if _, err := os.Stat(filepath.Join(st.TicketDir(id), "tracker")); !os.IsNotExist(err) {
		t.Fatalf("Mint wrote a tracker/ folder (stat err %v), want nothing written there", err)
	}
}

// TestMintCountsTheHighestExistingFolder covers the counter itself: the
// next id under a key is one past the highest number among the store-root
// folders that carry it, not simply "one more than the last Mint call" - an
// existing folder from outside this run (a gap, or one left by a build
// script) is still counted.
func TestMintCountsTheHighestExistingFolder(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("JIG-5"), 0o755); err != nil {
		t.Fatal(err)
	}

	id, err := st.Mint("JIG", Ticket{Title: "Next"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if id != "JIG-6" {
		t.Fatalf("id = %q, want JIG-6", id)
	}
}

// TestMintKeysCountSeparately covers two keys sharing one store root: each
// key's next number is worked out among its own ids only, so one key's
// count never shifts another's.
func TestMintKeysCountSeparately(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("JIG-5"), 0o755); err != nil {
		t.Fatal(err)
	}

	id, err := st.Mint("GRAPH", Ticket{Title: "First under GRAPH"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if id != "GRAPH-1" {
		t.Fatalf("id = %q, want GRAPH-1", id)
	}
}

// TestMintSkipsNonDirectoryEntries covers a plain file whose name matches
// the id shape sitting where the next id's folder would go: it must not
// count toward the next id (an empty root still mints "JIG-1"), so Mint
// keeps recomputing that same obstructed id - and failing on it - rather
// than skipping past it to "JIG-2".
func TestMintSkipsNonDirectoryEntries(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(st.Root, "tickets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.TicketDir("JIG-1"), []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}

	id, err := st.Mint("JIG", Ticket{Title: "Next"})
	if err == nil {
		t.Fatalf("Mint over a file blocking JIG-1: want an error (JIG-1 is not a directory), got id %q", id)
	}
}

// TestTicketIDsSortsByKeyThenNumber checks that TicketIDs returns every
// id-shaped store-root folder, under any key, sorted by key and then by
// number ascending - not the directory listing's own alphabetic order, which
// would put "JIG-10" ahead of "JIG-2" and interleave keys.
func TestTicketIDsSortsByKeyThenNumber(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for _, id := range []string{"JIG-10", "JIG-2", "JIG-1", "GRAPH-3", "GRAPH-1"} {
		if err := os.MkdirAll(st.TicketDir(id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A plain file matching the id shape must not be counted.
	if err := os.WriteFile(filepath.Join(st.Root, "tickets", "JIG-99"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ids, err := st.TicketIDs()
	if err != nil {
		t.Fatalf("TicketIDs: %v", err)
	}
	want := []string{"GRAPH-1", "GRAPH-3", "JIG-1", "JIG-2", "JIG-10"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("TicketIDs = %v, want %v", ids, want)
	}
}

// TestTicketIDsListsUndeclaredKeysToo covers a folder under a key the
// project no longer declares (or never did): TicketIDs lists every
// id-shaped folder regardless, since a key removed from project.yaml only
// stops new mints under it.
func TestTicketIDsListsUndeclaredKeysToo(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("OLD-1"), 0o755); err != nil {
		t.Fatal(err)
	}

	ids, err := st.TicketIDs()
	if err != nil {
		t.Fatalf("TicketIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "OLD-1" {
		t.Fatalf("TicketIDs = %v, want [OLD-1]", ids)
	}
}

// TestMintCountsAliasesToo covers the critical path "a key whose highest
// number belongs to an alias mints one past it": an alias on a ticket whose
// own id is lower than the alias's number still raises the next id minted
// under that alias's key, since the number was already minted once under it.
func TestMintCountsAliasesToo(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("JIG-1", Ticket{Title: "Renamed", Aliases: []string{"JIG-9"}}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}

	id, err := st.Mint("JIG", Ticket{Title: "Next"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if id != "JIG-10" {
		t.Fatalf("id = %q, want JIG-10 (one past the alias JIG-9)", id)
	}
}

// TestMintToleratesAnUnreadableTicketRecord covers a ticket.yaml that
// cannot be decoded (a hand-broken file) sitting under the same key: Mint
// must still compute a next id from the folder names alone, since the
// broken record is that ticket's own problem (jig validate reports it), not
// a reason to refuse every other mint under its key.
func TestMintToleratesAnUnreadableTicketRecord(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("JIG-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.TicketFilePath("JIG-1"), []byte("blocked_by: ["), 0o644); err != nil {
		t.Fatal(err)
	}

	id, err := st.Mint("JIG", Ticket{Title: "Next"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if id != "JIG-2" {
		t.Fatalf("id = %q, want JIG-2", id)
	}
}
