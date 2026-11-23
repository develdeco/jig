package observe

import (
	"testing"
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
