package observe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// holdRig is a builder a-1 that claimed the fixture ticket unattended from the
// session's checkout and works in a worktree of its own.
func holdRig(t *testing.T) (*rig, string) {
	r := newRig(t)
	wt := r.worktreeOn(r.fx.Ticket)
	r.handle(r.ev(KindSessionStart))
	r.addTo(r.fx.Ticket, journal.Line{Event: journal.EventClaim, Slice: "a", Session: "s1", Unattended: true})
	expectTicket(t, "the claim", r.call("a-1", "jig claim "+r.fx.Ticket+" --slice a --unattended"), r.fx.Ticket)
	return r, wt
}

// TestARefusedStopLeavesTheClaimAndTheNextCallsBound: a stop jig refused is no
// stop, so the held builder's next calls stay on its ticket.
func TestARefusedStopLeavesTheClaimAndTheNextCallsBound(t *testing.T) {
	t.Parallel()
	r, wt := holdRig(t)
	r.call("a-1", "cd "+filepath.ToSlash(wt)+" && ls")
	if err := os.WriteFile(filepath.Join(wt, "go.mod"), []byte("module mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := r.handle(r.ev(KindStop, func(e *Event) { e.Agent = "a-1" })); !strings.Contains(res.Block, "go.mod") {
		t.Fatalf("a stop with its own tree dirty = %+v, want it held, for the files of its own tree", res)
	}
	expectTicket(t, "a call after a refused stop", r.call("a-1", "jig verify "+r.fx.Ticket+" --slice a"), r.fx.Ticket)
}

// TestTheSessionsCheckoutDoesNotHoldASubagentPlacedByItsClaim: files changed
// in the session's checkout are not the builder's work; with no directory of
// its own known it is placed on its ticket and held for none.
func TestTheSessionsCheckoutDoesNotHoldASubagentPlacedByItsClaim(t *testing.T) {
	t.Parallel()
	r, _ := holdRig(t)
	if err := os.WriteFile(filepath.Join(r.fx.RepoDir, "go.mod"), []byte("module other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := r.handle(r.ev(KindStop, func(e *Event) { e.Agent = "a-1" })); res.Block != "" {
		t.Errorf("a stop read for the session's checkout = %+v, want no block", res)
	}
}
