package observe

import (
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestTheBindingReadsTheFormsACommandTakes: a claim that names no ticket takes
// the one of the branch it ran on, a claim named by an alias or after other
// flags binds to the ticket, and the spellings of a leading cd.
func TestTheBindingReadsTheFormsACommandTakes(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	wt := r.worktreeOn(r.fx.Ticket)
	r.handle(r.ev(KindSessionStart))
	slash := filepath.ToSlash(wt)

	r.addTo(r.fx.Ticket, journal.Line{Event: journal.EventClaim, Slice: "a", Session: "s1"})
	expectTicket(t, "a claim that names no ticket, run on its branch", r.call("a-1", "jig claim --slice a", wt), r.fx.Ticket)
	expectTicket(t, "a call of another agent on a quoted cd", r.call("a-2", `cd "`+slash+`" && ls`), r.fx.Ticket)
	expectTicket(t, "a single-quoted cd", r.call("a-2", "cd '"+slash+"' && ls"), r.fx.Ticket)
	expectTicket(t, "a cd with no &&, which moves no later call", r.call("a-2", "cd "+slash), "")
	expectTicket(t, "a cd in the middle of the line", r.call("a-2", "ls && cd "+slash+" && ls"), "")
	expectTicket(t, "a cd with a subshell", r.call("a-2", "cd $(pwd) && ls"), "")
	expectTicket(t, "a cd to home", r.call("a-2", "cd ~ && ls"), "")
}
