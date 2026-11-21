package store

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestTicketKindDefaultsToTaskAndIsWrittenOnlyWhenNot: an absent kind reads as
// a task and the file writes none for a task; any other kind and a parent are
// written and read back.
func TestTicketKindDefaultsToTaskAndIsWrittenOnlyWhenNot(t *testing.T) {
	t.Parallel()
	st := &Store{Root: t.TempDir()}
	for id, rec := range map[string]Ticket{
		"T-1": {Title: "plain"},
		"T-2": {Title: "explicit", Kind: KindTask},
		"T-3": {Title: "multi", Kind: KindMultiTask},
		"T-4": {Title: "under", Parent: "T-3"},
		"T-5": {Title: "issue", Kind: KindIssue},
	} {
		if err := st.CreateTicketRecord(id, rec); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]string{"T-1": KindTask, "T-2": KindTask, "T-3": KindMultiTask, "T-4": KindTask, "T-5": KindIssue} {
		got, err := st.ReadTicket(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.KindOrDefault() != want {
			t.Errorf("%s kind = %q, want %q", id, got.KindOrDefault(), want)
		}
		raw, _ := os.ReadFile(st.TicketFilePath(id))
		hasKey := strings.Contains(string(raw), "kind:")
		if hasKey != (want != KindTask) {
			t.Errorf("%s file has kind key = %v, want %v:\n%s", id, hasKey, want != KindTask, raw)
		}
	}
	got, _ := st.ReadTicket("T-4")
	if got.Parent != "T-3" {
		t.Errorf("parent = %q, want T-3", got.Parent)
	}
	raw, _ := os.ReadFile(st.TicketFilePath("T-1"))
	if strings.Contains(string(raw), "parent:") {
		t.Errorf("a ticket with no parent wrote one:\n%s", raw)
	}
}

// TestTicketKindOutsideTheThreeFailsTheRead: a kind that is none of issue,
// task and multi-task is TICKET_RECORD_INVALID.
func TestTicketKindOutsideTheThreeFailsTheRead(t *testing.T) {
	t.Parallel()
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("T-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.TicketFilePath("T-1"), []byte("schema_version: 1\nkind: epic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := st.ReadTicket("T-1")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_RECORD_INVALID" || !strings.Contains(ae.Msg, `"epic"`) {
		t.Fatalf("ReadTicket with kind epic = %v, want TICKET_RECORD_INVALID naming it", err)
	}
}

// TestTicketKindSurvivesARewrite: a rewrite of the record (a branch write)
// keeps kind and parent.
func TestTicketKindSurvivesARewrite(t *testing.T) {
	t.Parallel()
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("T-1", Ticket{Kind: KindMultiTask, Parent: "T-9"}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTicketBranch("T-1", "feature"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.ReadTicket("T-1")
	if got.Kind != KindMultiTask || got.Parent != "T-9" {
		t.Fatalf("after a rewrite kind = %q parent = %q", got.Kind, got.Parent)
	}
}
