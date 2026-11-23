package observe

import (
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// sliceOfLast is the slice the last tool line of the ledger is written on.
func (r *rig) sliceOfLast() string {
	lines := r.ledgerOf(journal.EventTool)
	return lines[len(lines)-1].Slice
}

// TestACallThatNamesAHeldTicketKeepsTheClaimsSlice: a builder's read of its
// own brief and its `jig context` calls are on its ticket and under its slice;
// a call that names another ticket is on that ticket and under none; an agent
// with no claim is under none. The path a call names is read before the role
// the agent holds.
func TestACallThatNamesAHeldTicketKeepsTheClaimsSlice(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if err := r.st.CreateTicketRecord("JIG-2", store.Ticket{Slug: "two"}); err != nil {
		t.Fatal(err)
	}
	r.worktreeOn(r.fx.Ticket)
	r.handle(r.ev(KindSessionStart))
	own := filepath.Join(r.fx.StoreDir, "tickets", r.fx.Ticket, "brief.md")
	other := filepath.Join(r.fx.StoreDir, "tickets", "JIG-2", "brief.md")

	expectTicket(t, "the claim", r.claimCall("a-1", r.fx.Ticket, "a"), r.fx.Ticket)
	expectTicket(t, "a read of its own brief", r.callFiles("a-1", "Read", own), r.fx.Ticket)
	expectTicket(t, "the slice of that read", r.sliceOfLast(), "a")
	expectTicket(t, "jig context", r.call("a-1", "jig context "+r.fx.Ticket+" --slice a"), r.fx.Ticket)
	expectTicket(t, "the slice of jig context", r.sliceOfLast(), "a")
	expectTicket(t, "a read of another ticket's brief", r.callFiles("a-1", "Read", other), "JIG-2")
	expectTicket(t, "its slice", r.sliceOfLast(), "")
	expectTicket(t, "jig status of another ticket", r.call("a-1", "jig status JIG-2"), "JIG-2")
	expectTicket(t, "its slice", r.sliceOfLast(), "")
	expectTicket(t, "an agent with no claim reading the brief", r.callFiles("a-2", "Read", own), r.fx.Ticket)
	expectTicket(t, "its slice", r.sliceOfLast(), "")
}

// TestAPathInACommandIsReadAsASpelledTheShellTakesIt: a quoted path binds, and
// a path under tickets/ that is no ticket of the store binds none.
func TestAPathInACommandIsReadAsASpelledTheShellTakesIt(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.handle(r.ev(KindSessionStart))
	dir := filepath.ToSlash(filepath.Join(r.fx.StoreDir, "tickets"))

	expectTicket(t, "a double-quoted path", r.call("", `cat "`+dir+`/`+r.fx.Ticket+`/brief.md"`), r.fx.Ticket)
	expectTicket(t, "a single-quoted path", r.call("", "cat '"+dir+"/"+r.fx.Ticket+"/brief.md'"), r.fx.Ticket)
	expectTicket(t, "a folder that is no ticket", r.call("", "cat "+dir+"/NOPE-9/brief.md"), "")
}
