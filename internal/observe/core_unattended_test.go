package observe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
)

func TestStopHoldsAnUnattendedSessionBackOncePerFinding(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	sha := r.commit("a", "a.txt")
	r.add(journal.Line{Event: journal.EventVerifyPending, Slice: "a", Commit: sha, Attempt: 1})

	first := r.handle(r.ev(KindStop))
	if !strings.Contains(first.Block, "unattended") || !strings.Contains(first.Block, "verification of "+sha[:12]+" for slice a is still running") ||
		!strings.Contains(first.Block, "jig verify "+r.fx.Ticket+" "+sha[:12]+" --slice a --wait") {
		t.Fatalf("Block = %q, want the reason with the pending verification and how to wait for it", first.Block)
	}
	delivered := r.ledgerOf(journal.EventNoteDelivered)
	if len(delivered) != 1 || delivered[0].Outcome != "stop-block" || delivered[0].Session != "s1" {
		t.Fatalf("note-delivered lines = %+v, want the refusal recorded", delivered)
	}
	// The agent continues; at its next Stop the same verification is still
	// running and there is nothing new, so it may stop.
	again := r.handle(r.ev(KindStop, func(e *Event) { e.StopActive = true }))
	if again.Block != "" || again.Message == "" {
		t.Errorf("Stop while continuing with nothing new = %+v, want a message and no block", again)
	}
	// A new fact holds the session back again, even while it continues.
	sha2 := r.commit("b", "b.txt")
	r.add(journal.Line{Event: journal.EventVerifyPending, Slice: "b", Commit: sha2, Attempt: 1})
	third := r.handle(r.ev(KindStop, func(e *Event) { e.StopActive = true }))
	if !strings.Contains(third.Block, sha2[:12]) {
		t.Errorf("Stop while continuing with a new pending verification = %+v, want a block that names it", third)
	}
	// At the end of a new turn, whatever is still outstanding holds it.
	if fresh := r.handle(r.ev(KindStop)); fresh.Block == "" {
		t.Errorf("Stop at the end of a new turn with verifications running = %+v, want a block", fresh)
	}
}

func TestAnEnforcedNoteHoldsAnUnattendedSessionBackOnceEvenIfItWasToldBefore(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.d.Sources = []NamedSource{{"drift", func(NoteCtx) ([]Note, error) {
		return []Note{{Ref: "drift:1", Text: "brief section 2 changed", Enforce: true}, {Ref: "info:1", Text: "FYI"}}, nil
	}}}
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	if res := r.handle(r.ev(KindPrompt)); !strings.Contains(res.Context, "brief section 2 changed") {
		t.Fatalf("prompt context = %q, want the note told", res.Context)
	}

	first := r.handle(r.ev(KindStop))
	if !strings.Contains(first.Block, "brief section 2 changed") || strings.Contains(first.Block, "FYI") {
		t.Fatalf("Block = %q, want the enforced note it was told but not yet refused for, and not the advisory one it already heard", first.Block)
	}
	for _, active := range []bool{true, false} {
		if res := r.handle(r.ev(KindStop, func(e *Event) { e.StopActive = active })); !res.Empty() {
			t.Errorf("Stop (continuing %v) after the refusal = %+v, want nothing: once per note", active, res)
		}
	}
}

func TestAnInteractiveSessionIsOnlyToldAnEnforcedNote(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.d.Sources = []NamedSource{{"drift", func(NoteCtx) ([]Note, error) {
		return []Note{{Ref: "drift:1", Text: "scope drift in a.go", Enforce: true}}, nil
	}}}
	r.handle(r.ev(KindSessionStart))
	res := r.handle(r.ev(KindStop))
	if res.Block != "" || !strings.Contains(res.Message, "scope drift in a.go") {
		t.Errorf("Stop = %+v, want the note as a message and no block", res)
	}
	if got := r.ledgerOf(journal.EventNoteDelivered); len(got) != 0 {
		t.Errorf("a message to the person recorded %+v as delivered to the model", got)
	}
}

func TestAReleasedClaimIsNotUnattended(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	r.add(journal.Line{Event: journal.EventRelease, Slice: "a", Session: "s1"})
	sha := r.commit("a", "a.txt")
	r.add(journal.Line{Event: journal.EventVerifyPending, Slice: "a", Commit: sha, Attempt: 1})
	if res := r.handle(r.ev(KindStop)); res.Block != "" {
		t.Errorf("Stop after the claim was released = %+v, want no block", res)
	}
}

func TestTheJigYamlDefaultMakesASessionUnattended(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	sha := r.commit("a", "a.txt")
	r.add(journal.Line{Event: journal.EventVerifyPending, Slice: "a", Commit: sha, Attempt: 1})
	if res := r.handle(r.ev(KindStop)); res.Block != "" {
		t.Fatalf("Stop without a default = %+v, want no block", res)
	}
	cfg, err := project.Load(filepath.Join(r.fx.StoreDir, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	marker := "jig: \"1\"\nproject: " + cfg.Name + "\nunattended: true\n"
	if err := os.WriteFile(filepath.Join(r.fx.RepoDir, "jig.yaml"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := r.handle(r.ev(KindStop)); res.Block == "" {
		t.Errorf("Stop with unattended: true in jig.yaml = %+v, want a block", res)
	}
}
