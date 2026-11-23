package observe

import (
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/store"
)

// worktreeOn adds a ticket to the store, with a worktree of the clone on the
// branch that binds it, and returns the worktree's directory. The clone stays
// on its default branch, on no ticket.
func (r *rig) worktreeOn(id string) string {
	r.t.Helper()
	if id != r.fx.Ticket {
		if err := r.st.CreateTicketRecord(id, store.Ticket{Slug: "t-" + id}); err != nil {
			r.t.Fatal(err)
		}
	}
	dir := filepath.Join(r.fx.Dir, "wt-"+id)
	if _, err := gitx.Run(r.fx.RepoDir, "worktree", "add", "-q", "-b", "jig/"+id, dir); err != nil {
		r.t.Fatal(err)
	}
	return dir
}

// call is a tool call of agent made with command from the session's
// directory, or from cwd when one is given; it returns the ticket its ledger
// line was written on.
func (r *rig) call(agent, command string, cwd ...string) string {
	r.t.Helper()
	r.handle(r.ev(KindTool, func(e *Event) {
		e.Agent, e.Tool, e.Command = agent, "Bash", command
		if len(cwd) > 0 {
			e.Cwd = cwd[0]
		}
	}))
	lines := r.ledgerOf(journal.EventTool)
	last := lines[len(lines)-1]
	if last.Command != command || last.Agent != agent {
		r.t.Fatalf("the last tool line is %+v, want the call %q of agent %q", last, command, agent)
	}
	return last.Ticket
}

// claimCall is what a claim leaves: the journal line the CLI wrote, then the
// call the hook heard, whose line is what the call is bound by.
func (r *rig) claimCall(agent, ticket, slice string) string {
	r.t.Helper()
	r.addTo(ticket, journal.Line{Event: journal.EventClaim, Slice: slice, Session: "s1"})
	return r.call(agent, "jig claim "+ticket+" --slice "+slice+" --unattended")
}

// releaseCall is the release of a slice: the journal line, then the call.
func (r *rig) releaseCall(agent, ticket, slice string) string {
	r.t.Helper()
	r.addTo(ticket, journal.Line{Event: journal.EventRelease, Slice: slice, Session: "s1"})
	return r.call(agent, "jig claim "+ticket+" --slice "+slice+" --release")
}

// addTo appends a line to the journal of ticket.
func (r *rig) addTo(ticket string, l journal.Line) {
	r.t.Helper()
	l.TS = ledger.Stamp(r.clock.Now())
	if err := journal.Append(r.st, ticket, l); err != nil {
		r.t.Fatal(err)
	}
}

func expectTicket(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: the line is on ticket %q, want %q", what, got, want)
	}
}

// TestToolLineBindsToItsAgentsClaim: a call is on the ticket whose slice its
// own agent holds, from the session's directory, which is on no ticket: the
// claim's own call, the calls after it, and a sibling's on a claim of its own.
// A call before the agent's first claim is on none.
func TestToolLineBindsToItsAgentsClaim(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.worktreeOn("JIG-2")
	r.handle(r.ev(KindSessionStart))

	expectTicket(t, "a call before the first claim", r.call("a-1", "jig here"), "")
	expectTicket(t, "the claim's own call", r.claimCall("a-1", r.fx.Ticket, "a"), r.fx.Ticket)
	expectTicket(t, "a call after the claim", r.call("a-1", "go test ./..."), r.fx.Ticket)
	expectTicket(t, "a sibling's claim call", r.claimCall("a-2", "JIG-2", "a"), "JIG-2")
	expectTicket(t, "the sibling's call", r.call("a-2", "git status"), "JIG-2")
	expectTicket(t, "the first agent's call again", r.call("a-1", "git diff"), r.fx.Ticket)
	expectTicket(t, "the session's own call, which claimed nothing", r.call("", "ls"), "")
	expectTicket(t, "an agent that claimed nothing", r.call("a-3", "ls"), "")
}

// TestToolLineBindsToTheWorktreeItRanIn: with no claim a call is on the ticket
// of the worktree it ran in: the directory a leading cd names, else the
// event's directory, through the branch there. A task's worktree is the task,
// an issue's branch the issue, and a cd that cannot be bound is none.
func TestToolLineBindsToTheWorktreeItRanIn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	task := r.worktreeOn(r.fx.Ticket)
	issue := r.worktreeOn("JIG-2")
	r.handle(r.ev(KindSessionStart))

	expectTicket(t, "cd into a task's worktree", r.call("a-1", "cd "+filepath.ToSlash(task)+" && go test ./..."), r.fx.Ticket)
	expectTicket(t, "cd into an issue's branch", r.call("a-1", `cd "`+filepath.ToSlash(issue)+`" && jig verify JIG-2 --final`), "JIG-2")
	expectTicket(t, "the event's directory", r.call("a-1", "go vet ./...", task), r.fx.Ticket)
	expectTicket(t, "the event's directory of the session", r.call("", "git status", issue), "JIG-2")
	expectTicket(t, "a relative cd", r.call("a-1", "cd ../wt-JIG-2 && ls", task), "JIG-2")
	expectTicket(t, "a cd through a variable", r.call("a-1", "cd $WT && ls", task), "")
	expectTicket(t, "a cd to a directory that is not there", r.call("a-1", "cd "+filepath.ToSlash(filepath.Join(r.fx.Dir, "gone"))+" && ls", task), "")
	expectTicket(t, "a cd into the clone, on no ticket", r.call("a-1", "cd "+filepath.ToSlash(r.fx.RepoDir)+" && ls", task), "")
	expectTicket(t, "the clone", r.call("a-1", "ls"), "")
}

// TestToolLineAfterTheClaimEndsBindsToNone: a claim binds while it is held. A
// released slice, and a subagent's stop, end it; the main session's calls in
// the clone after its work is done are on none.
func TestToolLineAfterTheClaimEndsBindsToNone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.worktreeOn("JIG-2")
	r.handle(r.ev(KindSessionStart))

	expectTicket(t, "the main session's claim", r.claimCall("", r.fx.Ticket, "a"), r.fx.Ticket)
	expectTicket(t, "its call while held", r.call("", "go build ./..."), r.fx.Ticket)
	expectTicket(t, "its release", r.releaseCall("", r.fx.Ticket, "a"), r.fx.Ticket)
	expectTicket(t, "its call after the release", r.call("", "git log"), "")

	expectTicket(t, "a subagent's claim", r.claimCall("a-1", "JIG-2", "a"), "JIG-2")
	expectTicket(t, "its call while held", r.call("a-1", "go build ./..."), "JIG-2")
	r.handle(r.ev(KindStop, func(e *Event) { e.Agent = "a-1" }))
	expectTicket(t, "its call after its stop, the claim never released", r.call("a-1", "go vet ./..."), "")
}

// TestSubagentStopBoundByItsClaimIsPlaced: a subagent that worked a ticket
// from the session's directory is placed on it at its stop, again at a second
// stop; one that claimed nothing is placed by the directory, on none here.
func TestSubagentStopBoundByItsClaimIsPlaced(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.worktreeOn(r.fx.Ticket)
	r.handle(r.ev(KindSessionStart))
	r.claimCall("a-1", r.fx.Ticket, "a")
	r.call("a-1", "go test ./...")

	for i := 0; i < 2; i++ {
		r.handle(r.ev(KindStop, func(e *Event) { e.Agent = "a-1" }))
	}
	r.handle(r.ev(KindStop, func(e *Event) { e.Agent = "a-2" }))

	stops := r.hooksOf(KindStop)
	if len(stops) != 3 {
		t.Fatalf("stop hook lines = %+v, want three", stops)
	}
	for i, want := range []string{r.fx.Ticket, r.fx.Ticket, ""} {
		if stops[i].Ticket != want {
			t.Errorf("stop %d (agent %s) was placed on %q, want %q", i, stops[i].Agent, stops[i].Ticket, want)
		}
	}
}
