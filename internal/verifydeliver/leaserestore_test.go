package verifydeliver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
)

// commitOneFile initializes dir as a git repo with a single committed file.
func commitOneFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if _, err := gitx.Run(dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if _, err := gitx.Run(dir, "add", "-A"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "-m", "seed"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
}

// TestIsOwnGitRepoWithHead pins the guard in front of the pre-Acquire
// restore: only a directory that is itself a git top level with a commit
// checked out may be reset. A `.git` entry git cannot open (it would resolve
// an enclosing repo) and an unborn HEAD (a clone killed before checkout)
// must both be left alone.
func TestIsOwnGitRepoWithHead(t *testing.T) {
	enclosing := t.TempDir()
	commitOneFile(t, enclosing, "keep.txt", "keep\n")
	if !isOwnGitRepoWithHead(enclosing) {
		t.Fatalf("a committed repo at its own top level must qualify")
	}

	broken := filepath.Join(enclosing, "pool", "repo", "T-1-gate")
	if err := os.MkdirAll(filepath.Join(broken, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if isOwnGitRepoWithHead(broken) {
		t.Fatalf("a .git entry git cannot open resolves the enclosing repo; it must not qualify")
	}

	unborn := t.TempDir()
	if _, err := gitx.Run(unborn, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if isOwnGitRepoWithHead(unborn) {
		t.Fatalf("a repo with an unborn HEAD must not qualify")
	}

	if isOwnGitRepoWithHead(t.TempDir()) {
		t.Fatalf("a plain directory must not qualify")
	}
}

// TestGateBrokenLeaseNeverResetsEnclosingRepo reproduces NM6's data-loss
// path: JIG_HOME inside another git working copy, and a gate lease whose
// .git entry is not a repository. The pre-Acquire restore must not run in
// the enclosing repo, so its uncommitted edit survives whatever the gate
// does next.
func TestGateBrokenLeaseNeverResetsEnclosingRepo(t *testing.T) {
	enclosing := t.TempDir()
	commitOneFile(t, enclosing, "notes.txt", "committed\n")
	jigHome := filepath.Join(enclosing, "jig-home")
	t.Setenv("JIG_HOME", jigHome)

	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	poolDir, err := home.PoolDir()
	if err != nil {
		t.Fatalf("PoolDir: %v", err)
	}
	gateLease := filepath.Join(poolDir, "fixture-repo", fx.Ticket+"-gate")
	if err := os.MkdirAll(filepath.Join(gateLease, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	notes := filepath.Join(enclosing, "notes.txt")
	if err := os.WriteFile(notes, []byte("uncommitted work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The gate itself may fail here (pool.Acquire meets the broken lease);
	// what must hold is that nothing reset or cleaned the enclosing repo.
	_, _ = Gate(newDeps(t, fx), alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})

	got, err := os.ReadFile(notes)
	if err != nil {
		t.Fatalf("enclosing repo's file is gone: %v", err)
	}
	if string(got) != "uncommitted work\n" {
		t.Fatalf("enclosing repo's uncommitted edit was discarded: %q", got)
	}
}

// TestGateRecoversFromUnbornLease reproduces NM6's wedge: a gate lease left
// by a clone killed before its first checkout (origin configured, HEAD
// unborn). The pre-Acquire restore must skip it, so Acquire fetches and
// checks out as usual and the gate completes its round.
func TestGateRecoversFromUnbornLease(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	poolDir, err := home.PoolDir()
	if err != nil {
		t.Fatalf("PoolDir: %v", err)
	}
	gateLease := filepath.Join(poolDir, "fixture-repo", fx.Ticket+"-gate")
	if err := os.MkdirAll(gateLease, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(gateLease, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := gitx.Run(gateLease, "remote", "add", "origin", fx.RepoRemote); err != nil {
		t.Fatalf("git remote add: %v", err)
	}

	report, err := Gate(newDeps(t, fx), alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate over an unborn gate lease should recover, got: %v", err)
	}
	if report.Round != 1 || report.Verdict != "clean" {
		t.Fatalf("report = %+v, want round 1 clean", report)
	}
}
