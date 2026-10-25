package store

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
)

// TestCloseTicketRecordsTheClosureAndKeepsTheRest: closing a ticket writes the
// closed entry and leaves its title, blockers, branch and aliases as they
// were; reopening removes the entry and leaves them the same way. The file is
// the contract: a top-level closed key holding the reason, by, note and at,
// with schema_version still 1, so a jig that predates the key refuses the
// record instead of building a closed ticket by accident.
func TestCloseTicketRecordsTheClosureAndKeepsTheRest(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	rec := Ticket{
		Title:     "Widget",
		Slug:      "widget",
		Branch:    "feature/widget",
		BlockedBy: []TicketBlockedBy{{Ticket: "T-4", Kind: "merged"}},
		Aliases:   []string{"OLD-1"},
	}
	if err := st.CreateTicketRecord("T-1", rec); err != nil {
		t.Fatal(err)
	}

	closure := NewClosed(CloseCovered, "develdeco/jig#82", "shipped with the demo kit", time.Date(2026, 10, 25, 23, 59, 0, 0, time.UTC))
	if closure.At != "2026-10-25" {
		t.Fatalf("NewClosed dated %q, want the UTC day 2026-10-25", closure.At)
	}
	if err := st.CloseTicket("T-1", closure); err != nil {
		t.Fatalf("CloseTicket: %v", err)
	}

	got, err := st.ReadTicket("T-1")
	if err != nil {
		t.Fatal(err)
	}
	want := rec
	want.Closed = &closure
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after CloseTicket the record is\n%+v\nwant\n%+v", got, want)
	}

	data, err := os.ReadFile(st.TicketFilePath("T-1"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["schema_version"] != 1 {
		t.Errorf("schema_version = %v, want it to stay 1", raw["schema_version"])
	}
	wantClosed := map[string]any{"reason": "covered", "by": "develdeco/jig#82", "note": "shipped with the demo kit", "at": "2026-10-25"}
	if !reflect.DeepEqual(raw["closed"], wantClosed) {
		t.Errorf("closed = %v, want %v\n%s", raw["closed"], wantClosed, data)
	}

	if err := st.ReopenTicket("T-1"); err != nil {
		t.Fatalf("ReopenTicket: %v", err)
	}
	if got, err = st.ReadTicket("T-1"); err != nil || !reflect.DeepEqual(got, rec) {
		t.Fatalf("after ReopenTicket the record is %+v (err %v), want %+v", got, err, rec)
	}
	if data, _ = os.ReadFile(st.TicketFilePath("T-1")); strings.Contains(string(data), "closed") {
		t.Errorf("a reopened ticket.yaml still names the closure:\n%s", data)
	}
}

// TestCloseAndReopenRefuseTheStateTheTicketIsAlreadyIn: closing a closed ticket
// and reopening an open one are refused with a code and the next step, and the
// record is left as it was.
func TestCloseAndReopenRefuseTheStateTheTicketIsAlreadyIn(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.CreateTicketRecord("T-1", Ticket{Title: "Widget"}); err != nil {
		t.Fatal(err)
	}
	code := func(err error) string {
		var ae *axi.Error
		if !errors.As(err, &ae) {
			return ""
		}
		return ae.Code
	}

	if got := code(st.ReopenTicket("T-1")); got != "TICKET_NOT_CLOSED" {
		t.Errorf("reopening an open ticket: code %q, want TICKET_NOT_CLOSED", got)
	}
	first := Closed{Reason: CloseNotPlanned, At: "2026-10-25"}
	if err := st.CloseTicket("T-1", first); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(st.TicketFilePath("T-1"))
	err := st.CloseTicket("T-1", Closed{Reason: CloseCovered, By: "x", At: "2026-10-26"})
	if got := code(err); got != "TICKET_ALREADY_CLOSED" {
		t.Errorf("closing a closed ticket: code %q, want TICKET_ALREADY_CLOSED", got)
	}
	var ae *axi.Error
	if errors.As(err, &ae) && !strings.Contains(strings.Join(ae.Help, " "), "jig reopen T-1") {
		t.Errorf("the refusal's help %v does not name `jig reopen T-1`", ae.Help)
	}
	if after, _ := os.ReadFile(st.TicketFilePath("T-1")); string(after) != string(before) {
		t.Errorf("a refused close changed ticket.yaml:\nbefore %q\nafter  %q", before, after)
	}
}

// TestClosedCheck: the record is checked for a reason that is one of the
// three, a by where the reason needs one and none where it has none, and a
// date that is a date.
func TestClosedCheck(t *testing.T) {
	cases := []struct {
		name string
		c    Closed
		want string // a substring of the one problem, "" for none
	}{
		{"covered with a reference", Closed{Reason: CloseCovered, By: "develdeco/jig#82", At: "2026-10-25"}, ""},
		{"superseded with a ticket", Closed{Reason: CloseSuperseded, By: "T-2"}, ""},
		{"not planned alone", Closed{Reason: CloseNotPlanned, Note: "decided against"}, ""},
		{"covered without by", Closed{Reason: CloseCovered}, "needs by"},
		{"superseded with a blank by", Closed{Reason: CloseSuperseded, By: "  "}, "needs by"},
		{"not planned with by", Closed{Reason: CloseNotPlanned, By: "T-2"}, "has no by"},
		{"unknown reason", Closed{Reason: "done"}, `reason "done" is none of covered, superseded, not-planned`},
		{"no reason", Closed{}, "is none of"},
		{"a date that is none", Closed{Reason: CloseNotPlanned, At: "yesterday"}, "is not a date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := tc.c.Check()
			if tc.want == "" {
				if len(problems) != 0 {
					t.Fatalf("Check = %v, want none", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tc.want) {
				t.Fatalf("Check = %v, want one problem containing %q", problems, tc.want)
			}
		})
	}
}

// TestClosedPhraseAndReleases: how a closure reads in a sentence, and which
// reasons let the tickets that wait for the ticket go on.
func TestClosedPhraseAndReleases(t *testing.T) {
	phrases := map[string]Closed{
		"covered by develdeco/jig#82": {Reason: CloseCovered, By: "develdeco/jig#82"},
		"superseded by SHOP-4":        {Reason: CloseSuperseded, By: "SHOP-4"},
		"not planned":                 {Reason: CloseNotPlanned},
	}
	for want, c := range phrases {
		if got := c.Phrase(); got != want {
			t.Errorf("Phrase of %+v = %q, want %q", c, got, want)
		}
	}
	for reason, want := range map[string]bool{CloseCovered: true, CloseSuperseded: true, CloseNotPlanned: false, "bogus": false} {
		if got := Releases(reason); got != want {
			t.Errorf("Releases(%q) = %v, want %v", reason, got, want)
		}
	}
}

// TestReadTicketRefusesAnUnknownKeyOfTheClosure: the closed entry is strict
// like the rest of the record, so a key it does not know fails the read.
func TestReadTicketRefusesAnUnknownKeyOfTheClosure(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := os.MkdirAll(st.TicketDir("T-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "schema_version: 1\nclosed:\n  reason: covered\n  by: x\n  who: someone\n"
	if err := os.WriteFile(st.TicketFilePath("T-1"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := st.ReadTicket("T-1")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_RECORD_INVALID" {
		t.Fatalf("ReadTicket with an unknown closed key = %v, want TICKET_RECORD_INVALID", err)
	}
}
