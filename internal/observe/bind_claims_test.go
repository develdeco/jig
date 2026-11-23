package observe

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestAClaimBindsHoweverItIsSpelledAndWhoeverHolds: the claim call names its
// ticket after its flags; a claim that names no slice binds by the claims the
// agent made; the main session keeps its claim through its own stops;
// another session's claim binds nobody here.
func TestAClaimBindsHoweverItIsSpelledAndWhoeverHolds(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.worktreeOn("JIG-2")
	r.handle(r.ev(KindSessionStart))

	r.addTo(r.fx.Ticket, journal.Line{Event: journal.EventClaim, Slice: "b", Session: "s1"})
	expectTicket(t, "flags before the ticket", r.call("", "jig claim --unattended --slice b "+r.fx.Ticket), r.fx.Ticket)
	r.handle(r.ev(KindStop))
	expectTicket(t, "a call after the session's own stop", r.call("", "go test ./..."), r.fx.Ticket)

	r.addTo("JIG-2", journal.Line{Event: journal.EventClaim, Session: "s1"})
	expectTicket(t, "a claim of no named slice", r.call("a-1", "jig claim JIG-2"), "JIG-2")
	expectTicket(t, "the call after it", r.call("a-1", "ls"), "JIG-2")

	r.addTo(r.fx.Ticket, journal.Line{Event: journal.EventClaim, Slice: "c", Session: "other"})
	expectTicket(t, "a claim of another session", r.call("a-4", "jig claim --slice c "+r.fx.Ticket), r.fx.Ticket)
	expectTicket(t, "its next call", r.call("a-4", "ls"), "")
}
