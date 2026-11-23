package observe

import (
	"testing"
)

// TestAClosedReviewRoundLeavesAnOlderClaimBinding: a builder that reviewed
// its own work in between keeps the claim it still holds.
func TestAClosedReviewRoundLeavesAnOlderClaimBinding(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.worktreeOn("JIG-2")
	r.handle(r.ev(KindSessionStart))
	expectTicket(t, "the claim", r.claimCall("a-1", r.fx.Ticket, "a"), r.fx.Ticket)
	expectTicket(t, "a round of another ticket opened", r.call("a-1", "jig review context JIG-2"), "JIG-2")
	expectTicket(t, "closed", r.call("a-1", "jig review done JIG-2"), "JIG-2")
	expectTicket(t, "a call after it", r.call("a-1", "go test ./..."), r.fx.Ticket)
}
