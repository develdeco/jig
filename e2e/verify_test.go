package e2e

// End-to-end tests of `jig verify`: the commit a person or an agent made by
// hand, verified in a detached worktree by a detached worker, recorded in the
// journal as the frontier records a builder's green run, so the gate can reuse
// the pass. The harness stands for the agent session: it commits in the
// operator clone with the Jig-Slice trailer and runs jig from there.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/store"
)

// verifyWait is how long a test gives a detached verification to record its
// outcome: a go test run in a fresh worktree, on a loaded machine.
const verifyWait = 3 * time.Minute

// dropEnvFromSlices removes the env class slice d names from the store's
// slices.yaml. The gate brings up every env class a slice names and reuses an
// oracle pass only when exactly those were up for it, so with slice d naming
// rig an env-less pass of slice a would never stand for the gate's run. The
// edit is a working-tree change of the store, which the next checkpoint
// commits.
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

// TestVerifyHandCommitReusedByGate: a commit made by hand on the ticket's
// branch for slice a is verified by a detached worker (`jig verify`), which
// journals the pending line, the oracle line and the verified line; the gate
// round that follows reuses that pass for the alpha workspace's oracle instead
// of running it again, and runs beta's, which no one verified.
func TestVerifyHandCommitReusedByGate(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{ScenarioBranch: "clean-round"})
	dropEnvFromSlices(t, fx)
	h := newHarness(t, fx, env, "s-hand")
	branch := fx.Branch
	h.Branch(branch)
	sha := h.CommitScenario("a", 1)
	h.Push(branch)

	res := h.Jig("verify", fx.Ticket, sha, "--slice", "a")
	if res.Code != 0 || !strings.Contains(res.Stdout, "state: pending") || !strings.Contains(res.Stdout, "pid: ") {
		t.Fatalf("jig verify exit = %d, want 0 and a pending worker\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	oracle := waitJournal(t, fx, fx.Ticket, verifyWait, func(l journal.Line) bool {
		return l.Event == journal.EventOracle && l.Slice == "a" && l.Commit == sha
	})
	if oracle.Outcome != "pass" {
		t.Fatalf("oracle line = %+v, want a pass", oracle)
	}
	waitJournal(t, fx, fx.Ticket, verifyWait, func(l journal.Line) bool {
		return l.Event == journal.EventVerified && l.Slice == "a" && l.Commit == sha
	})
	h.WaitVerifiers()

	lines := readJournal(t, fx, fx.Ticket)
	var kinds []string
	for _, l := range lines {
		kinds = append(kinds, l.Event)
	}
	if want := []string{"verify-pending", "oracle", "verified"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("journal events = %v, want %v", kinds, want)
	}
	if oracle.Source != "jig" || oracle.Session != h.Session || oracle.Attempt != 1 || oracle.Command == "" || oracle.Seconds < 1 || oracle.Env != "" {
		t.Errorf("oracle line = %+v, want source jig, session %s, attempt 1, a command and a time", oracle, h.Session)
	}
	if got := journal.BuiltCommits(lines); !reflect.DeepEqual(got, []string{sha}) {
		t.Errorf("BuiltCommits = %v, want the verified commit %s", got, sha)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if ss, err := st.ReadSliceState(fx.Ticket, "a"); err != nil || ss.State != "green" || ss.Attempts != 1 {
		t.Errorf("slice a state = %+v, %v, want green after 1 attempt", ss, err)
	}
	if ledger := readLedger(t, env); len(ledger) != 1 || ledger[0].Event != journal.EventVerifySpawned || ledger[0].Commit != sha {
		t.Errorf("ledger = %+v, want the one verify-spawned line", ledger)
	}

	gate := runJig(t, env, fx.StoreDir, "gate", fx.Ticket, "--early")
	if gate.Code != 0 || !strings.Contains(gate.Stdout, "round: 1") {
		t.Fatalf("jig gate exit = %d, want 0 and round 1 open\nstdout:\n%s\nstderr:\n%s", gate.Code, gate.Stdout, gate.Stderr)
	}
	// review.json is what the reviewer is handed; its oracles_passed says which
	// runs the gate made and which it reused.
	var review struct {
		OraclesPassed []struct {
			Workspace  string `json:"workspace"`
			Command    string `json:"command"`
			ReusedFrom string `json:"reused_from"`
		} `json:"oracles_passed"`
	}
	data := readFileOrFatal(t, filepath.Join(st.TicketDir(fx.Ticket), "work", "gate.round-1.review.json"))
	if err := json.Unmarshal(data, &review); err != nil {
		t.Fatalf("parse review.json: %v\n%s", err, data)
	}
	reused := map[string]string{}
	commands := map[string]string{}
	for _, r := range review.OraclesPassed {
		reused[r.Workspace] = r.ReusedFrom
		commands[r.Workspace] = r.Command
	}
	if reused["alpha"] != sha || commands["alpha"] != oracle.Command {
		t.Errorf("alpha: reused_from %q with command %q, want it reused from %s with slice a's command %q", reused["alpha"], commands["alpha"], sha, oracle.Command)
	}
	if rf, ok := reused["beta"]; !ok || rf != "" {
		t.Errorf("beta: reused_from %q (present %v), want it run by the gate: no one verified it", rf, ok)
	}
}

// TestVerifyIdempotent: a second `jig verify` of a commit that has a
// verification reports the existing one and adds nothing: no journal line, no
// ledger line, no worker. The same holds while a verification is pending, and
// --rerun is the way to run it again, as the slice's next attempt.
func TestVerifyIdempotent(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-idem")
	branch := fx.Branch
	h.Branch(branch)
	sha := h.CommitScenario("a", 1)

	first := h.Jig("verify", fx.Ticket, sha, "--slice", "a", "--wait")
	if first.Code != 0 || !strings.Contains(first.Stdout, "state: pass") {
		t.Fatalf("jig verify --wait exit = %d, want 0 and a pass\nstdout:\n%s\nstderr:\n%s", first.Code, first.Stdout, first.Stderr)
	}
	journalBefore := readJournal(t, fx, fx.Ticket)
	ledgerBefore := readLedger(t, env)

	second := h.Jig("verify", fx.Ticket, sha, "--slice", "a")
	if second.Code != 0 || !strings.Contains(second.Stdout, "existing: true") || !strings.Contains(second.Stdout, "state: pass") {
		t.Fatalf("second jig verify exit = %d, want 0 and the existing pass\nstdout:\n%s\nstderr:\n%s", second.Code, second.Stdout, second.Stderr)
	}
	// The slice and the commit come from the commit's trailer and HEAD alike.
	third := h.Jig("verify")
	if third.Code != 0 || !strings.Contains(third.Stdout, "existing: true") {
		t.Fatalf("bare jig verify exit = %d, want the existing pass of HEAD\nstdout:\n%s\nstderr:\n%s", third.Code, third.Stdout, third.Stderr)
	}
	if got := readJournal(t, fx, fx.Ticket); !reflect.DeepEqual(got, journalBefore) {
		t.Errorf("the journal changed on a repeat: %d lines, were %d", len(got), len(journalBefore))
	}
	if got := readLedger(t, env); !reflect.DeepEqual(got, ledgerBefore) {
		t.Errorf("the ledger changed on a repeat: %+v", got)
	}

	// A pending record stands the same way.
	pendingSHA := h.Commit("b", "b: by hand", map[string]string{"notes/b.txt": "hand written"})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(st, fx.Ticket, journal.Line{Slice: "b", Event: journal.EventVerifyPending, Commit: pendingSHA, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	journalBefore = readJournal(t, fx, fx.Ticket)
	pending := h.Jig("verify", fx.Ticket, pendingSHA, "--slice", "b")
	if pending.Code != 0 || !strings.Contains(pending.Stdout, "existing: true") || !strings.Contains(pending.Stdout, "state: pending") {
		t.Fatalf("jig verify over a pending record exit = %d\nstdout:\n%s\nstderr:\n%s", pending.Code, pending.Stdout, pending.Stderr)
	}
	if got := readJournal(t, fx, fx.Ticket); !reflect.DeepEqual(got, journalBefore) {
		t.Errorf("the journal changed over a pending record: %d lines, were %d", len(got), len(journalBefore))
	}
	if got := readLedger(t, env); !reflect.DeepEqual(got, ledgerBefore) {
		t.Errorf("the ledger changed over a pending record: %+v", got)
	}

	rerun := h.Jig("verify", fx.Ticket, sha, "--slice", "a", "--rerun", "--wait")
	if rerun.Code != 0 || !strings.Contains(rerun.Stdout, "attempt: 2") || strings.Contains(rerun.Stdout, "existing: true") {
		t.Fatalf("jig verify --rerun --wait exit = %d, want a second attempt\nstdout:\n%s\nstderr:\n%s", rerun.Code, rerun.Stdout, rerun.Stderr)
	}
	var attempts []int
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventOracle && l.Slice == "a" {
			attempts = append(attempts, l.Attempt)
		}
	}
	if !reflect.DeepEqual(attempts, []int{1, 2}) {
		t.Errorf("oracle attempts of slice a = %v, want [1 2]", attempts)
	}
}

// TestVerifyFailPersistsSignature: slice b's first attempt, made without the
// fix slice a brings, fails its test. The detached worker journals the failed
// oracle line with the flattened tail of the output and the stall signature
// built from it, the full tail is in the worker's log, no verified line is
// written, and the slice is building.
func TestVerifyFailPersistsSignature(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-fail")
	branch := fx.Branch
	h.Branch(branch)
	sha := h.CommitScenario("b", 1)

	res := h.Jig("verify", fx.Ticket, sha, "--slice", "b")
	if res.Code != 0 || !strings.Contains(res.Stdout, "state: pending") {
		t.Fatalf("jig verify exit = %d, want 0 and a pending worker\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	line := waitJournal(t, fx, fx.Ticket, verifyWait, func(l journal.Line) bool {
		return l.Event == journal.EventOracle && l.Slice == "b" && l.Commit == sha
	})
	h.WaitVerifiers()

	if line.Outcome != "fail" || line.Attempt != 1 || line.Source != "jig" || line.Session != h.Session {
		t.Fatalf("oracle line = %+v, want a failure of attempt 1 by jig for session %s", line, h.Session)
	}
	if line.Summary == "" || len(line.Summary) > 600 || strings.ContainsAny(line.Summary, "\r\n") {
		t.Errorf("Summary = %q, want one line of at most 600 bytes", line.Summary)
	}
	if !strings.Contains(line.Summary, "FAIL") {
		t.Errorf("Summary = %q, want the end of go test's output", line.Summary)
	}
	if want := outcome.Signature("b", outcome.CodeBug, line.Summary); line.Signature != want {
		t.Errorf("Signature = %q, want %q", line.Signature, want)
	}
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventVerified {
			t.Errorf("a failed verification wrote %+v", l)
		}
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if ss, err := st.ReadSliceState(fx.Ticket, "b"); err != nil || ss.State != "building" || ss.Attempts != 1 {
		t.Errorf("slice b state = %+v, %v, want building after 1 attempt", ss, err)
	}
	logs := filepath.Join(env.home, "logs", "verify")
	var logBody string
	_ = filepath.WalkDir(logs, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".log") {
			logBody += string(readFileOrFatal(t, path))
		}
		return nil
	})
	if !strings.Contains(logBody, "the oracle failed") || !strings.Contains(logBody, "FAIL") {
		t.Errorf("the worker's log under %s lacks the oracle's output tail:\n%s", logs, logBody)
	}
}
