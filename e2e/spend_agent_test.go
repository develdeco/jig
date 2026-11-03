package e2e

// End-to-end tests of a subagent's spend: the transcript of a subagent is
// summed into a spend line of its own, on the ticket the subagent works on,
// and the main session's line leaves it out once it has one. The fake harness
// plays the main agent on one ticket and a subagent in a linked worktree on
// another, as a chart run's build does.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// agentTranscript is the transcript of agent, which Claude Code keeps beside
// the main session's: <main transcript without .jsonl>/subagents/agent-<id>.jsonl.
func agentTranscript(h *fakeHarness, agent string) string {
	return filepath.Join(strings.TrimSuffix(h.Transcript, ".jsonl"), "subagents", "agent-"+agent+".jsonl")
}

// addAgentUsage appends one assistant message of input tokens to the
// transcript of agent, making the directory it lives in first.
func addAgentUsage(t *testing.T, h *fakeHarness, agent, id string, input int64) {
	t.Helper()
	path := agentTranscript(h, agent)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	addUsage(t, path, h.Session, id, input)
}

// newAgentPair makes the two sides of a subagent's run: the main harness of
// session on mainBranch in the fixture clone, and a harness for the subagent
// agent of the same session, working in a linked worktree of the clone on
// agentBranch. Both name the one transcript the main session keeps, as the
// payload of a subagent does, and a branch that leads with a ticket id places
// its directory at that ticket.
func newAgentPair(t *testing.T, fx *fixture.Fixture, env jigEnv, session, agent, mainBranch, agentBranch string) (main, sub *fakeHarness) {
	t.Helper()
	main = newHarness(t, fx, env, session)
	main.Branch(mainBranch)
	main.Transcript = filepath.Join(t.TempDir(), session+".jsonl")

	wt := filepath.Join(t.TempDir(), "wt-"+agent)
	gitLog(t, fx.RepoDir, "worktree", "add", "-b", agentBranch, wt)
	sub = newHarness(t, fx, env, session)
	sub.Clone = wt
	sub.Agent = agent
	sub.Transcript = main.Transcript
	return main, sub
}

// spendLinesOn are the spend lines of the journal of ticket, in order.
func spendLinesOn(t *testing.T, fx *fixture.Fixture, ticket string) []journal.Line {
	t.Helper()
	var out []journal.Line
	for _, l := range readJournal(t, fx, ticket) {
		if l.Event == journal.EventSpend {
			out = append(out, l)
		}
	}
	return out
}

// TestSubagentSpendIsItsOwnTickets: the main session works on ticket A and a
// subagent of it on ticket B. The subagent's stop puts its own transcript on
// B as a line of its own, and the main session's next line no longer counts
// it: A holds the main agent's tokens and B the subagent's, and `jig status`
// of each says so. Until the subagent has stopped, its tokens are in the
// session's line, so none is lost.
func TestSubagentSpendIsItsOwnTickets(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	A := fx.Ticket
	B := mintTicket(t, env, fx.StoreDir, "The second ticket", "")
	main, sub := newAgentPair(t, fx, env, "s-run", "a-1", fx.Branch, B+"-second")

	addUsage(t, main.Transcript, "s-run", "m-main-1", 1000)
	addAgentUsage(t, sub, "a-1", "m-agent-1", 300)

	if r := main.Hook("SessionStart", map[string]any{"source": "startup"}); r.Code != 0 {
		t.Fatalf("SessionStart: exit %d\n%s", r.Code, r.Stderr)
	}
	if r := main.Hook("Stop", map[string]any{"stop_hook_active": false}); r.Code != 0 {
		t.Fatalf("Stop: exit %d\n%s", r.Code, r.Stderr)
	}
	// The subagent has not stopped: its 300 tokens are the session's.
	lines := spendLinesOn(t, fx, A)
	if len(lines) != 1 || lines[0].Session != "s-run" || lines[0].Agent != "" || lines[0].Spend == nil || lines[0].Spend.Input != 1300 {
		t.Fatalf("spend lines of %s = %+v, want one, the session's, at 1300 (the agent has not stopped)", A, lines)
	}
	if n := len(spendLinesOn(t, fx, B)); n != 0 {
		t.Fatalf("%d spend lines on %s before the subagent stopped, want none", n, B)
	}

	// The subagent stops in its worktree on B: a line of its own there.
	if r := sub.Hook("SubagentStop", map[string]any{"stop_hook_active": false}); r.Code != 0 {
		t.Fatalf("SubagentStop: exit %d\n%s", r.Code, r.Stderr)
	}
	onB := spendLinesOn(t, fx, B)
	if len(onB) != 1 || onB[0].Session != "s-run" || onB[0].Agent != "a-1" || onB[0].Harness != "claude" || onB[0].Ticket != B ||
		onB[0].Spend == nil || onB[0].Spend.Input != 300 {
		t.Fatalf("spend lines of %s = %+v, want one: session s-run, agent a-1, 300 tokens (its transcript alone)", B, onB)
	}
	if n := len(spendLinesOn(t, fx, A)); n != 1 {
		t.Fatalf("%d spend lines on %s after the subagent stopped, want still 1: its stop writes on its own ticket", n, A)
	}

	// The main session's next line takes the agent out.
	if r := main.Hook("Stop", map[string]any{"stop_hook_active": false}); r.Code != 0 {
		t.Fatalf("Stop: exit %d\n%s", r.Code, r.Stderr)
	}
	onA := spendLinesOn(t, fx, A)
	if len(onA) != 2 || onA[1].Agent != "" || onA[1].Spend == nil || onA[1].Spend.Input != 1000 {
		t.Fatalf("spend lines of %s = %+v, want a second, the session's, at 1000 without the agent", A, onA)
	}

	// The agent spends more and stops again; the session has nothing new.
	addAgentUsage(t, sub, "a-1", "m-agent-2", 200)
	if r := sub.Hook("SubagentStop", map[string]any{"stop_hook_active": false}); r.Code != 0 {
		t.Fatalf("second SubagentStop: exit %d\n%s", r.Code, r.Stderr)
	}
	if r := main.Hook("Stop", map[string]any{"stop_hook_active": false}); r.Code != 0 {
		t.Fatalf("Stop: exit %d\n%s", r.Code, r.Stderr)
	}
	if onB = spendLinesOn(t, fx, B); len(onB) != 2 || onB[1].Agent != "a-1" || onB[1].Spend == nil || onB[1].Spend.Input != 500 {
		t.Fatalf("spend lines of %s = %+v, want a second, cumulative, at 500", B, onB)
	}
	if n := len(spendLinesOn(t, fx, A)); n != 2 {
		t.Fatalf("%d spend lines on %s, want 2: the session's third Stop had nothing new", n, A)
	}

	// The ticket's status reads the latest line of each key.
	stB := runJig(t, env, fx.RepoDir, "status", B)
	if want := "spend:\n  sessions: 1\n  agents: 1\n  tokens: 500\n"; !strings.Contains(stB.Stdout, want) {
		t.Errorf("status of %s lacks %q:\n%s", B, want, stB.Stdout)
	}
	stA := runJig(t, env, fx.RepoDir, "status", A)
	if want := "spend:\n  sessions: 1\n  tokens: 1000\n"; !strings.Contains(stA.Stdout, want) || strings.Contains(stA.Stdout, "agents:") {
		t.Errorf("status of %s lacks %q or shows an agents pair:\n%s", A, want, stA.Stdout)
	}
}
