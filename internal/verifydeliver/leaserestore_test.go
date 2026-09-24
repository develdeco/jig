package verifydeliver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/pool"
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

// TestGateBrokenLeaseNeverResetsEnclosingRepo reproduces the data-loss path
// where JIG_HOME sits inside another git working copy, and a gate lease
// whose .git entry is not a repository. Neither the pre-Acquire restore nor
// pool.Acquire may run in the enclosing repo, so its branch and its
// uncommitted edit survive; Acquire moves the broken lease aside and clones
// afresh, so the gate completes its round.
func TestGateBrokenLeaseNeverResetsEnclosingRepo(t *testing.T) {
	enclosing := t.TempDir()
	commitOneFile(t, enclosing, "notes.txt", "committed\n")
	jigHome := filepath.Join(enclosing, "jig-home")
	t.Setenv("JIG_HOME", jigHome)

	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	gateLease, err := pool.Dir("fixture-repo", fx.Ticket, pool.Gate)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(gateLease, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	notes := filepath.Join(enclosing, "notes.txt")
	if err := os.WriteFile(notes, []byte("uncommitted work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, gateErr := Gate(newDeps(t, fx), alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})

	got, err := os.ReadFile(notes)
	if err != nil {
		t.Fatalf("enclosing repo's file is gone: %v", err)
	}
	if string(got) != "uncommitted work\n" {
		t.Fatalf("enclosing repo's uncommitted edit was discarded: %q", got)
	}
	if head, err := gitx.Run(enclosing, "symbolic-ref", "--short", "HEAD"); err != nil || head != "main" {
		t.Fatalf("enclosing repo HEAD = %q (%v), want main", head, err)
	}
	if gateErr != nil {
		t.Fatalf("Gate over a broken gate lease should recover, got: %v", gateErr)
	}
	if report.Round != 1 || report.Verdict != "clean" {
		t.Fatalf("report = %+v, want round 1 clean", report)
	}
}

// TestGateRecoversFromUnbornLease reproduces the wedge left by a gate
// lease from a clone killed before its first checkout (origin configured,
// HEAD unborn). The pre-Acquire restore must skip it (pool.Usable needs a
// commit), Acquire reuses it in place, where its checkout repairs HEAD, and
// the gate completes its round.
func TestGateRecoversFromUnbornLease(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	gateLease, err := pool.Dir("fixture-repo", fx.Ticket, pool.Gate)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
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
	if asides, _ := filepath.Glob(gateLease + ".broken-*"); len(asides) != 0 {
		t.Fatalf("a gate lease with an unborn HEAD was moved aside: %v", asides)
	}
}
