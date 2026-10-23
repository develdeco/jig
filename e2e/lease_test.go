package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
)

// newEnclosingRepo creates a committed git working copy with its own origin,
// standing in for a dotfiles checkout that holds ~/.config/jig. It returns
// the working copy's path; notes.txt is its one tracked file.
func newEnclosingRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitLog(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	gitLog(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, pinnedIdentityEnv, "commit", "-m", "dotfiles"); err != nil {
		t.Fatalf("commit enclosing repo: %v", err)
	}
	bare := filepath.Join(t.TempDir(), "dotfiles.git")
	gitLog(t, filepath.Dir(bare), "clone", "--bare", dir, bare)
	gitLog(t, dir, "remote", "add", "origin", bare)
	gitLog(t, dir, "fetch", "origin")
	return dir
}

// gateLeaseDir is the ticket's gate lease: where a gate round opens its pristine
// copy of the branch for the reviewer.
func gateLeaseDir(home, ticket string) string {
	return filepath.Join(home, "pool", "fixture-repo", ticket+"-gate")
}

// TestGateRecoversBrokenLeaseInsideEnclosingRepo reproduces the pool's
// enclosing-repository hazard end to end: JIG_HOME sits inside another git
// working copy with its own origin (the default ~/.config/jig inside a
// dotfiles checkout), and the ticket's gate lease is a .git directory git
// cannot open (a lease deleted by hand and stopped by a locked pack file).
// Git's upward discovery must never reach the enclosing repository: `jig
// gate` leaves its branch, refs and uncommitted work alone, moves the broken
// lease aside instead of deleting it, and opens the round in a fresh clone.
func TestGateRecoversBrokenLeaseInsideEnclosingRepo(t *testing.T) {
	t.Parallel()
	enclosing := newEnclosingRepo(t)
	home := filepath.Join(enclosing, "jig-home")
	env := jigEnv{home: home}
	fx := fixture.Generate(t, fixture.Opts{Home: home})
	b := buildTicketOn(t, fx, env, false)

	lease := gateLeaseDir(home, fx.Ticket)
	pack := filepath.Join(lease, ".git", "objects", "pack", "pack-leftover.pack")
	if err := os.MkdirAll(filepath.Dir(pack), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pack, []byte("left behind by a locked file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(enclosing, "notes.txt")
	if err := os.WriteFile(notes, []byte("uncommitted work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := b.builder.Jig("gate", fx.Ticket, "--early")

	if got := gitLog(t, enclosing, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Errorf("enclosing repo HEAD = %q, want main: jig checked out a branch in it", got)
	}
	if _, err := gitx.Run(enclosing, "rev-parse", "--verify", "--quiet", "refs/heads/"+b.branch); err == nil {
		t.Errorf("enclosing repo gained branch %s: jig ran its lease checkout there", b.branch)
	}
	if got, err := os.ReadFile(notes); err != nil || string(got) != "uncommitted work\n" {
		t.Errorf("enclosing repo's uncommitted edit = %q, %v; want it untouched", got, err)
	}

	if r.Code != 0 {
		t.Fatalf("jig gate exit = %d, want 0 (the round opens, as on a clean pool)\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	top := gitLog(t, lease, "rev-parse", "--show-toplevel")
	topInfo, err := os.Stat(top)
	if err != nil {
		t.Fatalf("stat lease toplevel %q: %v", top, err)
	}
	leaseInfo, err := os.Stat(lease)
	if err != nil {
		t.Fatalf("stat lease: %v", err)
	}
	if !os.SameFile(topInfo, leaseInfo) {
		t.Fatalf("lease toplevel = %q, want the lease itself (%s)", top, lease)
	}
	if got := gitLog(t, lease, "rev-parse", "HEAD"); got != b.sha {
		t.Fatalf("lease HEAD = %q, want the reviewed head %s", got, b.sha)
	}

	asides, err := filepath.Glob(lease + ".broken-*")
	if err != nil || len(asides) != 1 {
		t.Fatalf("moved-aside leases = %v (%v), want exactly one %s.broken-<time>", asides, err, filepath.Base(lease))
	}
	kept := filepath.Join(asides[0], ".git", "objects", "pack", "pack-leftover.pack")
	if got, err := os.ReadFile(kept); err != nil || string(got) != "left behind by a locked file\n" {
		t.Fatalf("moved-aside lease lost its contents: %q, %v", got, err)
	}
	if !strings.Contains(r.Stderr, asides[0]) {
		t.Fatalf("stderr does not name the moved-aside lease %s:\n%s", asides[0], r.Stderr)
	}
}

// TestGateRefusesCorruptHEADLeaseInsideEnclosingRepo reproduces the same
// enclosing-repository hazard as TestGateRecoversBrokenLeaseInsideEnclosingRepo,
// for a gate lease whose .git has everything a repository needs except a HEAD
// git can read (truncated to empty, as a crash mid-write can leave it). Git's
// own upward discovery does not fail on this: from the lease it walks past the
// broken .git and answers for the enclosing repository instead. `jig gate`
// must stop with an error rather than read that answer as "the lease is not a
// repository of its own", and leave the lease where it is: a lease git
// refuses is the person's to look at, not jig's to move aside.
func TestGateRefusesCorruptHEADLeaseInsideEnclosingRepo(t *testing.T) {
	t.Parallel()
	enclosing := newEnclosingRepo(t)
	home := filepath.Join(enclosing, "jig-home")
	env := jigEnv{home: home}
	fx := fixture.Generate(t, fixture.Opts{Home: home})
	b := buildTicketOn(t, fx, env, false)

	lease := gateLeaseDir(home, fx.Ticket)
	if r1 := b.builder.Jig("gate", fx.Ticket, "--early"); r1.Code != 0 {
		t.Fatalf("jig gate exit %d, want 0 (the round opens)\nstdout:\n%s\nstderr:\n%s", r1.Code, r1.Stdout, r1.Stderr)
	}
	before := gitLog(t, lease, "log", "--format=%s", b.branch)

	if err := os.WriteFile(filepath.Join(lease, ".git", "HEAD"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(enclosing, "notes.txt")
	if err := os.WriteFile(notes, []byte("uncommitted work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r2 := b.builder.Jig("gate", fx.Ticket, "--early")
	if r2.Code == 0 {
		t.Fatalf("jig gate over a corrupt-HEAD lease exit 0, want an error\nstdout:\n%s\nstderr:\n%s", r2.Stdout, r2.Stderr)
	}
	if asides, _ := filepath.Glob(lease + ".broken-*"); len(asides) != 0 {
		t.Fatalf("a lease git refuses was moved aside: %v", asides)
	}
	if got := gitLog(t, enclosing, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Errorf("enclosing repo HEAD = %q, want main: jig checked out a branch in it", got)
	}
	if got, err := os.ReadFile(notes); err != nil || string(got) != "uncommitted work\n" {
		t.Errorf("enclosing repo's uncommitted edit = %q, %v; want it untouched", got, err)
	}

	// Repair HEAD by hand, as a person would, and confirm the lease was never
	// touched.
	if err := os.WriteFile(filepath.Join(lease, ".git", "HEAD"), []byte("ref: refs/heads/"+b.branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := gitLog(t, lease, "log", "--format=%s", b.branch); after != before {
		t.Fatalf("gate lease log after repair = %q, want unchanged from before the corruption:\n%q", after, before)
	}
}

// TestReservedLeaseSuffixTicketRefused reproduces the lease-key collision:
// ticket X's gate lease is pool/<repo>/X-gate and its publish lease is
// pool/<repo>/X-publish, so a ticket named X-gate would build in the
// directory X's gate resets and cleans. Every command that takes a ticket
// must refuse such an id before any lease exists.
func TestReservedLeaseSuffixTicketRefused(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})

	for _, id := range []string{fx.Ticket + "-gate", fx.Ticket + "-GATE", fx.Ticket + "-publish"} {
		src := filepath.Join(fx.StoreDir, "tickets", fx.Ticket)
		dst := filepath.Join(fx.StoreDir, id)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"brief.md", "slices.yaml"} {
			data, err := os.ReadFile(filepath.Join(src, name))
			if err != nil {
				t.Fatalf("read fixture %s: %v", name, err)
			}
			if err := os.WriteFile(filepath.Join(dst, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}

		for _, args := range [][]string{
			{"validate", id},
			{"gate", id, "--scenario", fx.ScenarioDir},
			{"publish", id, "--yes"},
			{"status", id},
		} {
			r := runJig(t, env, fx.StoreDir, args...)
			if r.Code == 0 || !strings.Contains(r.Stdout, "reserves") || !strings.Contains(r.Stdout, id) {
				t.Errorf("jig %s: exit %d, want a non-zero exit naming %s as a reserved id\nstdout:\n%s\nstderr:\n%s",
					strings.Join(args, " "), r.Code, id, r.Stdout, r.Stderr)
			}
		}

		if _, err := os.Stat(poolBuildLeaseDir(env.home, "fixture-repo", id)); !os.IsNotExist(err) {
			t.Errorf("a lease exists at %s after refusing ticket %s (stat err %v)", poolBuildLeaseDir(env.home, "fixture-repo", id), id, err)
		}
	}
}
