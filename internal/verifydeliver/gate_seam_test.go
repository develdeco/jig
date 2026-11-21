package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

func gateOpenCount(t *testing.T, st *store.Store, ticket string) int {
	t.Helper()
	lines, err := journal.Read(st, ticket)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range lines {
		if l.Event == journal.EventGateOpen {
			n++
		}
	}
	return n
}

func keepNote(t *testing.T, st *store.Store, ticket, id, workspace, outcome, reason string) {
	t.Helper()
	line := journal.Line{Event: journal.EventTriage, Ref: id, Outcome: outcome, Summary: reason, Command: TriageTarget(workspace, "test"), Provenance: "person"}
	if err := journal.Append(st, ticket, line); err != nil {
		t.Fatal(err)
	}
}

// TestSeamAGateQueuesKeptNotesBeforeARoundOpensAndSpendsThem: the rule behind
// a kept note's life. Round 1 reports four low notes (the risk floor routes
// them to notes). A person keeps two of one workspace and one of another and
// dismisses the fourth. The next gate queues one fix slice per workspace (the
// two keeps are one slice carrying both reasons and ids; the dismissed note has
// none), opens no round, counts the keeps as the next round's fix budget, and
// refuses to queue them again; a keep made later queues only itself.
func TestSeamAGateQueuesKeptNotesBeforeARoundOpensAndSpendsThem(t *testing.T) {
	t.Parallel()
	fx, d, _ := cloneBuilt(t)
	ticket := fx.Ticket

	p, err := GatePrepare(d, GateOpts{Ticket: ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	res := cleanReport(p)
	for i, f := range []string{"alpha/alpha.go", "alpha/alpha.go", "alpha/alpha.go", "alpha/alpha.go"} {
		res.Findings = append(res.Findings, ResultFinding{File: f, Line: i + 1, Title: "note " + f, Detail: "d", Action: ActionFix, Risk: RiskLow, RiskRationale: "cosmetic", Oracle: "test"})
	}
	if _, err := GateFinish(d, p, res, Provenance{Session: "s-reviewer", How: ReviewerByFlag}); err != nil {
		t.Fatalf("GateFinish: %v", err)
	}
	_, notes, _, err := OutstandingFindings(d.Store, ticket)
	if err != nil || len(notes) != 4 {
		t.Fatalf("notes after round 1 = %+v, %v, want four", notes, err)
	}

	keepNote(t, d.Store, ticket, "r1-f1", "alpha", TriageKeep, "Say it now.")
	keepNote(t, d.Store, ticket, "r1-f2", "alpha", TriageKeep, "Share the check now.")
	keepNote(t, d.Store, ticket, "r1-f4", "alpha", TriageDismiss, "")
	opened := gateOpenCount(t, d.Store, ticket)

	again, err := GatePrepare(d, GateOpts{Ticket: ticket})
	if err != nil {
		t.Fatalf("GatePrepare after the keeps: %v", err)
	}
	if len(again.Queued) != 1 || again.Queued[0] != "fix-2-q-alpha" {
		t.Fatalf("queued = %v, want the one slice fix-2-q-alpha for both keeps", again.Queued)
	}
	if got := gateOpenCount(t, d.Store, ticket); got != opened {
		t.Fatalf("gate-open lines = %d, want %d: queuing a keep opens no round", got, opened)
	}
	if _, err := os.Stat(filepath.Join(fx.StoreDir, "tickets", ticket, "gate", "round-2")); err == nil {
		t.Fatal("round-2 exists: queuing a keep records no round")
	}
	slices, err := d.Store.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	var fix *store.Slice
	for i := range slices {
		if slices[i].FromGate == 2 {
			if fix != nil {
				t.Fatalf("round 2 queued a second slice %+v", slices[i])
			}
			fix = &slices[i]
		}
	}
	if fix == nil || len(fix.Findings) != 2 || fix.Findings[0] != "r1-f1" || fix.Findings[1] != "r1-f2" {
		t.Fatalf("fix slice = %+v, want one slice of r1-f1 and r1-f2", fix)
	}
	for _, want := range []string{"Say it now.", "Share the check now.", "Gate finding r1-f1, kept by the human:", "Gate finding r1-f2, kept by the human:"} {
		if !strings.Contains(fix.Goal, want) {
			t.Errorf("goal missing %q:\n%s", want, fix.Goal)
		}
	}
	if used, _ := UsedFixBudget(d.Store, ticket, 3); used != 1 {
		t.Errorf("fix budget used = %d, want 1: the keep queued before round 2 counts", used)
	}

	// The slice is open work: a gate before it is green queues nothing more.
	if _, err := GatePrepare(d, GateOpts{Ticket: ticket}); err == nil {
		t.Fatal("a gate with the kept slice unbuilt succeeded, want a refusal")
	}
	if err := d.Store.WriteSliceState(ticket, fix.ID, store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	// A later keep queues only itself; the spent ones are not queued again.
	keepNote(t, d.Store, ticket, "r1-f3", "beta", TriageKeep, "Do it.")
	later, err := GatePrepare(d, GateOpts{Ticket: ticket})
	if err != nil {
		t.Fatalf("GatePrepare after a later keep: %v", err)
	}
	if len(later.Queued) != 1 || later.Queued[0] == fix.ID {
		t.Fatalf("queued = %v, want one new slice: the spent keeps are not queued again", later.Queued)
	}
	all, err := d.Store.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.ID == later.Queued[0] && (len(s.Findings) != 1 || s.Findings[0] != "r1-f3") {
			t.Errorf("slice %s = %+v, want a slice of r1-f3 alone", s.ID, s)
		}
	}
	if err := d.Store.WriteSliceState(ticket, later.Queued[0], store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatal(err)
	}

	// Round 2 opens and closes: the keeps queued for it still count as its
	// fix budget, and no keep is queued a second time.
	p2, err := GatePrepare(d, GateOpts{Ticket: ticket})
	if err != nil {
		t.Fatalf("GatePrepare round 2: %v", err)
	}
	if len(p2.Queued) != 0 {
		t.Fatalf("round 2 queued %v, want nothing: every keep is spent", p2.Queued)
	}
	// The reviewer reports r1-f1 again, as the review skill tells it to: the
	// keep is applied once, in the slice already queued, and the round lists
	// the finding once.
	again2 := cleanReport(p2)
	again2.StillPresent = []StillPresentEntry{{Prior: "r1-f1", Line: 1}}
	if _, err := GateFinish(d, p2, again2, Provenance{Session: "s-reviewer", How: ReviewerByFlag}); err != nil {
		t.Fatalf("GateFinish round 2: %v", err)
	}
	if used, _ := UsedFixBudget(d.Store, ticket, 3); used != 1 {
		t.Errorf("fix budget used after round 2 = %d, want 1: both keeps were queued for round 2", used)
	}
	after, err := d.Store.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	queuedFor := 0
	for _, s := range after {
		for _, f := range s.Findings {
			if f == "r1-f1" {
				queuedFor++
			}
		}
	}
	if queuedFor != 1 {
		t.Errorf("r1-f1 is in %d slices, want 1", queuedFor)
	}
	ff, ok, err := readFindingsYAML(d.Store, ticket, 2)
	if err != nil || !ok {
		t.Fatalf("round 2 findings: %v, %v", ok, err)
	}
	entries := 0
	for _, f := range ff.Findings {
		if f.ID == "r1-f1" {
			entries++
		}
	}
	if entries != 1 {
		t.Errorf("round 2 findings.yaml lists r1-f1 %d times, want once", entries)
	}
}
