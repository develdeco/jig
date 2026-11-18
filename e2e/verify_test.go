package e2e

// End-to-end tests of `jig verify`: the commit a person or an agent made by
// hand, verified by the run of `jig verify` the builder makes itself, recorded
// in the journal as the frontier records a builder's green run, so the gate
// can reuse the pass. The harness stands for the agent session: it commits in
// the operator clone with the Jig-Slice trailer and runs jig from there.

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/store"
	"gopkg.in/yaml.v3"
)

// verifyWait is how long a test gives a verification to record its outcome: a
// go test run in a fresh worktree, on a loaded machine.
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
// branch for slice a is verified by the builder's own `jig verify`, which
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

	res := h.Verify(fx.Ticket, "a")
	if res.Code != 0 || !strings.Contains(res.Stdout, "state: pass") {
		t.Fatalf("jig verify exit = %d, want 0 and a pass\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	oracle, ok := oracleLine(readJournal(t, fx, fx.Ticket), "a", sha, "pass")
	if !ok {
		t.Fatalf("no oracle pass line for %s", sha)
	}

	lines := readJournal(t, fx, fx.Ticket)
	var kinds []string
	for _, l := range lines {
		kinds = append(kinds, l.Event)
	}
	if want := []string{"oracle", "verified"}; !reflect.DeepEqual(kinds, want) {
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
	for _, l := range readLedger(t, env) {
		if l.Event == journal.EventVerifySpawned {
			t.Errorf("ledger holds %+v: a foreground verify starts no worker", l)
		}
	}

	gate := runJig(t, env, fx.StoreDir, "gate", fx.Ticket, "--early")
	if gate.Code != 0 || !strings.Contains(gate.Stdout, "round: 1") {
		t.Fatalf("jig gate exit = %d, want 0 and round 1 open\nstdout:\n%s\nstderr:\n%s", gate.Code, gate.Stdout, gate.Stderr)
	}
	// review.json is what the reviewer is handed; its oracles_passed lists the
	// passes jig witnessed for the slices, since the gate runs nothing.
	var review struct {
		OraclesPassed []struct {
			Slice     string `json:"slice"`
			Workspace string `json:"workspace"`
			Command   string `json:"command"`
			Commit    string `json:"commit"`
			AtHead    string `json:"at_head"`
		} `json:"oracles_passed"`
	}
	data := readFileOrFatal(t, filepath.Join(st.TicketDir(fx.Ticket), "work", "gate.round-1.review.json"))
	if err := json.Unmarshal(data, &review); err != nil {
		t.Fatalf("parse review.json: %v\n%s", err, data)
	}
	if len(review.OraclesPassed) != 1 {
		t.Fatalf("oracles_passed = %+v, want one row: slice a's witnessed pass", review.OraclesPassed)
	}
	if r := review.OraclesPassed[0]; r.Slice != "a" || r.Workspace != "alpha" || r.Commit != sha || r.Command != oracle.Command || r.AtHead != "yes" {
		t.Errorf("oracles_passed row = %+v, want slice a's pass in alpha at %s with command %q, at the head", r, sha, oracle.Command)
	}
}

// TestVerifyRunsAgainEveryTime: a second `jig verify` of a commit that has a
// verification runs the oracle again, as the slice's next attempt: nothing
// stands in its way, no existing answer, no lock, no pending line, and no
// worker or ledger line is added by a foreground run.
func TestVerifyRunsAgainEveryTime(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-again")
	branch := fx.Branch
	h.Branch(branch)
	sha := h.CommitScenario("a", 1)
	ledgerBefore := readLedger(t, env)

	first := h.Verify(fx.Ticket, "a")
	if first.Code != 0 || !strings.Contains(first.Stdout, "state: pass") || !strings.Contains(first.Stdout, "attempt: 1") {
		t.Fatalf("jig verify exit = %d, want 0 and a first-attempt pass\nstdout:\n%s\nstderr:\n%s", first.Code, first.Stdout, first.Stderr)
	}
	second := h.Verify(fx.Ticket, "a")
	if second.Code != 0 || !strings.Contains(second.Stdout, "attempt: 2") || strings.Contains(second.Stdout, "existing") {
		t.Fatalf("second jig verify exit = %d, want a second attempt run again\nstdout:\n%s\nstderr:\n%s", second.Code, second.Stdout, second.Stderr)
	}
	// The slice and the commit come from the commit's trailer and HEAD alike.
	third := h.Jig("verify")
	if third.Code != 0 || !strings.Contains(third.Stdout, "attempt: 3") || !strings.Contains(third.Stdout, "commit: "+sha) {
		t.Fatalf("bare jig verify exit = %d, want a third attempt at HEAD\nstdout:\n%s\nstderr:\n%s", third.Code, third.Stdout, third.Stderr)
	}
	var attempts []int
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventOracle && l.Slice == "a" {
			attempts = append(attempts, l.Attempt)
		}
	}
	if !reflect.DeepEqual(attempts, []int{1, 2, 3}) {
		t.Errorf("oracle attempts of slice a = %v, want [1 2 3]", attempts)
	}
	if got := readLedger(t, env); !reflect.DeepEqual(got, ledgerBefore) {
		t.Errorf("the ledger changed: %+v, was %+v", got, ledgerBefore)
	}
}

// TestVerifyFailPersistsSignature: slice b's first attempt, made without the
// fix slice a brings, fails its test. `jig verify` exits 1, journals the
// failed oracle line with the flattened tail of the output and the stall
// signature built from it, the output reached the stream, no verified
// line is written, and the slice is building.
func TestVerifyFailPersistsSignature(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	h := newHarness(t, fx, env, "s-fail")
	branch := fx.Branch
	h.Branch(branch)
	sha := h.CommitScenario("b", 1)

	res := h.Verify(fx.Ticket, "b")
	if res.Code != 1 || !strings.Contains(res.Stdout, "state: fail") {
		t.Fatalf("jig verify exit = %d, want 1 and a failure\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	line, ok := oracleLine(readJournal(t, fx, fx.Ticket), "b", sha, "fail")
	if !ok {
		t.Fatalf("no failed oracle line for %s", sha)
	}

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
	// The output tail is on the stream the command wrote it to, with the
	// report after it.
	if !strings.Contains(res.Stdout, "FAIL") {
		t.Errorf("the output of the run lacks the oracle's output tail:\n%s", res.Stdout)
	}
}

// setSliceOracle sets slice's oracle in the store's slices.yaml to cmd, a
// literal command, as dropEnvFromSlices edits it: a working-tree change of
// the store, which the next checkpoint commits.
func setSliceOracle(t *testing.T, fx *fixture.Fixture, slice, cmd string) {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	path := filepath.Join(st.TicketDir(fx.Ticket), "slices.yaml")
	body := string(readFileOrFatal(t, path))
	quoted, err := yaml.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}
	head := "  - id: " + slice + "\n"
	i := strings.Index(body, head)
	j := strings.Index(body[max(i, 0):], "    oracle: test\n")
	if i < 0 || j < 0 {
		t.Fatalf("test setup: %s has no slice %s naming the test oracle:\n%s", path, slice, body)
	}
	at := i + j
	body = body[:at] + "    oracle: " + strings.TrimSpace(string(quoted)) + "\n" + body[at+len("    oracle: test\n"):]
	writeFileOrFatal(t, path, []byte(body))
}

// TestVerifyRunsAnOracleWrittenForSh: an oracle is an sh command on every OS.
// Slice a's oracle here is the fixture's go test for alpha with a -run
// pattern in single quotes, whose | cmd.exe would read as a pipe. On Windows
// as elsewhere, `jig verify` fails it on a commit without the Clamp fix by
// the test the pattern's second alternative names, passes it on the fix, and
// records it as written.
func TestVerifyRunsAnOracleWrittenForSh(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	m, err := manifest.Resolve(fx.RepoDir)
	if err != nil {
		t.Fatalf("resolve the fixture's manifest: %v", err)
	}
	oracle := m.OracleCmd("test", manifest.Workspace{Path: "alpha"}) + " -run 'TestAdd|TestClamp'"
	setSliceOracle(t, fx, "a", oracle)
	h := newHarness(t, fx, env, "s-sh")
	h.Branch(fx.Branch)

	unfixed := h.Commit("a", "a: a note, not the fix", map[string]string{"notes/a.txt": "not yet"})
	res := h.Verify(fx.Ticket, "a")
	if res.Code != 1 || !strings.Contains(res.Stdout, "state: fail") {
		t.Fatalf("jig verify of the unfixed commit exit = %d, want 1 and a failure\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	fixed := h.CommitScenario("a", 1)
	res = h.Verify(fx.Ticket, "a")
	if res.Code != 0 || !strings.Contains(res.Stdout, "state: pass") {
		t.Fatalf("jig verify of the fix exit = %d, want 0 and a pass\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}

	lines := readJournal(t, fx, fx.Ticket)
	failed, ok := oracleLine(lines, "a", unfixed, "fail")
	if !ok || failed.Command != oracle || !strings.Contains(failed.Summary, "--- FAIL: TestClamp") {
		t.Errorf("the unfixed commit's oracle line = %+v (found %v), want a failure of %q by TestClamp", failed, ok, oracle)
	}
	if passed, ok := oracleLine(lines, "a", fixed, "pass"); !ok || passed.Command != oracle {
		t.Errorf("the fix's oracle line = %+v (found %v), want a pass of %q", passed, ok, oracle)
	}
}
