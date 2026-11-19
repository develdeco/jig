package e2e

// End-to-end tests of the recorded gate round and the publish forms: jig
// launches no model, so an agent that jig did not start does the reviewing and
// the pushing. The fake harness stands for those agents: the builder commits in
// the operator clone and has the commit verified, a reviewer in a session of
// its own reads the round and reports, and the builder (or a person) pushes.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx/gitxtest"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// builtTicket is JIG-1 built by hand: the builder session committed the
// scenario's patches on the ticket's branch in the operator clone, and had the
// commits verified. Either slice a alone is built (a gate then needs --early),
// or the whole ticket, with every slice green. The branch is not pushed.
type builtTicket struct {
	whole   bool
	fx      *fixture.Fixture
	env     jigEnv
	builder *fakeHarness
	branch  string
	sha     string
	st      *store.Store
}

// verifySlots bounds how many tickets are being verified at once across the
// tests of this file: each verification is a `go test` in a worktree, and the
// whole suite runs its tests in parallel.
var verifySlots = make(chan struct{}, 2)

// newBuiltTicket builds the ticket described on builtTicket. The commits are
// verified by detached workers started together, as an agent's commits would
// be; the ticket is built when every verification has recorded its pass.
func newBuiltTicket(t *testing.T, whole bool) builtTicket {
	t.Helper()
	fx, env := newFixture(t, fixture.Opts{})
	return buildTicketOn(t, fx, env, whole)
}

// buildTicketOn builds the ticket on a fixture and an environment the test
// made (newBuiltTicket's, but for a test that needs to shape them first: a
// scenario branch, a jig home of its own, a missing brief).
func buildTicketOn(t *testing.T, fx *fixture.Fixture, env jigEnv, whole bool) builtTicket {
	t.Helper()
	verifySlots <- struct{}{}
	defer func() { <-verifySlots }()
	dropEnvFromSlices(t, fx)
	h := newHarness(t, fx, env, "s-builder")
	branch := fx.Branch
	h.Branch(branch)

	// Each commit is proven right after it is made, as a builder does.
	var last string
	prove := func(slice string, attempt int) {
		t.Helper()
		last = h.CommitScenario(slice, attempt)
		res := h.Verify(fx.Ticket, slice)
		if res.Code != 0 || !strings.Contains(res.Stdout, "state: pass") {
			t.Fatalf("jig verify %s exit = %d, want 0 and a pass\nstdout:\n%s\nstderr:\n%s", slice, res.Code, res.Stdout, res.Stderr)
		}
	}
	prove("a", 1)
	if whole {
		// Slice b's first attempt fails its test and is never verified; the
		// second is the slice's commit.
		h.CommitScenario("b", 1)
		prove("b", 2)
		prove("c", 2)
		prove("d", 1)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	return builtTicket{whole: whole, fx: fx, env: env, builder: h, branch: branch, sha: last, st: st}
}

// buildFix has the builder commit the scenario's patch for each slice given -
// gate fix slices a round queued, which the scenario builds - and have the
// commits verified, the way newBuiltTicket builds the ticket. It returns the
// head the builder is at afterwards.
func (b builtTicket) buildFix(t *testing.T, slices ...string) string {
	t.Helper()
	verifySlots <- struct{}{}
	defer func() { <-verifySlots }()
	var sha string
	for _, s := range slices {
		sha = b.builder.CommitScenario(s, 1)
		res := b.builder.Verify(b.fx.Ticket, s)
		if res.Code != 0 || !strings.Contains(res.Stdout, "state: pass") {
			t.Fatalf("jig verify %s exit = %d, want 0 and a pass\nstdout:\n%s\nstderr:\n%s", s, res.Code, res.Stdout, res.Stderr)
		}
	}
	return sha
}

// workFile is a file of the ticket's work directory in the store.
func (b builtTicket) workFile(name string) string {
	return filepath.Join(b.st.TicketDir(b.fx.Ticket), "work", name)
}

// roundDir is a recorded gate round's directory in the store.
func (b builtTicket) roundDir(n string) string {
	return filepath.Join(b.st.TicketDir(b.fx.Ticket), "gate", "round-"+n)
}

// openRound opens round 1 the way the builder does: `jig gate`, run from the
// clone (with --early when the ticket is not whole).
func (b builtTicket) openRound(t *testing.T) jigResult {
	t.Helper()
	args := []string{"gate", b.fx.Ticket}
	if !b.whole {
		args = append(args, "--early")
	}
	res := b.builder.Jig(args...)
	if res.Code != 0 {
		t.Fatalf("jig gate exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	return res
}

// reviewRequest is the part of a round's review.json these tests read.
type reviewRequest struct {
	HeadSHA    string   `json:"head_sha"`
	BaseSHA    string   `json:"base_sha"`
	Scope      string   `json:"scope"`
	MustReview []string `json:"must_review"`
}

// request reads the review.json of the open round n.
func (b builtTicket) request(t *testing.T, round int) reviewRequest {
	t.Helper()
	var req reviewRequest
	data := readFileOrFatal(t, b.workFile(fmt.Sprintf("gate.round-%d.review.json", round)))
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("parse review.json: %v\n%s", err, data)
	}
	return req
}

// mustReview reads the paths the open round n's review.json says a reviewer
// must cover. Round 1 reviews the commit the ticket was built at; a later round
// the head the clone's branch is at now.
func (b builtTicket) mustReview(t *testing.T, round int) []string {
	t.Helper()
	req := b.request(t, round)
	want := b.sha
	if round > 1 {
		want = h2tip(t, b)
	}
	if req.HeadSHA != want {
		t.Fatalf("review.json head_sha = %s, want the clone's commit %s", req.HeadSHA, want)
	}
	if len(req.MustReview) == 0 {
		t.Fatalf("review.json must_review is empty (round %d)", round)
	}
	return req.MustReview
}

// cleanResult writes the result.json of a reviewer that found nothing and read
// every file it had to in round n, and returns its path.
func (b builtTicket) cleanResult(t *testing.T, round int) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"findings": []any{}, "still_present": []any{}, "reviewed_paths": b.mustReview(t, round), "summary": "nothing found",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "clean.json")
	writeFileOrFatal(t, path, data)
	return path
}

// report reads a recorded round's report.yaml.
func (b builtTicket) report(t *testing.T, n string) map[string]any {
	t.Helper()
	var rep map[string]any
	if err := yaml.Unmarshal(readFileOrFatal(t, filepath.Join(b.roundDir(n), "report.yaml")), &rep); err != nil {
		t.Fatalf("parse report.yaml: %v", err)
	}
	return rep
}

// reviewedClean has a reviewer in a session of its own read the open round n
// and report it clean, and returns the reviewer's `review done` result.
func (b builtTicket) reviewedClean(t *testing.T, round int) jigResult {
	t.Helper()
	r2 := newHarness(t, b.fx, b.env, "s-reviewer")
	if ctx := r2.Jig("review", "context", b.fx.Ticket); ctx.Code != 0 {
		t.Fatalf("jig review context exit = %d\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	r2.Bash("git log --oneline -3")
	done := r2.Jig("review", "done", b.fx.Ticket, "--result", b.cleanResult(t, round))
	if done.Code != 0 || !strings.Contains(done.Stdout, "verdict: clean") {
		t.Fatalf("jig review done exit = %d, want 0 and a clean verdict\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
	return done
}

// h2tip is the sha the clone's branch is at.
func h2tip(t *testing.T, b builtTicket) string {
	t.Helper()
	return gitLog(t, b.fx.RepoDir, "rev-parse", "refs/heads/"+b.branch)
}

// originRefOrEmpty is the sha refname is at in the bare remote, or "".
func originRefOrEmpty(t *testing.T, remote, refname string) string {
	t.Helper()
	return gitLog(t, remote, "for-each-ref", "--format=%(objectname)", refname)
}

// githubClone makes the fixture's repo a GitHub repo as far as jig can tell:
// the project names its remote by a GitHub URL, which the git config of the
// test rewrites to the bare remote on disk, and a fake gh is first on PATH. It
// returns the environment to run jig in, and the path of the fake gh's log.
func githubClone(t *testing.T, b builtTicket) (jigEnv, string) {
	t.Helper()
	return githubRemote(t, b.fx, b.env.withGitGlobal(t))
}

// githubRemote is githubClone for a test that already gave env a git global
// config of its own (withGitGlobal) and may have installed settings in it, a
// core.hooksPath say, which a second withGitGlobal would leave behind.
func githubRemote(t *testing.T, fx *fixture.Fixture, env jigEnv) (jigEnv, string) {
	t.Helper()
	const remoteURL = "https://github.com/acme/fixture-repo.git"
	if _, err := gitxtest.RunReplaceEnv(fx.Dir, env.environ(), "config", "--global", "url."+filepath.ToSlash(fx.RepoRemote)+".insteadOf", remoteURL); err != nil {
		t.Fatalf("git config --global url.insteadOf: %v", err)
	}
	path := filepath.Join(fx.StoreDir, "project.yaml")
	body := string(readFileOrFatal(t, path))
	if strings.Count(body, fx.RepoRemote) != 1 {
		t.Fatalf("test setup: %s names the repo's remote %s %d times:\n%s", path, fx.RepoRemote, strings.Count(body, fx.RepoRemote), body)
	}
	writeFileOrFatal(t, path, []byte(strings.Replace(body, fx.RepoRemote, remoteURL, 1)))

	log := filepath.Join(t.TempDir(), "gh.log")
	stub := fixture.GhStub(t)
	env = env.with(
		"PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_STUB_LOG="+log,
		"GH_STUB_STATE="+filepath.Join(t.TempDir(), "gh.state"),
	)
	return env, log
}

// ghCalls are the argv of each call the fake gh logged, joined by spaces.
func ghCalls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var calls []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var c struct {
			Argv []string `json:"argv"`
		}
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("parse a gh log line %q: %v", line, err)
		}
		calls = append(calls, strings.Join(c.Argv[1:], " "))
	}
	return calls
}

// TestPublishBodyThenPR: with the round clean, `jig publish --body` renders
// the pull request body and the review notes and nothing else: no lease is
// made or changed, nothing is pushed, no pull request is opened. The harness
// pushes the branch and opens the pull request itself, and `jig publish --pr
// <url>` records it after the host says it is from the ticket's branch: a pr
// line and a publish-done line.
func TestPublishBodyThenPR(t *testing.T) {
	t.Parallel()
	b := newBuiltTicket(t, true)
	env, log := githubClone(t, b)
	b.env = env
	b.builder.env = env
	b.openRound(t)
	b.reviewedClean(t, 1)

	cloneHead := gitLog(t, b.fx.RepoDir, "rev-parse", "refs/heads/"+b.branch)
	originBefore := originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch)
	callsBefore := len(ghCalls(t, log))

	body := runJig(t, env, b.builder.Clone, "publish", b.fx.Ticket, "--body")
	if body.Code != 0 || !strings.Contains(body.Stdout, "head: "+b.sha) || !strings.Contains(body.Stdout, "branch: "+b.branch) {
		t.Fatalf("jig publish --body exit = %d, want 0 and the reviewed head\nstdout:\n%s\nstderr:\n%s", body.Code, body.Stdout, body.Stderr)
	}
	prBody := filepath.Join(b.st.TicketDir(b.fx.Ticket), "pr", "fixture-repo.md")
	text := string(readFileOrFatal(t, prBody))
	for _, want := range []string{"## What\n", "## Verification", "at the reviewed head " + b.sha[:7]} {
		if !strings.Contains(text, want) {
			t.Errorf("pr/fixture-repo.md lacks %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(b.st.TicketDir(b.fx.Ticket), "pr", "review-notes.md")); err != nil {
		t.Errorf("--body wrote no review notes: %v", err)
	}
	// No lease was made or changed, and nothing reached origin or the host.
	if _, err := os.Stat(filepath.Join(env.home, "pool", "fixture-repo", b.fx.Ticket+"-publish")); err == nil {
		t.Errorf("--body made a publish lease")
	}
	if got := gitLog(t, b.fx.RepoDir, "rev-parse", "refs/heads/"+b.branch); got != cloneHead {
		t.Errorf("the clone's %s moved from %s to %s", b.branch, cloneHead, got)
	}
	if got := originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch); got != originBefore {
		t.Errorf("origin's %s went from %q to %q", b.branch, originBefore, got)
	}
	if got := len(ghCalls(t, log)); got != callsBefore {
		t.Errorf("--body called gh %d time(s): %v", got-callsBefore, ghCalls(t, log))
	}
	var bodyLine journal.Line
	for _, l := range readJournal(t, b.fx, b.fx.Ticket) {
		switch l.Event {
		case journal.EventPrBody:
			bodyLine = l
		case journal.EventPr, journal.EventPublishDone:
			t.Errorf("--body journaled %+v", l)
		}
	}
	if bodyLine.Commit != b.sha || bodyLine.Branch != b.branch {
		t.Errorf("pr-body line = %+v, want commit %s on %s", bodyLine, b.sha, b.branch)
	}

	// The harness pushes the branch; the pull request is opened by it.
	b.builder.Push(b.branch)
	const prURL = "https://github.com/acme/fixture-repo/pull/7"
	wrongHead := runJig(t, env.with("GH_STUB_HEAD_REF=some-other-branch", "GH_STUB_HEAD_OID="+b.sha), b.builder.Clone, "publish", b.fx.Ticket, "--pr", prURL)
	if wrongHead.Code == 0 || !strings.Contains(wrongHead.Stdout, "PR_HEAD_MISMATCH") {
		t.Errorf("--pr for a pull request from another branch: exit = %d, want PR_HEAD_MISMATCH\nstdout:\n%s", wrongHead.Code, wrongHead.Stdout)
	}
	stalePR := runJig(t, env.with("GH_STUB_HEAD_REF="+b.branch, "GH_STUB_HEAD_OID="+strings.Repeat("0", 40)), b.builder.Clone, "publish", b.fx.Ticket, "--pr", prURL)
	if stalePR.Code == 0 || !strings.Contains(stalePR.Stdout, "PUBLISH_UNREVIEWED_HEAD") {
		t.Errorf("--pr for a pull request at another commit: exit = %d, want PUBLISH_UNREVIEWED_HEAD\nstdout:\n%s", stalePR.Code, stalePR.Stdout)
	}
	for _, l := range readJournal(t, b.fx, b.fx.Ticket) {
		if l.Event == journal.EventPr || l.Event == journal.EventPublishDone {
			t.Fatalf("a refused --pr journaled %+v", l)
		}
	}

	rec := runJig(t, env.with("GH_STUB_HEAD_REF="+b.branch, "GH_STUB_HEAD_OID="+b.sha), b.builder.Clone, "publish", b.fx.Ticket, "--pr", prURL)
	if rec.Code != 0 || !strings.Contains(rec.Stdout, "verified: yes") {
		t.Fatalf("jig publish --pr exit = %d, want 0 and a verified pull request\nstdout:\n%s\nstderr:\n%s", rec.Code, rec.Stdout, rec.Stderr)
	}
	var prLine journal.Line
	done := false
	for _, l := range readJournal(t, b.fx, b.fx.Ticket) {
		switch l.Event {
		case journal.EventPr:
			prLine = l
		case journal.EventPublishDone:
			done = true
		}
	}
	if prLine.URL != prURL || prLine.Branch != b.branch || prLine.Commit != b.sha || !done {
		t.Errorf("pr line = %+v, publish-done = %v, want %s from %s at %s, then publish-done", prLine, done, prURL, b.branch, b.sha)
	}
	// Recording a pull request is not publishing it: jig opened none, and the
	// branch is the one the harness pushed.
	for _, c := range ghCalls(t, log)[callsBefore:] {
		if strings.HasPrefix(c, "pr create") || strings.HasPrefix(c, "pr edit") {
			t.Errorf("--pr called %q", c)
		}
	}
	if got := originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch); got != b.sha {
		t.Errorf("origin's %s = %s, want the harness's push of %s", b.branch, got, b.sha)
	}
}
