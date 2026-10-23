package e2e

// End-to-end tests of the recorded gate round and the publish forms: jig
// launches no model, so an agent that jig did not start does the reviewing and
// the pushing. The fake harness stands for those agents: the builder commits in
// the operator clone and has the commit verified, a reviewer in a session of
// its own reads the round and reports, and the builder (or a person) pushes.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
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
	branch := "jig/" + fx.Ticket
	h.Branch(branch)

	type built struct{ slice, sha string }
	steps := []built{{"a", h.CommitScenario("a", 1)}}
	if whole {
		// Slice b's first attempt fails its test and is never verified; the
		// second is the slice's commit.
		h.CommitScenario("b", 1)
		steps = append(steps, built{"b", h.CommitScenario("b", 2)}, built{"c", h.CommitScenario("c", 2)}, built{"d", h.CommitScenario("d", 1)})
	}
	for _, s := range steps {
		res := h.Jig("verify", fx.Ticket, s.sha, "--slice", s.slice)
		if res.Code != 0 || !strings.Contains(res.Stdout, "state: pending") {
			t.Fatalf("jig verify %s exit = %d, want 0 and a pending worker\nstdout:\n%s\nstderr:\n%s", s.slice, res.Code, res.Stdout, res.Stderr)
		}
	}
	for _, s := range steps {
		l := waitJournal(t, fx, fx.Ticket, verifyWait, func(l journal.Line) bool {
			if l.Slice != s.slice {
				return false
			}
			return (l.Event == journal.EventVerified && l.Commit == s.sha) || (l.Event == journal.EventOracle && l.Outcome == "fail")
		})
		if l.Event != journal.EventVerified {
			t.Fatalf("slice %s failed its oracle at %s: %s (journal line %+v)", s.slice, s.sha, l.Summary, l)
		}
	}
	h.WaitVerifiers()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	return builtTicket{whole: whole, fx: fx, env: env, builder: h, branch: branch, sha: steps[len(steps)-1].sha, st: st}
}

// buildFix has the builder commit the scenario's patch for each slice given -
// gate fix slices a round queued, which the scenario builds - and have the
// commits verified, the way newBuiltTicket builds the ticket. It returns the
// head the builder is at afterwards.
func (b builtTicket) buildFix(t *testing.T, slices ...string) string {
	t.Helper()
	verifySlots <- struct{}{}
	defer func() { <-verifySlots }()
	type built struct{ slice, sha string }
	var steps []built
	for _, s := range slices {
		steps = append(steps, built{s, b.builder.CommitScenario(s, 1)})
	}
	for _, s := range steps {
		res := b.builder.Jig("verify", b.fx.Ticket, s.sha, "--slice", s.slice)
		if res.Code != 0 || !strings.Contains(res.Stdout, "state: pending") {
			t.Fatalf("jig verify %s exit = %d, want 0 and a pending worker\nstdout:\n%s\nstderr:\n%s", s.slice, res.Code, res.Stdout, res.Stderr)
		}
	}
	for _, s := range steps {
		l := waitJournal(t, b.fx, b.fx.Ticket, verifyWait, func(l journal.Line) bool {
			if l.Slice != s.slice {
				return false
			}
			return (l.Event == journal.EventVerified && l.Commit == s.sha) || (l.Event == journal.EventOracle && l.Outcome == "fail" && l.Commit == s.sha)
		})
		if l.Event != journal.EventVerified {
			t.Fatalf("slice %s failed its oracle at %s: %s (journal line %+v)", s.slice, s.sha, l.Summary, l)
		}
	}
	b.builder.WaitVerifiers()
	return steps[len(steps)-1].sha
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

// mustReview reads the paths the open round's review.json says a reviewer must
// cover.
func (b builtTicket) mustReview(t *testing.T) []string {
	t.Helper()
	var req struct {
		HeadSHA    string   `json:"head_sha"`
		Worktree   string   `json:"worktree"`
		MustReview []string `json:"must_review"`
	}
	data := readFileOrFatal(t, b.workFile("gate.round-1.review.json"))
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("parse review.json: %v\n%s", err, data)
	}
	if req.HeadSHA != b.sha {
		t.Fatalf("review.json head_sha = %s, want the clone's commit %s", req.HeadSHA, b.sha)
	}
	if len(req.MustReview) == 0 {
		t.Fatalf("review.json must_review is empty:\n%s", data)
	}
	return req.MustReview
}

// cleanResult writes the result.json of a reviewer that found nothing and read
// every file it had to, and returns its path.
func (b builtTicket) cleanResult(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"findings": []any{}, "still_present": []any{}, "reviewed_paths": b.mustReview(t), "summary": "nothing found",
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

// reviewedClean has a reviewer in a session of its own read the open round and
// report it clean, and returns the reviewer's `review done` result.
func (b builtTicket) reviewedClean(t *testing.T) jigResult {
	t.Helper()
	r2 := newHarness(t, b.fx, b.env, "s-reviewer")
	if ctx := r2.Jig("review", "context", b.fx.Ticket); ctx.Code != 0 {
		t.Fatalf("jig review context exit = %d\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	r2.Bash("git log --oneline -3")
	done := r2.Jig("review", "done", b.fx.Ticket, "--result", b.cleanResult(t))
	if done.Code != 0 || !strings.Contains(done.Stdout, "verdict: clean") {
		t.Fatalf("jig review done exit = %d, want 0 and a clean verdict\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
	return done
}

// TestGateReviewsClonesCopy: a branch built by hand in the operator clone and
// not pushed anywhere is what `jig gate` reviews: it reads the clone's
// committed copy, re-verifies it (reusing the pass `jig verify` recorded),
// opens the round without making a gate/round-N directory, and hands the
// reviewer a request. A reviewer in a session of its own reads it, reads the
// code, and ends the round with `jig review done`: clean, and independent of the
// builder.
func TestGateReviewsClonesCopy(t *testing.T) {
	t.Parallel()
	b := newBuiltTicket(t, false)

	gate := b.openRound(t)
	for _, want := range []string{"round: 1", "branch: " + b.branch, "copy: the clone's", "head: " + b.sha, "scope: full"} {
		if !strings.Contains(gate.Stdout, want) {
			t.Errorf("jig gate output lacks %q\nstdout:\n%s", want, gate.Stdout)
		}
	}
	if originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch) != "" {
		t.Fatalf("the branch is on origin, but it was never pushed")
	}
	// The round is open, not made: its state is in the work directory, and the
	// store has no gate/round-1 yet.
	if _, err := os.Stat(b.roundDir("1")); err == nil {
		t.Errorf("%s exists before `jig review done`", b.roundDir("1"))
	}
	openData := readFileOrFatal(t, b.workFile("gate.round-1.open.yaml"))
	var open struct {
		Round int    `yaml:"round"`
		Head  string `yaml:"head"`
		Copy  string `yaml:"copy"`
		Lease string `yaml:"lease"`
	}
	if err := yaml.Unmarshal(openData, &open); err != nil {
		t.Fatalf("parse the open record: %v", err)
	}
	if open.Round != 1 || open.Head != b.sha || open.Copy != "the clone's" || open.Lease == "" {
		t.Errorf("open record = %+v, want round 1 at %s, the clone's copy and a lease", open, b.sha)
	}
	// The store is shared: neither the open record nor the request names a
	// directory of this machine's jig home. The lease is written relative to it.
	for _, name := range []string{"gate.round-1.open.yaml", "gate.round-1.review.json"} {
		if strings.Contains(strings.ReplaceAll(string(readFileOrFatal(t, b.workFile(name))), `\\`, `\`), b.env.home) {
			t.Errorf("%s names the jig home %s", name, b.env.home)
		}
	}
	lease := filepath.Join(b.env.home, filepath.FromSlash(open.Lease))
	// The oracle pass jig verify recorded stands for the gate's run of alpha's
	// oracle; beta's, which no one verified, the gate ran itself.
	var req struct {
		OraclesPassed []struct {
			Workspace  string `json:"workspace"`
			ReusedFrom string `json:"reused_from"`
		} `json:"oracles_passed"`
	}
	if err := json.Unmarshal(readFileOrFatal(t, b.workFile("gate.round-1.review.json")), &req); err != nil {
		t.Fatal(err)
	}
	reused := map[string]string{}
	for _, o := range req.OraclesPassed {
		reused[o.Workspace] = o.ReusedFrom
	}
	if reused["alpha"] != b.sha || reused["beta"] != "" {
		t.Errorf("oracles_passed reused_from = %v, want alpha reused from %s and beta run by the gate", reused, b.sha)
	}
	// The reviewer reads the code at the head in the worktree the open round
	// names, which is jig's, not the clone.
	if head := gitLog(t, lease, "rev-parse", "HEAD"); head != b.sha {
		t.Errorf("the open round's worktree is at %s, want %s", head, b.sha)
	}

	// jig review context again prints the open round and opens no other.
	r2 := newHarness(t, b.fx, b.env, "s-reviewer")
	ctx := r2.Jig("review", "context", b.fx.Ticket, "--model", "test-model")
	if ctx.Code != 0 || !strings.Contains(ctx.Stdout, "round: 1") || !strings.Contains(ctx.Stdout, "reviewer: s-reviewer") || !strings.Contains(ctx.Stdout, lease) {
		t.Fatalf("jig review context exit = %d, want 0, the open round and its worktree %s\nstdout:\n%s\nstderr:\n%s", ctx.Code, lease, ctx.Stdout, ctx.Stderr)
	}
	r2.Bash("git log --oneline -3")

	done := r2.Jig("review", "done", b.fx.Ticket, "--result", b.cleanResult(t))
	if done.Code != 0 || !strings.Contains(done.Stdout, "verdict: clean") || !strings.Contains(done.Stdout, "independent: true") {
		t.Fatalf("jig review done exit = %d, want 0, clean and independent\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
	rep := b.report(t, "1")
	if rep["verdict"] != "clean" || rep["independent"] != true || rep["model"] != "test-model" {
		t.Errorf("report.yaml = %v, want a clean round, independent: true, model test-model", rep)
	}
	reviewer, _ := rep["reviewer"].(map[string]any)
	if reviewer["session"] != "s-reviewer" {
		t.Errorf("report.yaml reviewer = %v, want session s-reviewer", reviewer)
	}
	if shas, _ := rep["reviewed_sha"].(map[string]any); shas["fixture-repo"] != b.sha {
		t.Errorf("report.yaml reviewed_sha = %v, want %s", shas, b.sha)
	}
	if !strings.Contains(string(readFileOrFatal(t, filepath.Join(b.roundDir("1"), "report.yaml"))), "independent: true") {
		t.Errorf("report.yaml does not say `independent: true`")
	}
	// The round is closed: there is no open round to close again.
	if again := r2.Jig("review", "done", b.fx.Ticket, "--result", b.cleanResult(t)); again.Code == 0 || !strings.Contains(again.Stdout, "REVIEW_NOT_OPEN") {
		t.Errorf("a second jig review done exit = %d, want REVIEW_NOT_OPEN\nstdout:\n%s", again.Code, again.Stdout)
	}
	// Nothing on the branch moved, and the gate pushed nothing of it.
	if head := h2tip(t, b); head != b.sha {
		t.Errorf("the clone's branch is at %s, want %s", head, b.sha)
	}
	if originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch) != "" {
		t.Errorf("the gate pushed the branch to origin")
	}
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

// TestReviewNotIndependent: the agent that built the change opens and reports
// the round itself. The round is recorded, with its report saying the reviewer
// was not independent, and it is not clean: `jig review done` refuses it with
// REVIEW_NOT_INDEPENDENT, and a publish after it with PUBLISH_NOT_CLEAN.
func TestReviewNotIndependent(t *testing.T) {
	t.Parallel()
	b := newBuiltTicket(t, true)
	b.openRound(t)

	if ctx := b.builder.Jig("review", "context", b.fx.Ticket); ctx.Code != 0 {
		t.Fatalf("jig review context exit = %d\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	done := b.builder.Jig("review", "done", b.fx.Ticket, "--result", b.cleanResult(t))
	if done.Code != 1 || !strings.Contains(done.Stdout, "REVIEW_NOT_INDEPENDENT") {
		t.Fatalf("jig review done by the builder exit = %d, want 1 and REVIEW_NOT_INDEPENDENT\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
	if !strings.Contains(done.Stdout, "independent: false") {
		t.Errorf("the output does not name the round as not independent:\n%s", done.Stdout)
	}
	rep := b.report(t, "1")
	if rep["verdict"] == "clean" || rep["independent"] != false {
		t.Errorf("report.yaml = %v, want a round that is not clean and independent: false", rep)
	}
	if !strings.Contains(string(readFileOrFatal(t, filepath.Join(b.roundDir("1"), "report.yaml"))), "independent: false") {
		t.Errorf("report.yaml does not say `independent: false`")
	}
	for _, l := range readJournal(t, b.fx, b.fx.Ticket) {
		if l.Event == journal.EventGateClean {
			t.Errorf("journal has %+v, want no clean round", l)
		}
	}

	pub := runJig(t, b.env, b.fx.StoreDir, "publish", b.fx.Ticket, "--body")
	if pub.Code == 0 || !strings.Contains(pub.Stdout, "PUBLISH_NOT_CLEAN") {
		t.Errorf("jig publish --body after a round that is not clean: exit = %d, want PUBLISH_NOT_CLEAN\nstdout:\n%s", pub.Code, pub.Stdout)
	}
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
	if _, err := gitx.RunReplaceEnv(fx.Dir, env.environ(), "config", "--global", "url."+filepath.ToSlash(fx.RepoRemote)+".insteadOf", remoteURL); err != nil {
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
	b.reviewedClean(t)

	gateLease := filepath.Join(env.home, "pool", "fixture-repo", b.fx.Ticket+"-gate")
	if _, err := os.Stat(gateLease); err != nil {
		t.Fatalf("test setup: the gate lease is not at %s: %v", gateLease, err)
	}
	leaseHead := gitLog(t, gateLease, "rev-parse", "HEAD")
	originBefore := originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch)
	callsBefore := len(ghCalls(t, log))

	body := runJig(t, env, b.builder.Clone, "publish", b.fx.Ticket, "--body")
	if body.Code != 0 || !strings.Contains(body.Stdout, "head: "+b.sha) || !strings.Contains(body.Stdout, "branch: "+b.branch) {
		t.Fatalf("jig publish --body exit = %d, want 0 and the reviewed head\nstdout:\n%s\nstderr:\n%s", body.Code, body.Stdout, body.Stderr)
	}
	prBody := filepath.Join(b.st.TicketDir(b.fx.Ticket), "pr", "fixture-repo.md")
	text := string(readFileOrFatal(t, prBody))
	for _, want := range []string{"## What changed", "## Verification", "Reviewed head:", b.sha[:7]} {
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
	if got := gitLog(t, gateLease, "rev-parse", "HEAD"); got != leaseHead {
		t.Errorf("the gate lease moved from %s to %s", leaseHead, got)
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

// TestPublishBare: bare `jig publish --yes` pushes the head the clean round
// reviewed exactly as it is - the same sha as the clone's, nothing reconciled,
// squashed or committed on top - from a branch that was never on origin, and
// opens the pull request through the host.
func TestPublishBare(t *testing.T) {
	t.Parallel()
	b := newBuiltTicket(t, true)
	env, log := githubClone(t, b)
	b.env = env
	b.builder.env = env
	b.openRound(t)
	b.reviewedClean(t)
	cloneLog := gitLog(t, b.fx.RepoDir, "log", "--format=%H", b.branch)

	// Without --yes (and with no approval) a bare publish asks, and stdin is
	// not a terminal: it is declined and pushes nothing.
	declined := runJig(t, env, b.builder.Clone, "publish", b.fx.Ticket)
	if declined.Code == 0 || !strings.Contains(declined.Stdout, "PUBLISH_DECLINED") {
		t.Errorf("jig publish without --yes: exit = %d, want PUBLISH_DECLINED\nstdout:\n%s", declined.Code, declined.Stdout)
	}
	if got := originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch); got != "" {
		t.Fatalf("a declined publish pushed %s to origin", got)
	}

	pub := runJig(t, env, b.builder.Clone, "publish", b.fx.Ticket, "--yes")
	if pub.Code != 0 || !strings.Contains(pub.Stdout, b.sha) || !strings.Contains(pub.Stdout, "opened") {
		t.Fatalf("jig publish --yes exit = %d, want 0, the head and an opened pull request\nstdout:\n%s\nstderr:\n%s", pub.Code, pub.Stdout, pub.Stderr)
	}
	if got := originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch); got != b.sha {
		t.Errorf("origin's %s = %s, want the reviewed head %s, unchanged", b.branch, got, b.sha)
	}
	if got := gitLog(t, b.fx.RepoRemote, "log", "--format=%H", b.branch); got != cloneLog {
		t.Errorf("origin's history of %s is\n%s\nwant the clone's\n%s", b.branch, got, cloneLog)
	}
	if got := h2tip(t, b); got != b.sha {
		t.Errorf("the clone's branch moved from %s to %s", b.sha, got)
	}
	var created string
	for _, c := range ghCalls(t, log) {
		if strings.HasPrefix(c, "pr create") {
			created = c
		}
	}
	if !strings.Contains(created, "--head "+b.branch) || !strings.Contains(created, "--base main") {
		t.Errorf("gh pr create call = %q, want --head %s --base main", created, b.branch)
	}
	var prLine journal.Line
	done := false
	for _, l := range readJournal(t, b.fx, b.fx.Ticket) {
		switch l.Event {
		case journal.EventPr:
			prLine = l
		case journal.EventPublishDone:
			done = true
		case "reconcile", "squash", "memorize", "revalidate":
			t.Errorf("publish journaled %+v, but it reconciles, squashes and memorizes nothing", l)
		}
	}
	if prLine.Outcome != "opened" || prLine.Commit != b.sha || prLine.Branch != b.branch || !strings.Contains(prLine.URL, "/pull/") || !done {
		t.Errorf("pr line = %+v, publish-done = %v, want an opened pull request for %s from %s", prLine, done, b.sha, b.branch)
	}
}
