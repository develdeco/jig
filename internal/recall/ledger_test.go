package recall

import (
	"reflect"
	"testing"
)

const sampleLedger = "# Ledger\n" +
	"\n## JIG-1 - Fix Clamp\n\nDelivered 2 slice(s).\n\n**Answers:**\nnone\n" +
	"\n## JIG-2 - Greeting tone\n\nDelivered 1 slice(s).\n\n**Answers:**\n" +
	"- q-001: Formal or casual? -> Casual.\n" +
	"- q-002: Which locale?\n  Only English -> English first.\n" +
	"\n## JIG-3\n\nDelivered 1 slice(s).\n\n**Answers:**\n- q-001: Keep the flag? -> Yes - and rename it.\n"

func TestParseLedger(t *testing.T) {
	t.Parallel()
	got := ParseLedger(sampleLedger)
	want := []LedgerEntry{
		{Ticket: "JIG-1", Title: "Fix Clamp", Answers: []Answer{}},
		{Ticket: "JIG-2", Title: "Greeting tone", Answers: []Answer{
			{ID: "q-001", Text: "Formal or casual? -> Casual."},
			{ID: "q-002", Text: "Which locale? Only English -> English first."},
		}},
		{Ticket: "JIG-3", Answers: []Answer{{ID: "q-001", Text: "Keep the flag? -> Yes - and rename it."}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseLedger =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseLedgerOfNothingAndOfCRLF(t *testing.T) {
	t.Parallel()
	if got := ParseLedger(""); got != nil {
		t.Errorf("ParseLedger of an empty ledger = %+v, want none", got)
	}
	if got := ParseLedger("# Ledger\n\nnotes before any entry\n"); got != nil {
		t.Errorf("ParseLedger of a ledger with no entry = %+v, want none", got)
	}
	crlf := "## JIG-9 - Tone\r\n\r\n**Answers:**\r\n- q-001: A? -> B.\r\n"
	want := []LedgerEntry{{Ticket: "JIG-9", Title: "Tone", Answers: []Answer{{ID: "q-001", Text: "A? -> B."}}}}
	if got := ParseLedger(crlf); !reflect.DeepEqual(got, want) {
		t.Errorf("ParseLedger of CRLF text = %+v, want %+v", got, want)
	}
}
