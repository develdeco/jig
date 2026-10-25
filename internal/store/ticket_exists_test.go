package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTicketExistsNeedsAFileOfItsOwn: a ticket folder holding only lock
// sidecars and a temporary file (what a git checkout leaves of a ticket it
// removed) is no ticket to work, while one file of its own (a brief written by
// hand, a record) makes it one; the id scan still counts the empty folder, so
// minting never reuses its number.
func TestTicketExistsNeedsAFileOfItsOwn(t *testing.T) {
	t.Parallel()
	st := &Store{Root: t.TempDir()}
	if st.TicketExists("SHOP-1") {
		t.Fatal("a ticket with no folder exists")
	}
	dir := st.TicketDir("SHOP-1")
	for _, name := range []string{"ticket.yaml.lock", "journal.ndjson.lock", ".brief.md.123.ab.tmp", filepath.Join("slices", "a.state.lock")} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if st.TicketExists("SHOP-1") {
		t.Error("a folder of locks and a temporary file exists as a ticket")
	}
	if id, err := st.Mint("SHOP", Ticket{Title: "Next"}); err != nil || id != "SHOP-2" {
		t.Errorf("Mint = %q, %v; want SHOP-2: the folder's number stays taken", id, err)
	}
	if ids, err := st.TicketIDs(); err != nil || len(ids) != 2 {
		t.Errorf("TicketIDs = %v, %v; want both folders, which the id scan counts", ids, err)
	}
	if ids, err := st.Tickets(); err != nil || len(ids) != 1 || ids[0] != "SHOP-2" {
		t.Errorf("Tickets = %v, %v; want SHOP-2 alone", ids, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte("# SHOP-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !st.TicketExists("SHOP-1") {
		t.Error("a folder with a brief is not a ticket")
	}
}
