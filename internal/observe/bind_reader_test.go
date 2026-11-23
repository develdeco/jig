package observe

import (
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

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
