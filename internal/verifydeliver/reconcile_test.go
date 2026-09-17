package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// tinyRepo creates a minimal git repo committed on main, with a bare
// remote, for reconcile tests that don't need the full fixture.
func tinyRepo(t *testing.T) (repoDir, remoteDir string) {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-b", "main")
	run(t, dir, "config", "user.name", "jig-fixture")
	run(t, dir, "config", "user.email", "fixture@example.invalid")
	writeAndCommit(t, dir, "f.txt", "one", "init")

	remote := filepath.Join(t.TempDir(), "remote.git")
	run(t, dir, "clone", "--bare", dir, remote)
	run(t, dir, "remote", "add", "origin", remote)
	run(t, dir, "push", "origin", "main")
	return dir, remote
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v (in %s): %v", args, dir, err)
	}
	return out
}

func writeAndCommit(t *testing.T, dir, name, content, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-m", msg)
	return run(t, dir, "rev-parse", "HEAD")
}

// cloneFrom clones remote into a fresh temp dir, returning its path.
func cloneFrom(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	run(t, filepath.Dir(dir), "clone", remote, dir)
	run(t, dir, "config", "user.name", "jig-fixture")
	run(t, dir, "config", "user.email", "fixture@example.invalid")
	return dir
}

func TestReconcilePoliciesLocalOnly(t *testing.T) {
	_, remote := tinyRepo(t)

	// A ticket branch created locally in a clone, never pushed, while
	// main moves ahead on the remote.
	clone := cloneFrom(t, remote)
	run(t, clone, "checkout", "-b", "jig/T-1")
	writeAndCommit(t, clone, "g.txt", "branch work", "branch work")

	advance := cloneFrom(t, remote)
	writeAndCommit(t, advance, "h.txt", "target moved", "target moved")
	run(t, advance, "push", "origin", "main")

	run(t, clone, "fetch", "origin")
	beforeCount := len(strings.Split(run(t, clone, "log", "--format=%H"), "\n"))

	policy, err := reconcile(clone, "T-1", "main")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if policy != policyLocalRebase {
		t.Fatalf("policy = %q, want %q", policy, policyLocalRebase)
	}

	// Linear history: no merge commit, exactly one more commit than
	// before (the rebased branch commit) plus main's advance.
	log := run(t, clone, "log", "--format=%H %P")
	for _, line := range strings.Split(log, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 2 {
			t.Fatalf("found a merge commit in a linear rebase: %q", line)
		}
	}
	afterCount := len(strings.Split(log, "\n"))
	if afterCount != beforeCount+1 {
		t.Fatalf("commit count after rebase = %d, want %d", afterCount, beforeCount+1)
	}
}

func TestReconcilePoliciesPrePushed(t *testing.T) {
	_, remote := tinyRepo(t)

	clone := cloneFrom(t, remote)
	run(t, clone, "checkout", "-b", "jig/T-1")
	writeAndCommit(t, clone, "g.txt", "branch work", "branch work")
	run(t, clone, "push", "origin", "jig/T-1")
	pushedHead := run(t, clone, "rev-parse", "HEAD")

	advance := cloneFrom(t, remote)
	writeAndCommit(t, advance, "h.txt", "target moved", "target moved")
	run(t, advance, "push", "origin", "main")

	run(t, clone, "fetch", "origin")
	policy, err := reconcile(clone, "T-1", "main")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if policy != policyMerge {
		t.Fatalf("policy = %q, want %q", policy, policyMerge)
	}

	// A merge commit exists, and the previously pushed head is still
	// reachable unrewritten.
	parents := strings.Fields(run(t, clone, "log", "-1", "--format=%P"))
	if len(parents) != 2 {
		t.Fatalf("HEAD parents = %v, want a 2-parent merge commit", parents)
	}
	if _, err := gitx.Run(clone, "merge-base", "--is-ancestor", pushedHead, "HEAD"); err != nil {
		t.Fatalf("previously pushed head %s is not an ancestor of HEAD: %v", pushedHead, err)
	}
	remoteHead := run(t, clone, "rev-parse", "refs/remotes/origin/jig/T-1")
	if remoteHead != pushedHead {
		t.Fatalf("pushed history was rewritten: origin/jig/T-1 = %s, want unchanged %s", remoteHead, pushedHead)
	}
}

func TestRebaseOnto(t *testing.T) {
	_, remote := tinyRepo(t)
	clone := cloneFrom(t, remote)

	oldBaseSHA := run(t, clone, "rev-parse", "HEAD")
	run(t, clone, "checkout", "-b", "feature")
	writeAndCommit(t, clone, "feature.txt", "feature work", "feature work")
	featureSummary := run(t, clone, "log", "-1", "--format=%s")

	run(t, clone, "checkout", "main")
	writeAndCommit(t, clone, "newbase.txt", "new base work", "new base work")
	newBaseSHA := run(t, clone, "rev-parse", "HEAD")

	if err := RebaseOnto(clone, newBaseSHA, oldBaseSHA, "feature"); err != nil {
		t.Fatalf("RebaseOnto: %v", err)
	}

	parent := run(t, clone, "rev-parse", "HEAD~1")
	if parent != newBaseSHA {
		t.Fatalf("feature's new parent = %s, want new base %s", parent, newBaseSHA)
	}
	gotSummary := run(t, clone, "log", "-1", "--format=%s")
	if gotSummary != featureSummary {
		t.Fatalf("feature commit summary = %q, want %q (stacked, not squashed)", gotSummary, featureSummary)
	}
}
