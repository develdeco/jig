package e2e

// Helpers that the e2e tests of other groups still use, kept from the files of
// tests whose rules are now decided at narrower seams.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// approve records an approval of the brief of JIG-2 as it is on disk.
func approve(t *testing.T, st *store.Store) {
	t.Helper()
	sum, err := st.BriefSHA256("JIG-2")
	if err != nil {
		t.Fatal(err)
	}
	a := store.Approval{By: "Test Person", At: time.Now().UTC().Format(time.RFC3339), BriefSHA256: sum, Provenance: "person"}
	if err := st.WriteApproval("JIG-2", a); err != nil {
		t.Fatal(err)
	}
}

// verifyWait is how long a test gives a verification to record its outcome: a
// go test run in a fresh worktree, on a loaded machine.
const verifyWait = 3 * time.Minute

// dropEnvFromSlices removes the env class slice d names from the store's
// slices.yaml. A verify, or a final run, of a slice naming the class brings
// it up around the command; these journeys are not about that, so slice d
// names none and no run starts the fixture's env tool. The edit is a
// working-tree change of the store, which the next checkpoint commits.
func dropEnvFromSlices(t *testing.T, fx *fixture.Fixture) {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	path := filepath.Join(st.TicketDir(fx.Ticket), "slices.yaml")
	body := string(readFileOrFatal(t, path))
	dropped := strings.Replace(body, "    env: rig\n", "", 1)
	if dropped == body {
		t.Fatalf("test setup: %s names no env class to drop:\n%s", path, body)
	}
	writeFileOrFatal(t, path, []byte(dropped))
}

// oracleLine returns the journal's oracle line for slice at sha with the given
// outcome, and whether there is one.
func oracleLine(lines []journal.Line, slice, sha, outcome string) (journal.Line, bool) {
	for _, l := range lines {
		if l.Event == journal.EventOracle && l.Slice == slice && l.Commit == sha && l.Outcome == outcome {
			return l, true
		}
	}
	return journal.Line{}, false
}

// addUsage appends one assistant message of input tokens to a transcript.
func addUsage(t *testing.T, path, session, id string, input int64) {
	t.Helper()
	line := fmt.Sprintf(`{"type":"assistant","sessionId":%q,"message":{"id":%q,"role":"assistant","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":%d,"output_tokens":0}}}`+"\n", session, id, input)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

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
