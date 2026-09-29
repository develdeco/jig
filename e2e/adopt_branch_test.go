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
		for rel, content := range c.files {
			full := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatalf("authorBranch: mkdir: %v", err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatalf("authorBranch: write %s: %v", rel, err)
			}
		}
		gitLog(t, dir, "add", "-A")
		if _, err := gitx.RunEnv(dir, pinnedIdentityEnv, "commit", "-m", c.message); err != nil {
			t.Fatalf("authorBranch: commit %q: %v", c.message, err)
		}
	}
	gitLog(t, dir, "push", "origin", branch)
	return gitLog(t, dir, "rev-parse", "HEAD")
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

// TestGateBranchRoundFixesBuildOnTheReviewedBranch is the hand-written-branch
// loop end to end, through the built binary and file-path remotes: an author
// builds branch add-retry outside jig and pushes it, main then moves past
// the branch's fork point, and jig gates the branch for a ticket minted
// afterwards. The fix slice that round queues must be built by `jig run` on
// add-retry itself, on top of the author's commit, and the next gate must
// review add-retry again - not a jig/<ticket> branch cut from main, which
// lacks the author's code.
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

	// Round 1 over the author's branch: the scripted round queues fix-1.
	g1 := runJig(t, fx.StoreDir, "gate", ticket, "--branch", branch, "--scenario", fx.ScenarioDir)
	if g1.Code != 0 {
		t.Fatalf("jig gate --branch exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", g1.Code, g1.Stdout, g1.Stderr)
	}
	if !strings.Contains(g1.Stdout, "verdict: fix-slices") {
		t.Fatalf("round 1 did not queue a fix slice:\n%s", g1.Stdout)
	}

	// The fix slice is built on the reviewed branch.
	r := runJig(t, fx.StoreDir, "run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r.Code != 0 {
		t.Fatalf("jig run exit = %d, want 0 (fix-1 built)\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	lease := poolBuildLeaseDir(home, "fixture-repo", ticket)
	if got := gitLog(t, lease, "symbolic-ref", "--short", "HEAD"); got != branch {
		t.Fatalf("the build lease is on %q, want the reviewed branch %q", got, branch)
	}
	if got := gitLog(t, lease, "rev-parse", "HEAD~1"); got != authorTip {
		t.Fatalf("the fix commit's parent is %s, want the author's tip %s: the fix was not built on top of the author's commits", got, authorTip)
	}
	if got := gitLog(t, lease, "log", "-1", "--format=%s"); !strings.HasPrefix(got, ticket+" fix-1: ") {
		t.Fatalf("the tip of %s is %q, want the fix slice's commit", branch, got)
	}
	// The author's branch on origin is untouched: jig commits stay in the
	// build lease until a publish pushes them.
	if got := gitLog(t, fx.RepoRemote, "rev-parse", "refs/heads/"+branch); got != authorTip {
		t.Fatalf("origin's %s = %s, want the author's tip %s unchanged", branch, got, authorTip)
	}

	// The ticket records the branch and the start sha the fix commit builds
	// on: the author's tip at adoption, not the moved main.
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	rec, err := st.ReadTicket(ticket)
	if err != nil || rec.Branch != branch || rec.Title != "Add retry" {
		t.Fatalf("ticket record = %+v (err %v), want title %q and branch %q", rec, err, "Add retry", branch)
	}
	start, err := os.ReadFile(filepath.Join(st.TicketDir(ticket), "start.fixture-repo.sha"))
	if err != nil || strings.TrimSpace(string(start)) != authorTip {
		t.Fatalf("start sha = %q (err %v), want the author's tip %s", start, err, authorTip)
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
}
