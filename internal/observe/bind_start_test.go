package observe

import (
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// start is a session start of the given source (startup, resume, clear or
// compact), as the harness reports it.
func (r *rig) start(source string) {
	r.t.Helper()
	r.handle(r.ev(KindSessionStart, func(e *Event) { e.Source = source }))
}

// TestAClaimSurvivesACompaction: a session that resumes or compacts starts
// again under the same id, and a subagent's held claim is still held; a start
// that is fresh (startup, clear) ends what came before it, as it always has.
func TestAClaimSurvivesACompaction(t *testing.T) {
	t.Parallel()
	for source, survives := range map[string]bool{"compact": true, "resume": true, "startup": false, "clear": false, "": false} {
		r := newRig(t)
		r.worktreeOn(r.fx.Ticket)
		r.start("startup")
		expectTicket(t, "the claim", r.claimCall("a-1", r.fx.Ticket, "a"), r.fx.Ticket)
		r.start(source)
		want := ""
		if survives {
			want = r.fx.Ticket
		}
		expectTicket(t, "a call after a start by "+source, r.call("a-1", "go test ./..."), want)
	}
}

// TestAClaimIsReadPastACompactionOnlyAsFarAsALongRun: a claim made longer
// before the start than a builder runs is not read.
func TestAClaimIsReadPastACompactionOnlyAsFarAsALongRun(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.start("startup")
	expectTicket(t, "the claim", r.claimCall("a-1", r.fx.Ticket, "a"), r.fx.Ticket)
	r.clock.advance(claimWindow + time.Hour)
	r.start("compact")
	expectTicket(t, "a call after it", r.call("a-1", "go test ./..."), "")
}

// TestAStopAfterItsSlicesVerificationIsPlacedByTheClaim: a builder verifies
// its slice and then stops; the verification ends nothing, so the stop is
// placed by the claim.
func TestAStopAfterItsSlicesVerificationIsPlacedByTheClaim(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.start("startup")
	r.claimCall("a-1", r.fx.Ticket, "a")
	r.addTo(r.fx.Ticket, journal.Line{Event: journal.EventVerified, Slice: "a", Session: "s1"})
	expectTicket(t, "a call after the verification", r.call("a-1", "git status"), r.fx.Ticket)
	for i := 0; i < 2; i++ {
		r.handle(r.ev(KindStop, func(e *Event) { e.Agent = "a-1" }))
	}
	for i, l := range r.hooksOf(KindStop) {
		if l.Ticket != r.fx.Ticket {
			t.Errorf("stop %d was placed on %q, want %q", i, l.Ticket, r.fx.Ticket)
		}
	}
}
