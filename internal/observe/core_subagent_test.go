package observe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/project"
)

// subagent makes an event the speech of the subagent a-7.
func subagent(e *Event) { e.Agent = "a-7" }

// toolLine records the tool call agent made with command in the machine
// ledger, as the harness's PostToolUse hook does.
func (r *rig) toolLine(agent, command string) {
	r.t.Helper()
	err := ledger.Append(r.fx.Home, journal.Line{
		TS: ledger.Stamp(r.clock.Now()), Event: journal.EventTool, Session: "s1", Agent: agent,
		Ticket: r.fx.Ticket, Cwd: r.fx.RepoDir, Tool: "Bash", Command: command,
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

// claimBy records a claim of slice by session s1 that agent made: the journal
// line the CLI writes, and the tool line of the call its harness wrote.
func (r *rig) claimBy(agent, slice string, unattended bool) {
	r.t.Helper()
	r.add(journal.Line{Event: journal.EventClaim, Slice: slice, Session: "s1", Unattended: unattended})
	command := "jig claim " + r.fx.Ticket + " --slice " + slice
	if unattended {
		command += " --unattended"
	}
	r.toolLine(agent, command)
}

// projectName is the name the fixture's store gives its project.
func (r *rig) projectName(t *testing.T) string {
	t.Helper()
	cfg, err := project.Load(filepath.Join(r.fx.StoreDir, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Name
}

// pending commits to slice and leaves its verification running.
func (r *rig) pending(slice, name string) string {
	r.t.Helper()
	sha := r.commit(slice, name)
	r.add(journal.Line{Event: journal.EventVerifyPending, Slice: slice, Commit: sha, Attempt: 1})
	return sha
}

// TestTheHoldFallsOnTheAgentThatClaimed: a subagent's unattended claim holds
// that subagent at its own stop and nobody else. The main session and a
// sibling (a reviewer nested in the builder, say) are told and never held; the
// claim holder is held once for each loose end: the running verification, a
// second one, uncommitted tracked files. A later interactive claim of the same
// agent only tells it.
func TestTheHoldFallsOnTheAgentThatClaimed(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.claimBy("a-7", "a", true)
	sha := r.pending("a", "a.txt")

	if main := r.handle(r.ev(KindStop)); main.Block != "" || !strings.Contains(main.Message, sha[:12]) {
		t.Errorf("main Stop = %+v, want the verification said and no block", main)
	}
	if sibling := r.handle(r.ev(KindStop, func(e *Event) { e.Agent = "a-9" })); sibling.Block != "" {
		t.Errorf("Stop of a subagent that holds no claim = %+v, want no block", sibling)
	}
	if got := r.ledgerOf(journal.EventNoteDelivered); len(got) != 0 {
		t.Fatalf("note-delivered lines = %+v, want no refusal recorded", got)
	}

	first := r.handle(r.ev(KindStop, subagent))
	if !strings.Contains(first.Block, "jig: this agent's claim is unattended, so it is held back from stopping") ||
		!strings.Contains(first.Block, "verification of "+sha[:12]+" for slice a is still running") {
		t.Fatalf("Block = %q, want the agent's reason naming the verification", first.Block)
	}
	delivered := r.ledgerOf(journal.EventNoteDelivered)
	if len(delivered) != 1 || delivered[0].Outcome != "stop-block" || delivered[0].Agent != "a-7" {
		t.Fatalf("note-delivered lines = %+v, want the refusal recorded for a-7", delivered)
	}
	again := r.handle(r.ev(KindStop, subagent, func(e *Event) { e.StopActive = true }))
	if again.Block != "" || again.Message == "" {
		t.Errorf("Stop while continuing with nothing new = %+v, want a message and no block", again)
	}
	sha2 := r.pending("b", "b.txt")
	if third := r.handle(r.ev(KindStop, subagent, func(e *Event) { e.StopActive = true })); !strings.Contains(third.Block, sha2[:12]) {
		t.Errorf("Stop while continuing with a new pending verification = %+v, want a block that names it", third)
	}
	if err := os.WriteFile(filepath.Join(r.fx.RepoDir, "go.mod"), []byte("module changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty := r.handle(r.ev(KindStop, subagent, func(e *Event) { e.StopActive = true })); !strings.Contains(dirty.Block, "1 tracked file changed and not committed: go.mod") || !strings.Contains(dirty.Block, "Commit them") {
		t.Errorf("subagent Stop with a tracked file changed = %+v, want the file named and the hold", dirty)
	}

	r.claimBy("a-7", "c", false)
	if res := r.handle(r.ev(KindStop, subagent)); res.Block != "" || !strings.Contains(res.Message, sha2[:12]) {
		t.Errorf("subagent Stop of an interactive claim = %+v, want a message and no block", res)
	}
}

// TestAClaimWhoseHolderIsUnknownHoldsTheSession: with no tool line of the
// claim's call (a harness without hooks) the whole session is the holder, as
// before; a tool line of the session's own agent makes it the holder and no
// subagent; tool lines of two agents make the holder unknown again.
func TestAClaimWhoseHolderIsUnknownHoldsTheSession(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	r.pending("a", "a.txt")
	if res := r.handle(r.ev(KindStop)); res.Block == "" {
		t.Errorf("main Stop with a claim of an unknown holder = %+v, want the session held, as before", res)
	}
	if res := r.handle(r.ev(KindStop, subagent)); res.Block == "" {
		t.Errorf("subagent Stop with a claim of an unknown holder = %+v, want it held too", res)
	}

	claim := "jig claim " + r.fx.Ticket + " --slice a --unattended"
	r.toolLine("", claim)
	if res := r.handle(r.ev(KindStop)); !strings.Contains(res.Block, "this session is unattended") {
		t.Errorf("main Stop = %+v, want the session held with the session's reason", res)
	}
	if res := r.handle(r.ev(KindStop, subagent)); res.Block != "" {
		t.Errorf("subagent Stop of a claim the session's own agent holds = %+v, want no block", res)
	}

	r.toolLine("a-7", claim)
	if res := r.handle(r.ev(KindStop)); res.Block == "" {
		t.Errorf("main Stop with two agents on the claim lines = %+v, want the session-wide rule", res)
	}
}

func TestARefusalOfASubagentDoesNotMarkTheMainSession(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	// The project defaults to unattended, so every agent of the session is held.
	marker := "jig: \"1\"\nproject: " + r.projectName(t) + "\nunattended: true\n"
	if err := os.WriteFile(filepath.Join(r.fx.RepoDir, "jig.yaml"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	sha := r.pending("a", "a.txt")
	if res := r.handle(r.ev(KindStop, subagent)); !strings.Contains(res.Block, sha[:12]) {
		t.Fatalf("subagent Stop = %+v, want a block", res)
	}
	if res := r.handle(r.ev(KindStop, subagent, func(e *Event) { e.StopActive = true })); res.Block != "" {
		t.Errorf("subagent Stop again, nothing new = %+v, want no block: once per finding", res)
	}
	// The refusal was a-7's: the main session and a sibling have not been
	// refused for it, and are held back once each.
	for _, agent := range []string{"", "a-8"} {
		res := r.handle(r.ev(KindStop, func(e *Event) { e.Agent = agent }))
		if !strings.Contains(res.Block, sha[:12]) {
			t.Errorf("Stop of agent %q = %+v, want a block of its own", agent, res)
		}
	}
}

func TestASubagentStopThatNamesNoTicketIsPlacedByItsOwnLines(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	// The clone stays on the default branch; the subagent works in a worktree
	// of its own on the ticket's branch, and its stop names the clone.
	wt := filepath.Join(r.fx.Dir, "wt-a7")
	if _, err := gitx.Run(r.fx.RepoDir, "worktree", "add", "-q", "-b", "jig/"+r.fx.Ticket, wt); err != nil {
		t.Fatal(err)
	}
	r.handle(r.ev(KindSessionStart))
	r.add(journal.Line{Event: journal.EventClaim, Slice: "a", Session: "s1", Unattended: true})
	err := ledger.Append(r.fx.Home, journal.Line{
		TS: ledger.Stamp(r.clock.Now()), Event: journal.EventTool, Session: "s1", Agent: "a-7",
		Ticket: r.fx.Ticket, Cwd: wt, Tool: "Bash", Command: "jig claim " + r.fx.Ticket + " --slice a --unattended",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "go.mod"), []byte("module changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := r.handle(r.ev(KindStop, subagent)) // Cwd is the clone, on no ticket
	if !strings.Contains(res.Block, "go.mod") {
		t.Errorf("subagent Stop naming the clone = %+v, want it placed in its worktree and held for the file there", res)
	}
	if res := r.handle(r.ev(KindStop)); !res.Empty() {
		t.Errorf("main Stop in the clone on no ticket = %+v, want nothing", res)
	}
}
