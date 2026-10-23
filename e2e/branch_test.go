package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestFlawedBriefRequeue: a slice whose builder judged the brief flawed is
// parked on a question and says how to resume it: amend the brief, then `jig
// requeue --from-brief-diff`, which re-queues exactly the slice that cites a
// section that changed, closes its question, and puts it back among the
// slices `jig next` offers.
func TestFlawedBriefRequeue(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{ScenarioBranch: "flawed-brief"})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	// Slice c has a brief section (FromBrief) and is parked with reason
	// flawed-brief, as a builder that judged the brief flawed leaves it.
	if err := st.WriteQuestion(fx.Ticket, store.Question{ID: "q-001", Slice: "c", Status: "open", Body: "The brief does not say which tone; amend brief.md section Slice C - greeting decision"}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteSliceState(fx.Ticket, "c", store.SliceState{State: "needs-input", Attempts: 1, Reason: "flawed-brief", Question: "q-001"}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatal(err)
	}

	// `jig requeue <ticket> --from-brief-diff` alone is not the whole remedy:
	// run as printed with no brief amendment, it requeues nothing and the
	// next status is byte-identical. The hint must say the brief is amended
	// first, not last.
	status := runJig(t, env, fx.StoreDir, "status", fx.Ticket)
	if status.Code != 0 || !strings.Contains(status.Stdout, "amend the brief, then run jig requeue "+fx.Ticket+" --from-brief-diff") {
		t.Fatalf("jig status exit = %d, want the amend-first flawed-brief parked cell\n%s", status.Code, status.Stdout)
	}
	if !strings.Contains(status.Stdout, "Amend the brief, then run `jig requeue "+fx.Ticket+" --from-brief-diff` to resume") {
		t.Fatalf("jig status lacks the amend-first flawed-brief hint:\n%s", status.Stdout)
	}
	if nothing := runJig(t, env, fx.StoreDir, "requeue", fx.Ticket, "--from-brief-diff"); nothing.Code != 0 {
		t.Fatalf("jig requeue before any amendment exit = %d, want 0\n%s", nothing.Code, nothing.Stdout)
	} else {
		assertOnlySlicesListed(t, nothing.Stdout, nil)
	}

	// Amend the brief: overlaying the branch's brief-amended.md, materialized
	// alongside the scenario tree by fixture.Generate, over the store's
	// brief.md.
	amended := readFileOrFatal(t, joinPath(fx.ScenarioDir, "brief-amended.md"))
	writeFileOrFatal(t, joinPath(fx.StoreDir, "tickets", fx.Ticket, "brief.md"), amended)

	r2 := runJig(t, env, fx.StoreDir, "requeue", fx.Ticket, "--from-brief-diff")
	if r2.Code != 0 {
		t.Fatalf("jig requeue exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r2.Code, r2.Stdout, r2.Stderr)
	}
	assertOnlySlicesListed(t, r2.Stdout, []string{"c"})

	cs, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatal(err)
	}
	if cs.State != "queued" || cs.Reason != "" || cs.Question != "" || cs.Attempts != 1 {
		t.Fatalf("slice c after the requeue = %+v, want queued, attempts kept, reason and question cleared", cs)
	}
	// The question is closed with the amendment: it is not left open to hold
	// the ticket in a parked state after every slice is back to work.
	if q, err := st.ReadQuestion(fx.Ticket, "q-001"); err != nil || q.Status == "open" {
		t.Fatalf("q-001 after the requeue = %+v, %v, want it no longer open", q, err)
	}
	if _, ok := findJournalLine(readJournal(t, fx, fx.Ticket), journal.EventRequeue, "c", ""); !ok {
		t.Fatal("no requeue journal line for slice c")
	}
	next := runJig(t, env, fx.StoreDir, "next", fx.Ticket, "--json")
	if next.Code != 0 || !strings.Contains(next.Stdout, `"c"`) {
		t.Fatalf("jig next after the requeue does not offer slice c (exit %d):\n%s", next.Code, next.Stdout)
	}
}
