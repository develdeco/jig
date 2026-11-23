package observe

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// stopsPlacedOn is the ticket of each stop hook line the agent's stops wrote.
func (r *rig) stopsPlacedOn(agent string, n int) []string {
	r.t.Helper()
	for i := 0; i < n; i++ {
		r.handle(r.ev(KindStop, func(e *Event) { e.Agent = agent }))
	}
	var out []string
	for _, l := range r.hooksOf(KindStop) {
		out = append(out, l.Ticket)
	}
	return out
}

// TestAStopAfterItsTicketsPublishIsPlacedByTheClaim: a claim that ended stops
// binding calls, never where the agent's spend goes.
func TestAStopAfterItsTicketsPublishIsPlacedByTheClaim(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.start("startup")
	r.claimCall("a-1", r.fx.Ticket, "a")
	r.addTo(r.fx.Ticket, journal.Line{Event: journal.EventPr, Session: "s1"})
	expectTicket(t, "a call after the publish", r.call("a-1", "git log"), "")
	for i, got := range r.stopsPlacedOn("a-1", 2) {
		expectTicket(t, "stop "+string(rune('0'+i)), got, r.fx.Ticket)
	}
}

// TestAStopAfterItsSlicesReleaseIsPlacedByTheClaim: a builder that gave its
// slice up and stops is placed by the claim it gave up.
func TestAStopAfterItsSlicesReleaseIsPlacedByTheClaim(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.start("startup")
	r.claimCall("a-1", r.fx.Ticket, "a")
	r.releaseCall("a-1", r.fx.Ticket, "a")
	for i, got := range r.stopsPlacedOn("a-1", 1) {
		expectTicket(t, "stop "+string(rune('0'+i)), got, r.fx.Ticket)
	}
}
