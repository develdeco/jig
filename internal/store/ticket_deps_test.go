package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTicketDepsRoundTrip(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("T-1"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := st.ReadTicketDeps("T-1")
	if err != nil {
		t.Fatalf("ReadTicketDeps on an absent file: %v", err)
	}
	if got != nil {
		t.Fatalf("ReadTicketDeps on an absent file = %+v, want nil", got)
	}

	want := []TicketBlockedBy{
		{Ticket: "T-4", Kind: "merged"},
		{Ticket: "T-9", Kind: "stacked"},
	}
	if err := st.WriteTicketDeps("T-1", want); err != nil {
		t.Fatalf("WriteTicketDeps: %v", err)
	}

	got, err = st.ReadTicketDeps("T-1")
	if err != nil {
		t.Fatalf("ReadTicketDeps: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadTicketDeps = %+v, want %+v", got, want)
	}

	if _, err := os.Stat(filepath.Join(st.TicketDir("T-1"), "ticket.yaml")); err != nil {
		t.Fatalf("ticket.yaml not written: %v", err)
	}
}
