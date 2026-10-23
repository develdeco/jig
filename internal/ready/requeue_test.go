package ready

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// newStore generates the fixture and opens its store.
func newStore(t *testing.T, opts fixture.Opts) (*fixture.Fixture, *store.Store) {
	t.Helper()
	opts.Home = t.TempDir()
	fx := fixture.Generate(t, opts)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return fx, st
}

// amendBrief replaces the ticket's brief.md with the one the flawed-brief
// scenario amends it to: the section slice c cites changes, the rest do not.
func amendBrief(t *testing.T, st *store.Store, ticket string) {
	t.Helper()
	amended, err := os.ReadFile(filepath.Join(fixture.RepoRoot(t), "testdata", "fixture", "scenario-branches", "flawed-brief", "brief-amended.md"))
	if err != nil {
		t.Fatalf("read brief-amended.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(st.TicketDir(ticket), "brief.md"), amended, 0o644); err != nil {
		t.Fatalf("write amended brief.md: %v", err)
	}
}

// parkOnFlawedBrief leaves slice c where a builder that judged the brief
// flawed leaves it: needs-input on an open question, with the reason.
func parkOnFlawedBrief(t *testing.T, st *store.Store, ticket string) {
	t.Helper()
	if err := st.WriteQuestion(ticket, store.Question{ID: "q-001", Slice: "c", Status: "open", Body: "The brief does not say which greeting; amend brief.md section(s) Greeting"}); err != nil {
		t.Fatalf("write question: %v", err)
	}
	if err := st.WriteSliceState(ticket, "c", store.SliceState{State: "needs-input", Attempts: 1, Reason: "flawed-brief", Question: "q-001"}); err != nil {
		t.Fatalf("write slice state: %v", err)
	}
}

func TestRequeueFromBriefDiff(t *testing.T) {
	t.Parallel()
	fx, st := newStore(t, fixture.Opts{})
	parkOnFlawedBrief(t, st, fx.Ticket)
	amendBrief(t, st, fx.Ticket)

	touched, err := Requeue(st, fx.Ticket, true)
	if err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	if len(touched) != 1 || touched[0] != "c" {
		t.Fatalf("Requeue touched = %v, want exactly [c]", touched)
	}

	cState, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c) after requeue: %v", err)
	}
	if cState.State != "queued" || cState.Attempts != 1 || cState.Reason != "" || cState.Question != "" {
		t.Fatalf("c after requeue = %+v, want queued/attempts=1 (kept)/reason+question cleared", cState)
	}
	q, err := st.ReadQuestion(fx.Ticket, "q-001")
	if err != nil || q.Status == "open" {
		t.Fatalf("q-001 after requeue = %+v, %v, want it no longer open: the amendment answers it", q, err)
	}
	var requeued []string
	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if l.Event == journal.EventRequeue {
			requeued = append(requeued, l.Slice)
		}
	}
	if strings.Join(requeued, ",") != "c" {
		t.Errorf("requeue journal lines are for slices %v, want [c]", requeued)
	}
	// The slices that cite no changed section stay as they were.
	for _, id := range []string{"a", "b", "d"} {
		if got, _ := st.ReadSliceState(fx.Ticket, id); got.State != "" && got.State != "queued" {
			t.Errorf("slice %s = %+v, want it untouched", id, got)
		}
	}
}

// TestRequeueWithoutTheBriefDiffDoesNothing: --from-brief-diff is its only
// mode, so without it there is nothing to requeue.
func TestRequeueWithoutTheBriefDiffDoesNothing(t *testing.T) {
	t.Parallel()
	fx, st := newStore(t, fixture.Opts{})
	parkOnFlawedBrief(t, st, fx.Ticket)
	amendBrief(t, st, fx.Ticket)
	if touched, err := Requeue(st, fx.Ticket, false); err != nil || touched != nil {
		t.Fatalf("Requeue without --from-brief-diff = %v, %v, want nothing", touched, err)
	}
	if got, _ := st.ReadSliceState(fx.Ticket, "c"); got.State != "needs-input" {
		t.Errorf("c = %+v, want it left parked", got)
	}
}

// TestRequeueClearsStalledSignature: Requeue must clear a touched slice's
// stall Signature and StallSummary alongside Reason, not just Reason - a
// stale signature or summary left behind by an earlier stall must not
// survive a requeue driven by Requeue itself. Signature and StallSummary
// are seeded directly so this fails on its own if Requeue's own clear is
// removed.
func TestRequeueClearsStalledSignature(t *testing.T) {
	t.Parallel()
	fx, st := newStore(t, fixture.Opts{})
	parkOnFlawedBrief(t, st, fx.Ticket)
	cState, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c): %v", err)
	}
	cState.Signature = "c|code-bug|stale signature from a previous stall"
	cState.StallSummary = "stale summary from a previous stall"
	if err := st.WriteSliceState(fx.Ticket, "c", cState); err != nil {
		t.Fatalf("write slice state: %v", err)
	}
	amendBrief(t, st, fx.Ticket)

	touched, err := Requeue(st, fx.Ticket, true)
	if err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	if len(touched) != 1 || touched[0] != "c" {
		t.Fatalf("Requeue touched = %v, want exactly [c]", touched)
	}

	after, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c) after requeue: %v", err)
	}
	if after.Signature != "" {
		t.Fatalf("c Signature = %q, want cleared by Requeue", after.Signature)
	}
	if after.StallSummary != "" {
		t.Fatalf("c StallSummary = %q, want cleared by Requeue", after.StallSummary)
	}
}

// TestRequeueFromBriefDiffWithoutABriefIsRefused: requeue --from-brief-diff
// compares a brief's sections with the slices', so a ticket with no brief.md
// (an adopted one is) is refused with an axi code and the slices' own
// remedies, not a raw "no such file" failure. A ticket that has a brief is
// unaffected.
func TestRequeueFromBriefDiffWithoutABriefIsRefused(t *testing.T) {
	t.Parallel()
	fx, st := newStore(t, fixture.Opts{})

	if _, err := Requeue(st, fx.Ticket, true); err != nil {
		t.Fatalf("Requeue on a ticket with a brief: %v", err)
	}

	if err := os.Remove(filepath.Join(st.TicketDir(fx.Ticket), "brief.md")); err != nil {
		t.Fatalf("remove brief.md: %v", err)
	}
	_, err := Requeue(st, fx.Ticket, true)
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" {
		t.Fatalf("Requeue with no brief: err = %v, want an *axi.Error VALIDATION_ERROR", err)
	}
	help := strings.Join(ae.Help, "\n")
	if !strings.Contains(ae.Msg, "no brief.md") || !strings.Contains(help, "--slice") || !strings.Contains(help, "jig answer "+fx.Ticket) {
		t.Fatalf("the refusal = %q / %q, want it to name the missing brief and the slices' own remedies", ae.Msg, help)
	}
}

// TestRequeueSliceStalled: RequeueSlice on a stalled slice (no brief section
// involved) puts it back to queued with its attempts kept and its reason,
// signature and stall summary cleared, and a requeue line in the journal.
func TestRequeueSliceStalled(t *testing.T) {
	t.Parallel()
	fx, st := newStore(t, fixture.Opts{})
	before := store.SliceState{State: "stalled", Attempts: 2, Reason: "stall", Signature: "a|code-bug|nil pointer", StallSummary: "nil pointer dereference in Clamp"}
	if err := st.WriteSliceState(fx.Ticket, "a", before); err != nil {
		t.Fatal(err)
	}

	if err := RequeueSlice(st, fx.Ticket, "a"); err != nil {
		t.Fatalf("RequeueSlice: %v", err)
	}

	after, err := st.ReadSliceState(fx.Ticket, "a")
	if err != nil {
		t.Fatalf("ReadSliceState(a) after RequeueSlice: %v", err)
	}
	if after.State != "queued" || after.Attempts != before.Attempts {
		t.Fatalf("a after RequeueSlice = %+v, want queued/attempts=%d (kept)", after, before.Attempts)
	}
	if after.Reason != "" || after.Signature != "" || after.StallSummary != "" {
		t.Fatalf("a after RequeueSlice = %+v, want reason/signature/stall summary cleared", after)
	}

	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	sawRequeue := false
	for _, l := range lines {
		if l.Slice == "a" && l.Event == journal.EventRequeue {
			sawRequeue = true
		}
	}
	if !sawRequeue {
		t.Fatalf("no requeue journal line for slice a; journal:\n%+v", lines)
	}
}

// TestRequeueSliceEnvBlocked: RequeueSlice on an env-blocked slice puts it
// back to queued the same way it does for stalled.
func TestRequeueSliceEnvBlocked(t *testing.T) {
	t.Parallel()
	fx, st := newStore(t, fixture.Opts{})
	before := store.SliceState{State: "env-blocked", Attempts: 1, Reason: "env-up-failed"}
	if err := st.WriteSliceState(fx.Ticket, "d", before); err != nil {
		t.Fatal(err)
	}

	if err := RequeueSlice(st, fx.Ticket, "d"); err != nil {
		t.Fatalf("RequeueSlice: %v", err)
	}

	after, err := st.ReadSliceState(fx.Ticket, "d")
	if err != nil {
		t.Fatalf("ReadSliceState(d) after RequeueSlice: %v", err)
	}
	if after.State != "queued" || after.Attempts != before.Attempts {
		t.Fatalf("d after RequeueSlice = %+v, want queued/attempts=%d (kept)", after, before.Attempts)
	}
	if after.Reason != "" {
		t.Fatalf("d after RequeueSlice = %+v, want reason cleared", after)
	}
}

// TestRequeueSliceRefusesOtherStates: RequeueSlice refuses a slice that is
// neither stalled nor env-blocked (queued, building, green or needs-input),
// since it cannot tell a caller's mistake from a stale id, and it must not
// silently repark an unrelated slice.
func TestRequeueSliceRefusesOtherStates(t *testing.T) {
	t.Parallel()
	fx, st := newStore(t, fixture.Opts{})

	for _, state := range []string{"queued", "building", "green", "needs-input"} {
		t.Run(state, func(t *testing.T) {
			if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: state, Attempts: 1}); err != nil {
				t.Fatalf("write slice state: %v", err)
			}
			if err := RequeueSlice(st, fx.Ticket, "a"); err == nil {
				t.Fatalf("RequeueSlice on a %s slice: err = nil, want a refusal", state)
			}
			after, err := st.ReadSliceState(fx.Ticket, "a")
			if err != nil {
				t.Fatalf("ReadSliceState(a): %v", err)
			}
			if after.State != state {
				t.Fatalf("a state after refused RequeueSlice = %q, want unchanged %q", after.State, state)
			}
		})
	}
}

// TestRequeueSliceUnknownID: RequeueSlice on an id that names no slice in
// the ticket's own slices.yaml refuses with its own code naming the unknown
// id, rather than reading the absent state file's zero value as a real
// "queued" slice and reporting a wrong-state refusal for a slice that was
// never there.
func TestRequeueSliceUnknownID(t *testing.T) {
	t.Parallel()
	fx, st := newStore(t, fixture.Opts{})

	err := RequeueSlice(st, fx.Ticket, "no-such-slice")
	if err == nil {
		t.Fatal("RequeueSlice on an unknown id: err = nil, want a refusal")
	}
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "SLICE_NOT_FOUND" {
		t.Fatalf("RequeueSlice on an unknown id: err = %v, want *axi.Error SLICE_NOT_FOUND", err)
	}
	if !strings.Contains(ae.Msg, "no-such-slice") {
		t.Fatalf("RequeueSlice on an unknown id: Msg = %q, want it to name the unknown id", ae.Msg)
	}
	if strings.Contains(ae.Msg, "queued") {
		t.Fatalf("RequeueSlice on an unknown id: Msg = %q, still claims a state", ae.Msg)
	}
}
