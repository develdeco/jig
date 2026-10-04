package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// authorCommit is one commit of a branch built outside jig: its message and
// the files it writes (path -> full content).
type authorCommit struct {
	message string
	files   map[string]string
}

// authorBranch builds branch outside jig, the way its author would: a fresh
// clone of remote, one commit per entry of commits, pushed to remote under
// branch. It returns the pushed tip.
func authorBranch(t *testing.T, remote, branch string, commits ...authorCommit) string {
	t.Helper()
	dir := t.TempDir()
	gitLog(t, dir, "clone", remote, ".")
	gitLog(t, dir, "checkout", "-b", branch)
	for _, c := range commits {
		commitAuthored(t, dir, c)
	}
	gitLog(t, dir, "push", "origin", branch)
	return gitLog(t, dir, "rev-parse", "HEAD")
}

// authorPush is the author pushing again: one more commit c on top of branch as
// remote has it, a fast-forward of it. It returns the new tip.
func authorPush(t *testing.T, remote, branch string, c authorCommit) string {
	t.Helper()
	dir := t.TempDir()
	gitLog(t, dir, "clone", remote, ".")
	gitLog(t, dir, "checkout", branch)
	commitAuthored(t, dir, c)
	gitLog(t, dir, "push", "origin", branch)
	return gitLog(t, dir, "rev-parse", "HEAD")
}

// commitAuthored writes c's files in the clone at dir and commits them.
func commitAuthored(t *testing.T, dir string, c authorCommit) {
	t.Helper()
	for rel, content := range c.files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("commitAuthored: mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("commitAuthored: write %s: %v", rel, err)
		}
	}
	gitLog(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, pinnedIdentityEnv, "commit", "-m", c.message); err != nil {
		t.Fatalf("commitAuthored: commit %q: %v", c.message, err)
	}
}

const retrySource = `package alpha

// Retry calls fn up to attempts times and returns the last error.
func Retry(attempts int, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
	}
	return err
}
`

const retryTest = `package alpha

import (
	"errors"
	"testing"
)

func TestRetryStopsOnSuccess(t *testing.T) {
	calls := 0
	err := Retry(3, func() error {
		calls++
		if calls < 2 {
			return errors.New("flaky")
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("Retry = %v after %d calls, want nil after 2", err, calls)
	}
}
`

// retryGivesUpTest is what the author adds to retryTest when they push again
// mid-loop: a second test in the same file.
const retryGivesUpTest = `
func TestRetryReturnsTheLastError(t *testing.T) {
	want := errors.New("always")
	if err := Retry(2, func() error { return want }); err != want {
		t.Fatalf("Retry = %v, want the last error %v", err, want)
	}
}
`

// TestGateBranchRoundFixesBuildOnTheReviewedBranch is the hand-written-branch
// loop end to end, through the built binary and file-path remotes: an author
// builds branch add-retry outside jig and pushes it, main then moves past
// the branch's fork point, and jig gates the branch for a ticket minted
// afterwards. The author pushes once more while the round's fix is still to
// be built. The fix slice that round queues must be built by `jig run` on
// add-retry itself, on top of the author's latest commit, and the next gate
// must review add-retry again - not a jig/<ticket> branch cut from main,
// which lacks the author's code. `jig publish` then ships add-retry as it
// is: all the author's commits keep their shas, jig's fix and publish's own
// commits sit on top, origin's branch is fast-forwarded, and the store
// records it.
func TestGateBranchRoundFixesBuildOnTheReviewedBranch(t *testing.T) {
	fx, home := newFixture(t, fixture.Opts{})
	const ticket = "JIG-2" // the fixture's own ticket is JIG-1, so the next mint is JIG-2
	const branch = "add-retry"

	// The fixture repo's own alpha.go fails its Clamp test at main (a build
	// slice fixes it), so the author's branch fixes it first: the gate runs
	// every oracle over the branch it reviews, and this one has to pass them.
	alpha, err := os.ReadFile(filepath.Join(fx.RepoDir, "alpha", "alpha.go"))
	if err != nil {
		t.Fatalf("read the fixture's alpha.go: %v", err)
	}
	clamped := strings.Replace(string(alpha), "\treturn v\n", "\tif v > hi {\n\t\treturn hi\n\t}\n\treturn v\n", 1)
	if clamped == string(alpha) {
		t.Fatal("test setup: alpha.go has no Clamp to fix")
	}
	authorTip := authorBranch(t, fx.RepoRemote, branch,
		authorCommit{"fix Clamp's upper bound", map[string]string{"alpha/alpha.go": clamped}},
		authorCommit{"add Retry", map[string]string{"alpha/retry.go": retrySource, "alpha/retry_test.go": retryTest}},
	)
	// Main moves past the branch's fork point after the author pushed.
	scriptedPush(t, fx.RepoRemote, "beta/notes.txt", "main moved\n", "main moves past the fork point")
	movedMain := gitLog(t, fx.RepoRemote, "rev-parse", "refs/heads/main")
	if movedMain == gitLog(t, fx.RepoRemote, "merge-base", "refs/heads/main", authorTip) {
		t.Fatal("test setup: main did not move past the branch's fork point")
	}

	mint := runJig(t, fx.StoreDir, "ticket", "new", "--title", "Add retry")
	if mint.Code != 0 || !strings.Contains(mint.Stdout, "id: "+ticket) {
		t.Fatalf("jig ticket new exit = %d, want 0 minting %s\nstdout:\n%s\nstderr:\n%s", mint.Code, ticket, mint.Stdout, mint.Stderr)
	}
	// A branch built outside jig needs no brief, and the mint says so.
	if !strings.Contains(mint.Stdout, "jig gate "+ticket+" --branch <name>") {
		t.Fatalf("jig ticket new does not offer to adopt a branch\nstdout:\n%s", mint.Stdout)
	}

	// Round 1 over the author's branch: the scripted round queues fix-1.
	g1 := runJig(t, fx.StoreDir, "gate", ticket, "--branch", branch, "--scenario", fx.ScenarioDir)
	if g1.Code != 0 {
		t.Fatalf("jig gate --branch exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", g1.Code, g1.Stdout, g1.Stderr)
	}
	if !strings.Contains(g1.Stdout, "verdict: fix-slices") || !strings.Contains(g1.Stdout, "branch: "+branch) {
		t.Fatalf("round 1 did not queue a fix slice on the adopted branch:\n%s", g1.Stdout)
	}

	// The author pushes once more, mid-loop: the round has been read and its
	// fix is still to be built. Whatever jig builds next goes on top of this
	// commit, not of the tip the round reviewed.
	authorNext := authorPush(t, fx.RepoRemote, branch,
		authorCommit{"test that Retry returns the last error", map[string]string{"alpha/retry_test.go": retryTest + retryGivesUpTest}})
	if authorNext == authorTip {
		t.Fatal("test setup: the author's second push did not move the branch")
	}

	// The fix slice is built on the reviewed branch, as origin has it now.
	r := runJig(t, fx.StoreDir, "run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r.Code != 0 {
		t.Fatalf("jig run exit = %d, want 0 (fix-1 built)\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	lease := poolBuildLeaseDir(home, "fixture-repo", ticket)
	if got := gitLog(t, lease, "symbolic-ref", "--short", "HEAD"); got != branch {
		t.Fatalf("the build lease is on %q, want the reviewed branch %q", got, branch)
	}
	if got := gitLog(t, lease, "rev-parse", "HEAD~1"); got != authorNext {
		t.Fatalf("the fix commit's parent is %s, want the author's latest tip %s: the fix was not built on top of the author's commits, the mid-loop push included", got, authorNext)
	}
	if got := gitLog(t, lease, "log", "-1", "--format=%s"); !strings.HasPrefix(got, ticket+" fix-1: ") {
		t.Fatalf("the tip of %s is %q, want the fix slice's commit", branch, got)
	}
	// The author's branch on origin is untouched: jig commits stay in the
	// build lease until a publish pushes them.
	if got := gitLog(t, fx.RepoRemote, "rev-parse", "refs/heads/"+branch); got != authorNext {
		t.Fatalf("origin's %s = %s, want the author's latest tip %s unchanged", branch, got, authorNext)
	}

	// The ticket records the branch and the start sha the fix commit builds
	// on. Until jig builds on an adopted branch its start sha follows the
	// author's branch, so it is the tip the author pushed mid-loop - not the
	// tip at adoption, and not the moved main.
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	rec, err := st.ReadTicket(ticket)
	if err != nil || rec.Branch != branch || rec.Title != "Add retry" {
		t.Fatalf("ticket record = %+v (err %v), want title %q and branch %q", rec, err, "Add retry", branch)
	}
	start, err := os.ReadFile(filepath.Join(st.TicketDir(ticket), "start.fixture-repo.sha"))
	if err != nil || strings.TrimSpace(string(start)) != authorNext {
		t.Fatalf("start sha = %q (err %v), want the author's latest tip %s", start, err, authorNext)
	}

	// The next round, with no --branch, reviews the same branch: the build
	// lease's copy, fix commit included. The scripted source has no round 2,
	// so it is clean.
	g2 := runJig(t, fx.StoreDir, "gate", ticket, "--scenario", fx.ScenarioDir)
	if g2.Code != 0 || !strings.Contains(g2.Stdout, "verdict: clean") {
		t.Fatalf("jig gate round 2 exit = %d, want a clean round\nstdout:\n%s\nstderr:\n%s", g2.Code, g2.Stdout, g2.Stderr)
	}
	gateLease := filepath.Join(home, "pool", "fixture-repo", ticket+"-gate")
	if got := gitLog(t, gateLease, "symbolic-ref", "--short", "HEAD"); got != branch {
		t.Fatalf("the gate lease is on %q, want the reviewed branch %q", got, branch)
	}
	if got, want := gitLog(t, gateLease, "rev-parse", "HEAD"), gitLog(t, lease, "rev-parse", "HEAD"); got != want {
		t.Fatalf("round 2 reviewed %s, want the branch with the fix commit, %s", got, want)
	}

	// The clean round makes the ticket publishable like any other, adopted or
	// not, and status says so.
	s := runJig(t, fx.StoreDir, "status", ticket)
	if s.Code != 0 || !strings.Contains(s.Stdout, "branch: "+branch) || !strings.Contains(s.Stdout, "Run `jig publish "+ticket+"` to open or update the PR") {
		t.Fatalf("jig status exit = %d, want the branch named and `jig publish %s` suggested\nstdout:\n%s\nstderr:\n%s", s.Code, ticket, s.Stdout, s.Stderr)
	}

	// Publish ships the branch as it is: main has moved past the fork point, so
	// it merges main in, adds the memorize commit, and fast-forwards origin's
	// branch - the author's commits, the mid-loop one included, keep their
	// shas, jig's fix sits on top.
	jigFix := gitLog(t, lease, "rev-parse", "HEAD")
	p := runJig(t, fx.StoreDir, "publish", ticket, "--yes")
	if p.Code != 0 {
		t.Fatalf("jig publish exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", p.Code, p.Stdout, p.Stderr)
	}
	head := gitLog(t, fx.RepoRemote, "rev-parse", "refs/heads/"+branch)
	if want := "pushed[1]{repo,head,squash}:\n  fixture-repo," + head + ",not squashed (branch already on origin)\n"; !strings.Contains(p.Stdout, want) {
		t.Fatalf("jig publish did not report the push %q\nstdout:\n%s", want, p.Stdout)
	}
	if head == authorNext {
		t.Fatalf("origin's %s did not move: still the author's latest tip %s", branch, authorNext)
	}
	// Fast-forwarded: the author's latest tip is an ancestor of the new head,
	// and the commits on top are, in order, jig's fix, the merge of main, and
	// the memorize commit.
	gitLog(t, fx.RepoRemote, "merge-base", "--is-ancestor", authorNext, head)
	fork := gitLog(t, fx.RepoRemote, "merge-base", "refs/heads/main", authorTip)
	authorCommits := strings.Fields(gitLog(t, fx.RepoRemote, "rev-list", "--reverse", fork+".."+authorNext))
	if len(authorCommits) != 3 || authorCommits[1] != authorTip || authorCommits[2] != authorNext {
		t.Fatalf("test setup: the author built %v, want three commits ending in the adoption tip %s and the mid-loop push %s", authorCommits, authorTip, authorNext)
	}
	for _, c := range authorCommits {
		gitLog(t, fx.RepoRemote, "merge-base", "--is-ancestor", c, head)
	}
	if got := strings.Fields(gitLog(t, fx.RepoRemote, "rev-list", "--reverse", "--first-parent", fork+".."+head)); len(got) < 3 || got[0] != authorCommits[0] || got[1] != authorCommits[1] || got[2] != authorCommits[2] {
		t.Fatalf("origin's %s starts with %v, want the author's commits %v under the same shas", branch, got, authorCommits)
	}
	added := strings.Fields(gitLog(t, fx.RepoRemote, "rev-list", "--first-parent", "--reverse", authorNext+".."+head))
	if len(added) != 3 || added[0] != jigFix {
		t.Fatalf("publish added %v on top of the author's latest tip, want jig's fix %s, the merge of main, and the memorize commit", added, jigFix)
	}
	if subject := gitLog(t, fx.RepoRemote, "log", "-1", "--format=%s", added[1]); !strings.HasPrefix(subject, "Merge ") {
		t.Errorf("the commit after jig's fix is %q, want the merge of main", subject)
	}
	if subject := gitLog(t, fx.RepoRemote, "log", "-1", "--format=%s", head); subject != "docs: memorize "+ticket {
		t.Errorf("the tip of %s is %q, want the memorize commit", branch, subject)
	}
	gitLog(t, fx.RepoRemote, "merge-base", "--is-ancestor", movedMain, head)
	if got := gitLog(t, fx.RepoRemote, "rev-parse", "refs/heads/main"); got != movedMain {
		t.Fatalf("origin's main = %s, want %s: publish never moves the target", got, movedMain)
	}
	if got := gitLog(t, fx.RepoRemote, "for-each-ref", "--format=%(refname)", "refs/heads/jig/"); got != "" {
		t.Fatalf("origin has %q, want no jig/<ticket> branch: an adopted ticket ships its own", got)
	}

	// The store records the publish, and that nothing was squashed.
	lines := readJournal(t, fx, ticket)
	if l, ok := findJournalLine(lines, "squash", "", "none:branch-on-origin"); !ok || l.Commit != "" {
		t.Errorf("journal has no squash line recording none:branch-on-origin, or it names a commit: %+v", l)
	}
	if l, ok := findJournalLine(lines, "pr", "", ""); !ok || l.Commit != head {
		t.Errorf("journal pr line = %+v (found %v), want the pushed head %s", l, ok, head)
	}
	if _, ok := findJournalLine(lines, "publish-done", "", ""); !ok {
		t.Errorf("journal has no publish-done line: %+v", lines)
	}
	if ledger := string(readFileOrFatal(t, joinPath(fx.StoreDir, "ledger.md"))); !strings.Contains(ledger, "## "+ticket+" - Add retry") {
		t.Errorf("ledger.md has no entry for %s titled with its recorded title:\n%s", ticket, ledger)
	}

	// The ticket goes on working: jig's commits are on origin under their own
	// shas, so a round over the branch reviews origin's copy, which holds them.
	g3 := runJig(t, fx.StoreDir, "gate", ticket, "--scenario", fx.ScenarioDir)
	if g3.Code != 0 || !strings.Contains(g3.Stdout, "verdict: clean") {
		t.Fatalf("jig gate after the publish exit = %d, want a clean round\nstdout:\n%s\nstderr:\n%s", g3.Code, g3.Stdout, g3.Stderr)
	}
	if got := gitLog(t, gateLease, "rev-parse", "HEAD"); got != head {
		t.Fatalf("the round after the publish reviewed %s, want origin's copy, %s", got, head)
	}
}
