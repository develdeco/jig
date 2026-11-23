package observe

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// TestAReviewerBindsToItsRoundFromContextToDone: a reviewer subagent in a
// worktree on no ticket's branch (a detached one) holds the round it opened
// with `jig review context T`, until its `jig review done T`.
func TestAReviewerBindsToItsRoundFromContextToDone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.handle(r.ev(KindSessionStart))

	expectTicket(t, "a call before the round", r.call("a-r", "git log"), "")
	expectTicket(t, "the context call", r.call("a-r", "jig review context "+r.fx.Ticket+" --model m"), r.fx.Ticket)
	expectTicket(t, "a call in the round", r.call("a-r", "git diff main"), r.fx.Ticket)
	expectTicket(t, "a finding", r.call("a-r", "jig finding --file a.go --title t"), r.fx.Ticket)
	expectTicket(t, "the done call", r.call("a-r", "jig review done "+r.fx.Ticket), r.fx.Ticket)
	expectTicket(t, "a call after the round", r.call("a-r", "ls"), "")
	expectTicket(t, "the builder, who opened no round", r.call("a-b", "git diff main"), "")
}

// TestAPlannerBindsToItsBriefFromTicketNewToApproval: a session that mints a
// ticket is tied to it by the touch line the verb writes, and its calls are
// the ticket's until the brief is approved.
func TestAPlannerBindsToItsBriefFromTicketNewToApproval(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.handle(r.ev(KindSessionStart))

	expectTicket(t, "a call before the ticket", r.call("", "go test ./..."), "")
	expectTicket(t, "the mint call, which names no ticket", r.call("", `jig ticket new --title "Cart totals"`), "")
	if err := ledger.TouchSession(r.fx.Home, r.clock.Now(), ledger.Registration{ID: "s1", Ticket: r.fx.Ticket}); err != nil {
		t.Fatal(err)
	}
	expectTicket(t, "a call drafting the brief", r.call("", "jig skill intake"), r.fx.Ticket)
	expectTicket(t, "another", r.call("", "cat brief.md"), r.fx.Ticket)
	r.addTo(r.fx.Ticket, journal.Line{Event: journal.EventApproval, Session: "s1"})
	expectTicket(t, "a call after the approval", r.call("", "git log"), "")
}

// TestACallBindsToTheTicketAJigVerbNames: `jig <verb> T` is on T whoever runs
// it from wherever, as a claim call is; a command that names no ticket of the
// store is not.
func TestACallBindsToTheTicketAJigVerbNames(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.handle(r.ev(KindSessionStart))

	expectTicket(t, "publish", r.call("", "jig publish "+r.fx.Ticket+" --yes"), r.fx.Ticket)
	expectTicket(t, "status after a flag", r.call("a-1", "jig status --json "+r.fx.Ticket), r.fx.Ticket)
	expectTicket(t, "a word that is no ticket", r.call("a-1", "jig status NOPE-9"), "")
	expectTicket(t, "a command that is not jig's", r.call("a-1", "echo "+r.fx.Ticket), "")
	expectTicket(t, "the push after", r.call("", "git push origin main"), "")
}
