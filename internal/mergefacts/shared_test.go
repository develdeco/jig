package mergefacts

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// TestSharedMerge: a task two issues list is published and merged for each
// issue by the pull request its into line names; OfFor reads one issue's, and
// Of is met only when every issue's is.
func TestSharedMerge(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	st := &store.Store{Root: t.TempDir()}
	for _, id := range []string{"I-1", "I-2"} {
		if err := st.CreateTicketRecord(id, store.Ticket{Title: "Issue", Kind: store.KindIssue}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CreateTicketRecord("JIG-1", store.Ticket{Title: "First"}); err != nil {
		t.Fatal(err)
	}
	for name, issue := range map[string]string{"a": "I-1", "b": "I-2"} {
		if err := store.AtomicWrite(st.ChartFile(name), []byte("issue: "+issue+"\ntickets:\n  - id: JIG-1\n    title: First\n")); err != nil {
			t.Fatal(err)
		}
	}
	u1, u2 := "https://github.com/acme/app/pull/1", "https://github.com/acme/app/pull/2"
	prs := &fakePRs{reads: map[string]PRRead{u1: {State: ready.PRMerged}, u2: {State: ready.PROpen}}}
	rd := func() *Reader { return reader(st, r, prs) }
	if m, err := rd().OfFor("JIG-1", "I-1"); err != nil || m.Published {
		t.Fatalf("nothing published: %+v, %v", m, err)
	}
	for _, l := range []journal.Line{
		{Event: journal.EventPr, Into: "I-1", URL: u1}, {Event: journal.EventPublishDone, Into: "I-1"},
		{Event: journal.EventPr, Into: "I-2", URL: u2}, {Event: journal.EventPublishDone, Into: "I-2"},
	} {
		if err := journal.Append(st, "JIG-1", l); err != nil {
			t.Fatal(err)
		}
	}
	if m, err := rd().OfFor("JIG-1", "I-1"); err != nil || !m.Met() {
		t.Errorf("I-1's pull request merged: %+v, %v, want met", m, err)
	}
	if m, err := rd().OfFor("JIG-1", "I-2"); err != nil || m.Met() || !m.Published {
		t.Errorf("I-2's pull request open: %+v, %v, want published, not met", m, err)
	}
	if m, err := rd().Of("JIG-1"); err != nil || m.Met() {
		t.Errorf("Of with one issue open: %+v, %v, want not met", m, err)
	}
	prs.reads[u2] = PRRead{State: ready.PRMerged}
	if m, err := rd().Of("JIG-1"); err != nil || !m.Met() {
		t.Errorf("Of with both merged: %+v, %v, want met", m, err)
	}
}
