package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestMintFirstID covers the base case: an empty store root mints the
// format's first id and writes the ticket's folder and ticket.yaml, nothing
// else.
func TestMintFirstID(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	id, err := st.Mint("JIG-{n}", Ticket{Title: "First"})
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
// next id is one past the highest number among the store-root folders the
// format matches, not simply "one more than the last Mint call" - an
// existing folder from outside this run (a gap, or one left by a build
// script) is still counted.
func TestMintCountsTheHighestExistingFolder(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("JIG-5"), 0o755); err != nil {
		t.Fatal(err)
	}

	id, err := st.Mint("JIG-{n}", Ticket{Title: "Next"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if id != "JIG-6" {
		t.Fatalf("id = %q, want JIG-6", id)
	}
}

// TestMintSkipsNonDirectoryEntries covers a plain file whose name matches
// the format sitting where the next id's folder would go: it must not count
// toward the next id (an empty root still mints "JIG-1"), so Mint keeps
// recomputing that same obstructed id - and failing on it - rather than
// skipping past it to "JIG-2".
func TestMintSkipsNonDirectoryEntries(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.WriteFile(st.TicketDir("JIG-1"), []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}

	id, err := st.Mint("JIG-{n}", Ticket{Title: "Next"})
	if err == nil {
		t.Fatalf("Mint over a file blocking JIG-1: want an error (JIG-1 is not a directory), got id %q", id)
	}
}

// TestMintRefusesAnUnusableID covers pool.CheckTicket's refusal, reached
// before anything is written: a ticket_format that mints a reserved lease
// suffix is refused, and the help points at ticket_format, not at any
// tracker.
func TestMintRefusesAnUnusableID(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	id, err := st.Mint("JIG-{n}-gate", Ticket{Title: "x"})
	if id != "" {
		t.Fatalf("id = %q, want empty on a refusal", id)
	}
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("Mint with ticket_format JIG-{n}-gate: err = %v, want an *axi.Error", err)
	}
	if ae.Code != "VALIDATION_ERROR" {
		t.Fatalf("Code = %q, want VALIDATION_ERROR", ae.Code)
	}
	if !strings.Contains(ae.Msg, "JIG-1-gate") || !strings.Contains(ae.Msg, "reserves") {
		t.Fatalf("Msg = %q, want it to name JIG-1-gate and the reserved-suffix reason", ae.Msg)
	}
	if len(ae.Help) != 1 || !strings.Contains(ae.Help[0], "ticket_format") {
		t.Fatalf("Help = %+v, want one line pointing at ticket_format", ae.Help)
	}
	if _, err := os.Stat(st.TicketDir("JIG-1-gate")); !os.IsNotExist(err) {
		t.Fatalf("the refused ticket left a store folder behind (stat err %v)", err)
	}
}

// TestMintRefusesAPathLikeID covers the other half of pool.CheckTicket's
// refusal: a ticket_format whose static text is not a single directory
// name is refused the same way, writing nothing outside the store either.
func TestMintRefusesAPathLikeID(t *testing.T) {
	st := &Store{Root: t.TempDir()}

	id, err := st.Mint("../escape-{n}", Ticket{Title: "x"})
	if id != "" {
		t.Fatalf("id = %q, want empty on a refusal", id)
	}
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("Mint with a path-like ticket_format: err = %v, want an *axi.Error", err)
	}
	if !strings.Contains(ae.Msg, "path separator") {
		t.Fatalf("Msg = %q, want it to name the path-separator reason", ae.Msg)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(st.Root), "escape-1")); !os.IsNotExist(err) {
		t.Fatalf("the refused ticket left a file outside the store (stat err %v)", err)
	}
}

// TestTicketIDsSortsByNumberAscending checks that TicketIDs returns the
// store-root folders format mints, in ascending numeric order - not the
// directory listing's own alphabetic order, which would put "JIG-10" ahead
// of "JIG-2".
func TestTicketIDsSortsByNumberAscending(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	for _, id := range []string{"JIG-10", "JIG-2", "JIG-1"} {
		if err := os.MkdirAll(st.TicketDir(id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A plain file matching the format must not be counted.
	if err := os.WriteFile(filepath.Join(st.Root, "JIG-99"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ids, err := st.TicketIDs("JIG-{n}")
	if err != nil {
		t.Fatalf("TicketIDs: %v", err)
	}
	want := []string{"JIG-1", "JIG-2", "JIG-10"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("TicketIDs = %v, want %v", ids, want)
	}
}
