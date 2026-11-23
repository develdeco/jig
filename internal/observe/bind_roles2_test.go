package observe

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// TestAPlannerIsBoundFromTheMintCallOnAsTheCLIOrdersIt: `jig ticket new` and
// `jig graduate` tie the session to the ticket while they run, so the touch is
// in the ledger before the hook writes the call. The mint call and the
// drafting calls are the ticket's until the approval.
func TestAPlannerIsBoundFromTheMintCallOnAsTheCLIOrdersIt(t *testing.T) {
	t.Parallel()
	for _, mint := range []string{`jig ticket new --title "Cart totals"`, "jig graduate some-chart"} {
		r := newRig(t)
		r.handle(r.ev(KindSessionStart))
		expectTicket(t, "a call before the ticket", r.call("", "go test ./..."), "")
		if err := ledger.TouchSession(r.fx.Home, r.clock.Now(), ledger.Registration{ID: "s1", Ticket: r.fx.Ticket}); err != nil {
			t.Fatal(err)
		}
		expectTicket(t, mint, r.call("", mint), r.fx.Ticket)
		expectTicket(t, "a call drafting the brief", r.call("", "jig skill intake"), r.fx.Ticket)
		expectTicket(t, "another", r.call("", "cat brief.md"), r.fx.Ticket)
		r.addTo(r.fx.Ticket, journal.Line{Event: journal.EventApproval, Session: "s1"})
		expectTicket(t, "a call after the approval", r.call("", "git log"), "")
	}
}

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
