package land_test

import (
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/store"
)

// TestAfterShownSuggestionsStates: a predecessor is landed, not landed or
// closed, one list for every reader; a closed one is no wait and an issue with
// no after has none.
func TestAfterShownSuggestionsStates(t *testing.T) {
	w := newWorld(t)
	w.ticket("I-1", store.Ticket{Kind: store.KindIssue})
	w.ticket("I-2", store.Ticket{Kind: store.KindIssue})
	w.ticket("I-3", store.Ticket{Kind: store.KindIssue, Closed: &store.Closed{Reason: "not planned"}})
	w.ticket("I-4", store.Ticket{Kind: store.KindIssue, After: []string{"I-1", "I-2", "I-3"}})
	w.publish("I-1", "", prURL("1"), "closed", "main")
	if err := journal.Append(w.st, "I-1", journal.Line{TS: time.Now().UTC().Format(time.RFC3339), Event: journal.EventLanded, URL: prURL("1")}); err != nil {
		t.Fatal(err)
	}
	rows, err := land.Suggestions(w.d, "I-4")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{land.AfterLanded, land.AfterNotLanded, land.AfterClosed}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	for i, r := range rows {
		if r.State != want[i] {
			t.Errorf("%s = %s, want %s", r.Ticket, r.State, want[i])
		}
		if r.Pending() != (want[i] == land.AfterNotLanded) {
			t.Errorf("%s Pending = %v", r.Ticket, r.Pending())
		}
	}
	if rows, _ := land.Suggestions(w.d, "I-1"); len(rows) != 0 {
		t.Errorf("an issue with no after has rows: %+v", rows)
	}
	if got, _ := land.Followers(w.d, "I-2"); len(got) != 1 || got[0] != "I-4" {
		t.Errorf("followers of I-2 = %v", got)
	}
	if got, _ := land.Followers(w.d, "I-1"); len(got) != 1 || got[0] != "I-4" {
		t.Errorf("followers of I-1 = %v", got)
	}
	rows, _ = land.Suggestions(w.d, "I-4")
	if notes := land.PendingNote("I-4", rows); len(notes) != 1 {
		t.Errorf("one predecessor is not landed, notes = %v", notes)
	}
}

// TestAfterShownIsNotARule: land.Rules holds no rule for it, so a not-landed
// predecessor refuses nothing.
func TestAfterShownIsNotARule(t *testing.T) {
	w := newWorld(t)
	w.ticket("I-1", store.Ticket{Kind: store.KindIssue})
	w.ticket("I-2", store.Ticket{Kind: store.KindIssue, After: []string{"I-1"}})
	w.publish("I-2", "", prURL("2"), "open", "main")
	if v := w.judge("I-2", land.Options{}); v.Refusal != nil || len(v.Merge) != 1 {
		t.Fatalf("verdict = %+v, want its merge named and no refusal", v)
	}
}
