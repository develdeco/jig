package verifydeliver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/frontier"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/staircase"
	"github.com/develdeco/jig/internal/store"
)

// buildGitEnv pins the identity/date used for every commit these test
// helpers make directly in a pool lease, mirroring the fake session
// backend so shas stay comparable.
var buildGitEnv = []string{
	"GIT_AUTHOR_NAME=jig-fixture",
	"GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=jig-fixture",
	"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

// testRungs is a synthetic, deterministic staircase used by every test in
// this package, cheap to dear.
func testRungs() staircase.Config {
	return staircase.Config{Rungs: []string{"rung-a", "rung-b", "rung-c"}}
}

// ticketBranch is the default branch name a ticket with no recorded branch
// resolves to (store.Store.TicketBranch's own fallback). Tests use this
// literal directly wherever they only need to name that conventional
// branch - to set up a lease, or to assert against it - without threading
// a *store.Store through every call site for a value most of these tests
// never record differently.
func ticketBranch(ticket string) string { return "jig/" + ticket }

// recordBranch records branch as ticket's working branch in st's
// ticket.yaml and commits and pushes it, the way an adoption does (without the
// review or the start sha), so the next Gate, Publish or buildLeaseDir
// resolves it instead of the "jig/<ticket>" default.
func recordBranch(t *testing.T, st *store.Store, ticket, branch string) {
	t.Helper()
	if err := st.WriteTicketBranch(ticket, branch); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}
	if err := st.Push(ticket + ": record branch"); err != nil {
		t.Fatalf("push the recorded branch: %v", err)
	}
}

// wantAxiCode fails the test unless err is, or wraps, an *axi.Error with the
// given code: the command under test must surface a refusal Store.TicketBranch
// returned as its own failure, code included, not swallow it.
func wantAxiCode(t *testing.T, err error, code string) {
	t.Helper()
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %v, want an *axi.Error %s", err, code)
	}
}

// wantTicketBranchInvalid is wantAxiCode for the refusal of a recorded branch.
func wantTicketBranchInvalid(t *testing.T, err error) {
	t.Helper()
	wantAxiCode(t, err, "TICKET_BRANCH_INVALID")
}

// originRef returns the sha refname points at in the bare remote, or "" when
// the remote has no such ref: a test compares it before and after a command
// to see that the command pushed nothing there.
func originRef(t *testing.T, remote, refname string) string {
	t.Helper()
	sha, err := gitx.Run(remote, "for-each-ref", "--format=%(objectname)", refname)
	if err != nil {
		t.Fatalf("resolve %s on origin: %v", refname, err)
	}
	return sha
}

// newDeps opens fx's store and loads its project.yaml into a Deps ready
// for Gate/Publish.
func newDeps(t *testing.T, fx *fixture.Fixture) Deps {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	cfg, err := project.Load(filepath.Join(fx.StoreDir, "project.yaml"))
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	return Deps{Store: st, Cfg: cfg, Rungs: testRungs(), Home: fx.Home, Oracle: passingOracle}
}

// passingOracle stands in for the gate's own oracle runs in tests that are
// not about them: every run passes at once. A test about the gate's oracle
// run (what it runs, a red run, what it runs after) sets Deps.Oracle to nil
// for the real one.
func passingOracle(string, string) (string, error) { return "", nil }

// oracleRecorder stands in for the gate's oracle runs like passingOracle,
// and records each command it was asked to run, in order, so a test can say
// which runs the gate made without paying for them.
type oracleRecorder struct {
	mu   sync.Mutex
	cmds []string
}

// run is the recorder's Deps.Oracle.
func (r *oracleRecorder) run(cmd, _ string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, cmd)
	return "", nil
}

// ran returns the commands run so far, in order.
func (r *oracleRecorder) ran() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.cmds...)
}

// buildLeaseDir returns the build lease directory frontier would use for the
// fixture's ticket: <pool>/fixture-repo/<ticket>, checked out on the branch
// frontier would resolve for it, so a test that records a branch in the
// fixture's ticket.yaml before driving the build gets a build lease on that
// branch, and one that records none gets the "jig/<ticket>" default.
func buildLeaseDir(t *testing.T, fx *fixture.Fixture) string {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	branch, err := st.TicketBranch(fx.Ticket, "main")
	if err != nil {
		t.Fatalf("resolve %s's branch: %v", fx.Ticket, err)
	}
	lease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Build)
	if err != nil {
		t.Fatalf("pool.Acquire build lease: %v", err)
	}
	return lease.Dir
}

// marshalReviewResult marshals result as result.json's bytes, filling a nil
// Findings, StillPresent or ReviewedPaths with [] first: ParseReviewResult
// rejects a JSON null for any of the three (a genuinely absent key is the
// only thing "not written" means), so a scripted reviewer double that
// builds a ReviewResult as a struct literal - where a nil slice with no
// omitempty tag would otherwise marshal as null - has to write the empty
// list out explicitly, the same way MarshalReviewRequest does for
// ReviewRequest's own nil slices.
func marshalReviewResult(t *testing.T, result ReviewResult) []byte {
	t.Helper()
	if result.Findings == nil {
		result.Findings = []ResultFinding{}
	}
	if result.StillPresent == nil {
		result.StillPresent = []StillPresentEntry{}
	}
	if result.ReviewedPaths == nil {
		result.ReviewedPaths = []string{}
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal ReviewResult: %v", err)
	}
	return data
}

// readReviewRequest reads and parses one round's review.json (the stub
// backend's own sd.SliceJSON), so a scripted reviewer double can answer
// ReviewedPaths with req.MustReview - the minimal valid response
// ParseReviewResult accepts - without hand-naming every path itself.
func readReviewRequest(t *testing.T, path string) ReviewRequest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read review.json: %v", err)
	}
	var req ReviewRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("parse review.json: %v", err)
	}
	return req
}

// scenarioResult reads and parses a scenario attempt's result.json.
func scenarioResult(t *testing.T, fx *fixture.Fixture, slice string, attempt int) map[string]any {
	t.Helper()
	path := filepath.Join(fx.ScenarioDir, "slices", slice, fmt.Sprintf("attempt-%d", attempt), "result.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

// applyScenarioPatch applies and commits a scenario attempt's patch.diff
// in dir, if one exists and is non-empty. It mirrors the fake session
// backend's commit behavior exactly (pinned identity/date).
func applyScenarioPatch(t *testing.T, fx *fixture.Fixture, dir, ticket, slice string, attempt int) {
	t.Helper()
	path := filepath.Join(fx.ScenarioDir, "slices", slice, fmt.Sprintf("attempt-%d", attempt), "patch.diff")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs %s: %v", path, err)
	}
	if _, err := gitx.Run(dir, "apply", abs); err != nil {
		t.Fatalf("git apply %s: %v", path, err)
	}
	if _, err := gitx.Run(dir, "add", "-A"); err != nil {
		t.Fatalf("git add after %s: %v", path, err)
	}
	msg := fmt.Sprintf("%s %s: attempt %d", ticket, slice, attempt)
	if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "-m", msg); err != nil {
		t.Fatalf("git commit after %s: %v", path, err)
	}
}

// journalBuilt records a green result the way frontier does when its commit
// verifies: the result line, what the builder claimed, then the verified line
// that makes the commit one jig built (journal.BuiltCommits).
func journalBuilt(t *testing.T, st *store.Store, ticket, slice, commit string, attempt int) {
	t.Helper()
	for _, l := range []journal.Line{
		{Slice: slice, Event: "result", Outcome: "green", Commit: commit, Attempt: attempt},
		{Slice: slice, Event: "verified", Commit: commit, Attempt: attempt},
	} {
		if err := journal.Append(st, ticket, l); err != nil {
			t.Fatalf("journal %s %s/%d: %v", l.Event, slice, attempt, err)
		}
	}
}

// driveAttempt plays one scenario attempt for slice against the build
// lease exactly as frontier would: it writes the dispatch line, applies any
// patch, then routes the scenario result to a journal result line and slice
// state the way frontier's Run does.
func driveAttempt(t *testing.T, st *store.Store, fx *fixture.Fixture, dir, model, slice string, attempt int) {
	t.Helper()
	ticket := fx.Ticket

	if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "dispatch", Model: model, Attempt: attempt}); err != nil {
		t.Fatalf("journal dispatch %s/%d: %v", slice, attempt, err)
	}
	applyScenarioPatch(t, fx, dir, ticket, slice, attempt)
	res := scenarioResult(t, fx, slice, attempt)
	outcome, _ := res["outcome"].(string)

	switch outcome {
	case "green":
		sha, err := gitx.RevParse(dir, "HEAD")
		if err != nil {
			t.Fatalf("resolve HEAD after %s/%d: %v", slice, attempt, err)
		}
		journalBuilt(t, st, ticket, slice, sha, attempt)
		if err := st.WriteSliceState(ticket, slice, store.SliceState{State: "green", Attempts: attempt}); err != nil {
			t.Fatalf("write slice state %s: %v", slice, err)
		}
		if err := st.Push(fmt.Sprintf("%s: slice %s green", ticket, slice)); err != nil {
			t.Fatalf("push after %s green: %v", slice, err)
		}
	case "needs-input":
		if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "result", Outcome: outcome, Attempt: attempt}); err != nil {
			t.Fatalf("journal result %s/%d: %v", slice, attempt, err)
		}
		question, _ := res["question"].(string)
		qid := st.NextQuestionID(ticket)
		if err := st.WriteQuestion(ticket, store.Question{ID: qid, Slice: slice, Status: "open", Body: question}); err != nil {
			t.Fatalf("write question for %s: %v", slice, err)
		}
		if err := st.WriteSliceState(ticket, slice, store.SliceState{State: "needs-input", Attempts: attempt, Question: qid}); err != nil {
			t.Fatalf("write slice state %s: %v", slice, err)
		}
		if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "question", Attempt: attempt}); err != nil {
			t.Fatalf("journal question %s/%d: %v", slice, attempt, err)
		}
		if err := st.Push(fmt.Sprintf("%s: slice %s needs input", ticket, slice)); err != nil {
			t.Fatalf("push after %s needs-input: %v", slice, err)
		}
	default:
		if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "result", Outcome: outcome, Attempt: attempt}); err != nil {
			t.Fatalf("journal result %s/%d: %v", slice, attempt, err)
		}
		if err := st.WriteSliceState(ticket, slice, store.SliceState{State: "queued", Attempts: attempt}); err != nil {
			t.Fatalf("write slice state %s: %v", slice, err)
		}
		if err := st.Push(fmt.Sprintf("%s: slice %s %s", ticket, slice, outcome)); err != nil {
			t.Fatalf("push after %s %s: %v", slice, outcome, err)
		}
	}
}

// answerSliceC answers slice c's open question and re-queues it, mirroring
// store.Answer + frontier's requeue-on-answer step.
func answerSliceC(t *testing.T, st *store.Store, ticket, text string) {
	t.Helper()
	questions, err := st.ReadQuestions(ticket)
	if err != nil {
		t.Fatalf("read questions: %v", err)
	}
	var qid string
	for _, q := range questions {
		if q.Slice == "c" && q.Status == "open" {
			qid = q.ID
		}
	}
	if qid == "" {
		t.Fatal("no open question for slice c")
	}
	slice, err := st.Answer(ticket, qid, text)
	if err != nil {
		t.Fatalf("answer %s: %v", qid, err)
	}
	if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "answer"}); err != nil {
		t.Fatalf("journal answer: %v", err)
	}
	cur, err := st.ReadSliceState(ticket, slice)
	if err != nil {
		t.Fatalf("read slice state %s: %v", slice, err)
	}
	cur.State = "queued"
	cur.Question = ""
	if err := st.WriteSliceState(ticket, slice, cur); err != nil {
		t.Fatalf("requeue slice %s: %v", slice, err)
	}
	if err := st.Push(fmt.Sprintf("%s: answer %s", ticket, qid)); err != nil {
		t.Fatalf("push after answer %s: %v", qid, err)
	}
}

// driveBuild plays the fixture's whole a/b/c/d scenario against a fresh
// build lease, exactly as frontier's own loop would, and records the
// ticket's start sha the way frontier's first dispatch does. Every builder
// dispatch uses model.
func driveBuild(t *testing.T, fx *fixture.Fixture, model string) {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	dir := buildLeaseDir(t, fx)

	startSHA, err := gitx.RevParse(dir, "origin/main")
	if err != nil {
		t.Fatalf("resolve origin/main: %v", err)
	}
	startPath := filepath.Join(st.TicketDir(fx.Ticket), "start.fixture-repo.sha")
	if err := os.MkdirAll(filepath.Dir(startPath), 0o755); err != nil {
		t.Fatalf("create ticket dir: %v", err)
	}
	if err := os.WriteFile(startPath, []byte(startSHA), 0o644); err != nil {
		t.Fatalf("write start sha: %v", err)
	}
	if err := st.Push(fx.Ticket + ": record start sha"); err != nil {
		t.Fatalf("push start sha: %v", err)
	}

	driveAttempt(t, st, fx, dir, model, "a", 1)
	driveAttempt(t, st, fx, dir, model, "b", 1)
	driveAttempt(t, st, fx, dir, model, "b", 2)
	driveAttempt(t, st, fx, dir, model, "c", 1)
	answerSliceC(t, st, fx.Ticket, "Casual.")
	driveAttempt(t, st, fx, dir, model, "c", 2)
	driveAttempt(t, st, fx, dir, model, "d", 1)
}

// driveFix1 plays the fixture's fix-1 scenario attempt (gate round 1's fix
// slice) against the build lease, green on the first try.
func driveFix1(t *testing.T, fx *fixture.Fixture, model string) {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	dir := buildLeaseDir(t, fx)
	driveAttempt(t, st, fx, dir, model, "fix-1", 1)
}

// authorBranch builds branch outside jig, the way its author would: a fresh
// clone of the fixture's remote, a commit off main that leaves the fixture
// repo passing its oracles (the scenario's slice a patch, which fixes Clamp),
// pushed to origin under branch. It returns the pushed tip. jig never builds
// or commits anything on it.
func authorBranch(t *testing.T, fx *fixture.Fixture, branch string) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "clone", fx.RepoRemote, ".")
	run(t, dir, "checkout", "-b", branch)
	applyScenarioPatch(t, fx, dir, "author", "a", 1)
	run(t, dir, "push", "origin", branch)
	return run(t, dir, "rev-parse", "HEAD")
}

// authorPush is the author pushing again: one more commit, adding file, on
// top of branch as origin has it. It returns the new tip.
func authorPush(t *testing.T, fx *fixture.Fixture, branch, file string) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "clone", fx.RepoRemote, ".")
	run(t, dir, "checkout", branch)
	if err := os.WriteFile(filepath.Join(dir, file), []byte("the author again\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	run(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "-m", "author: "+file); err != nil {
		t.Fatalf("commit %s: %v", file, err)
	}
	run(t, dir, "push", "origin", branch)
	return run(t, dir, "rev-parse", "HEAD")
}

// newAdoptTicket mints a ticket with a record and nothing else, as `jig
// ticket new` leaves it: no brief, no slices, no branch.
func newAdoptTicket(t *testing.T, d Deps, ticket string) {
	t.Helper()
	if err := d.Store.CreateTicketRecord(ticket, store.Ticket{Title: "Add retry"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	if err := d.Store.Push(ticket + ": minted"); err != nil {
		t.Fatalf("push the minted ticket: %v", err)
	}
}

// pushedBranch pushes an author's branch to origin and returns its name, for
// a test that only needs a branch a gate can adopt.
func pushedBranch(t *testing.T, fx *fixture.Fixture) string {
	t.Helper()
	const name = "author-branch"
	authorBranch(t, fx, name)
	return name
}

// wantNoAdoption fails when ticket recorded a branch or a start sha: a round
// that was refused adopted nothing. It is for a ticket whose start sha was not
// written before the round, which none of the tests that use it drive a build
// for.
func wantNoAdoption(t *testing.T, d Deps, ticket string) {
	t.Helper()
	rec, err := d.Store.ReadTicket(ticket)
	if err != nil {
		t.Fatalf("ReadTicket: %v", err)
	}
	if rec.Branch != "" {
		t.Fatalf("the refused round recorded branch %q, want no adoption", rec.Branch)
	}
	if got, err := os.ReadFile(d.Store.StartSHAPath(ticket, "fixture-repo")); !os.IsNotExist(err) {
		t.Fatalf("the refused round recorded a start sha %q (err %v), want none", got, err)
	}
}

// dropVerifiedLines rewrites ticket's journal the way a jig that never
// journaled verified lines left it (v0.1.x): every line but those. The result
// lines naming the commits stay, and so do the slices' green states.
func dropVerifiedLines(t *testing.T, st *store.Store, ticket string) {
	t.Helper()
	dropJournalLines(t, st, ticket, "journal as v0.1 wrote it", func(l journal.Line) bool {
		return l.Event == "verified"
	})
}

// dropJournalLines rewrites ticket's journal without the lines drop selects,
// and pushes it. Every other line is kept byte for byte.
func dropJournalLines(t *testing.T, st *store.Store, ticket, why string, drop func(journal.Line) bool) {
	t.Helper()
	path := filepath.Join(st.TicketDir(ticket), "journal.ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the journal: %v", err)
	}
	var kept []string
	for _, raw := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var l journal.Line
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			t.Fatalf("parse journal line %q: %v", raw, err)
		}
		if !drop(l) {
			kept = append(kept, raw)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite the journal: %v", err)
	}
	if err := st.Push(ticket + ": " + why); err != nil {
		t.Fatalf("push the rewritten journal: %v", err)
	}
}

// amendBriefAndRequeue amends every section of ticket's brief and runs the
// real `jig requeue --from-brief-diff` over it (frontier.Requeue), which sets
// each slice whose section changed back to queued, green ones included. It
// returns the slices it touched.
func amendBriefAndRequeue(t *testing.T, d Deps, ticket string) []string {
	t.Helper()
	path := filepath.Join(d.Store.TicketDir(ticket), "brief.md")
	brief, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read brief.md: %v", err)
	}
	amended := strings.ReplaceAll(string(brief), "\n## ", "\nAmended.\n\n## ") + "\nAmended.\n"
	if err := os.WriteFile(path, []byte(amended), 0o644); err != nil {
		t.Fatalf("write brief.md: %v", err)
	}
	touched, err := frontier.Requeue(frontier.Deps{
		Store:   d.Store,
		Journal: func(l journal.Line) error { return journal.Append(d.Store, ticket, l) },
	}, ticket, true)
	if err != nil {
		t.Fatalf("Requeue --from-brief-diff: %v", err)
	}
	return touched
}
