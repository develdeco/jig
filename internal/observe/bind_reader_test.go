package observe

import (
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestAClaimEndsAtItsSlicesVerification: the build flow never releases a green
// slice, so a claim ends when its slice is verified; the main session's later
// calls in another worktree bind by that worktree and in the main checkout to
// none.
func TestAClaimEndsAtItsSlicesVerification(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	other := r.worktreeOn("JIG-2")
	r.handle(r.ev(KindSessionStart))

	expectTicket(t, "the claim", r.claimCall("", r.fx.Ticket, "a"), r.fx.Ticket)
	expectTicket(t, "a call while it is held", r.call("", "go test ./..."), r.fx.Ticket)
	r.addTo(r.fx.Ticket, journal.Line{Event: journal.EventVerified, Slice: "a", Session: "s1"})
	expectTicket(t, "a call in another worktree after the verification", r.call("", "git push", other), "JIG-2")
	expectTicket(t, "a call in the main checkout after it", r.call("", "git log"), "")

	// A slice claimed again after its verification (a fix) is held again.
	r.addTo(r.fx.Ticket, journal.Line{Event: journal.EventClaim, Slice: "a", Session: "s1"})
	expectTicket(t, "a call under the new claim", r.call("", "go vet ./..."), r.fx.Ticket)
}

// TestBindingReadsNoTicketListForADirectId: a ticket named by its id, in a
// claim call or by the branch of a worktree, is found by its folder; the other
// tickets of the store are not read, so one that cannot be read hides nothing.
func TestBindingReadsNoTicketListForADirectId(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	wt := r.worktreeOn(r.fx.Ticket)
	// Another ticket carries this one's id as an alias: a walk of the store
	// is ambiguous, the folder of the id is not.
	if err := r.st.CreateTicketRecord("JIG-2", store.Ticket{Slug: "two", Aliases: []string{r.fx.Ticket}}); err != nil {
		t.Fatal(err)
	}
	r.handle(r.ev(KindSessionStart))

	expectTicket(t, "a claim call by id", r.call("a-1", "jig claim "+r.fx.Ticket+" --slice a"), r.fx.Ticket)
	expectTicket(t, "a worktree by its branch", r.call("a-2", "cd "+filepath.ToSlash(wt)+" && ls"), r.fx.Ticket)
	expectTicket(t, "a jig verb by id", r.call("a-3", "jig status "+r.fx.Ticket), r.fx.Ticket)
}

// TestAClaimOfATrackerNumberBindsItsTicket: a claim word that names no ticket
// (a tracker number the store has no item for, a typo) is resolved as jig
// claim resolves it, else the call binds to none, never to the bare word.
func TestAClaimOfATrackerNumberBindsItsTicket(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.handle(r.ev(KindSessionStart))

	expectTicket(t, "a number no item holds", r.call("a-1", "jig claim 12345 --slice a"), "")
	expectTicket(t, "a typo", r.call("a-1", "jig claim JIG-99 --slice a"), "")
	if err := r.st.CreateTicketRecord("JIG-2", store.Ticket{Slug: "two", Aliases: []string{"JIG-9"}}); err != nil {
		t.Fatal(err)
	}
	expectTicket(t, "an alias", r.call("a-1", "jig claim JIG-9 --slice a"), "JIG-2")
}

// TestAClaimSurvivesACompaction: a session that resumes or compacts starts
// again under the same id, and a subagent's held claim is still held.
func TestAClaimSurvivesACompaction(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.handle(r.ev(KindSessionStart))
	expectTicket(t, "the claim", r.claimCall("a-1", r.fx.Ticket, "a"), r.fx.Ticket)
	r.handle(r.ev(KindSessionStart))
	expectTicket(t, "a call after the session started again", r.call("a-1", "go test ./..."), r.fx.Ticket)
}
