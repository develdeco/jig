package verifydeliver

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/recall"
	"github.com/develdeco/jig/internal/store"
)

// TestLedgerEntryReadsBackThroughRecall pins the contract between the publish
// that writes ledger.md and jig recall, which reads it: the entries and the
// answers of each come back as they went in, answered questions only.
func TestLedgerEntryReadsBackThroughRecall(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	slices := []store.Slice{{ID: "a"}, {ID: "b"}}
	questions := []store.Question{
		{ID: "q-001", Status: "answered", Body: "Formal or casual?", Answer: "Casual."},
		{ID: "q-002", Status: "open", Body: "Never answered?"},
		{ID: "q-003", Status: "answered", Body: "Which locale?", Answer: "English first, then Spanish."},
	}
	if err := writeLedgerEntry(st, "JIG-1", "Greeting tone", slices, questions); err != nil {
		t.Fatal(err)
	}
	if err := writeLedgerEntry(st, "JIG-2", "Rename the flags - all of them", slices[:1], nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(st.Root, "ledger.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := []recall.LedgerEntry{
		{Ticket: "JIG-1", Title: "Greeting tone", Answers: []recall.Answer{
			{ID: "q-001", Text: "Formal or casual? -> Casual."},
			{ID: "q-003", Text: "Which locale? -> English first, then Spanish."},
		}},
		{Ticket: "JIG-2", Title: "Rename the flags - all of them", Answers: []recall.Answer{}},
	}
	if got := recall.ParseLedger(string(data)); !reflect.DeepEqual(got, want) {
		t.Errorf("recall.ParseLedger(ledger.md) = %+v, want %+v\nledger.md:\n%s", got, want, data)
	}
}
