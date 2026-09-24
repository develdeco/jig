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

// TestRunRecoversBrokenLeaseInsideEnclosingRepo reproduces the pool's
// enclosing-repository hazard end to end: JIG_HOME sits inside another git
// working copy with its own origin (the default ~/.config/jig inside a
// dotfiles checkout), and the ticket's build lease is a .git directory git
// cannot open (a lease deleted by hand and stopped by a locked pack file).
// Git's upward discovery must never reach the enclosing repository: `jig
// run` leaves its branch, refs and uncommitted work alone, moves the broken
// lease aside instead of deleting it, and builds in a fresh clone.
func TestRunRecoversBrokenLeaseInsideEnclosingRepo(t *testing.T) {
	enclosing := newEnclosingRepo(t)
	home := filepath.Join(enclosing, "jig-home")
	t.Setenv("JIG_HOME", home)
	fx := fixture.Generate(t, fixture.Opts{})

	lease := poolBuildLeaseDir(home, "fixture-repo", fx.Ticket)
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

	r := runJig(t, fx.StoreDir, "run", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)

	if got := gitLog(t, enclosing, "symbolic-ref", "--short", "HEAD"); got != "main" {
		t.Errorf("enclosing repo HEAD = %q, want main: jig checked out a branch in it", got)
	}
	if _, err := gitx.Run(enclosing, "rev-parse", "--verify", "--quiet", "refs/heads/jig/"+fx.Ticket); err == nil {
		t.Errorf("enclosing repo gained branch jig/%s: jig ran its lease checkout there", fx.Ticket)
	}
	if got, err := os.ReadFile(notes); err != nil || string(got) != "uncommitted work\n" {
		t.Errorf("enclosing repo's uncommitted edit = %q, %v; want it untouched", got, err)
	}

	if r.Code != 2 {
		t.Fatalf("jig run exit = %d, want 2 (paused at slice c, as on a clean pool)\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
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
	if got := gitLog(t, lease, "symbolic-ref", "--short", "HEAD"); got != "jig/"+fx.Ticket {
		t.Fatalf("lease HEAD = %q, want jig/%s", got, fx.Ticket)
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

// TestRunRefusesCorruptHEADBuildLeaseInsideEnclosingRepo reproduces the same
// enclosing-repository hazard as TestRunRecoversBrokenLeaseInsideEnclosingRepo,
// for a build lease whose .git has everything a repository needs except a
// HEAD git can read (truncated to empty, as a crash mid-write can leave it)
// after slices a, b and d are already committed there and the run is paused
// on a question. Git's own upward discovery does not fail on this: from the
// lease it walks past the broken .git and answers for the enclosing
// repository instead. `jig run --answer` must stop with an error rather
// than read that answer as "the lease is not a repository of its own":
// moving it aside would drop a, b and d from the ticket branch while the
// store still calls them green.
func TestRunRefusesCorruptHEADBuildLeaseInsideEnclosingRepo(t *testing.T) {
	enclosing := newEnclosingRepo(t)
	home := filepath.Join(enclosing, "jig-home")
	t.Setenv("JIG_HOME", home)
	fx := fixture.Generate(t, fixture.Opts{})

	lease := poolBuildLeaseDir(home, "fixture-repo", fx.Ticket)
	r1 := runJig(t, fx.StoreDir, "run", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r1.Code != 2 {
		t.Fatalf("run 1 exit %d, want 2 (paused on a question with a, b, d green)\nstdout:\n%s\nstderr:\n%s", r1.Code, r1.Stdout, r1.Stderr)
	}
	before := gitLog(t, lease, "log", "--format=%s", "jig/"+fx.Ticket)

	if err := os.WriteFile(filepath.Join(lease, ".git", "HEAD"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(enclosing, "notes.txt")
	if err := os.WriteFile(notes, []byte("uncommitted work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r2 := runJig(t, fx.StoreDir, "run", fx.Ticket, "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r2.Code == 0 {
		t.Fatalf("run 2 (continuing over a corrupt-HEAD lease) exit 0, want an error\nstdout:\n%s\nstderr:\n%s", r2.Stdout, r2.Stderr)
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

	// Repair HEAD by hand, as a person would, and confirm a, b and d were
	// never touched.
	if err := os.WriteFile(filepath.Join(lease, ".git", "HEAD"), []byte("ref: refs/heads/jig/"+fx.Ticket+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := gitLog(t, lease, "log", "--format=%s", "jig/"+fx.Ticket); after != before {
		t.Fatalf("build lease log after repair = %q, want unchanged from before the corruption:\n%q", after, before)
	}
}

// TestReservedLeaseSuffixTicketRefused reproduces the lease-key collision:
// ticket X's gate lease is pool/<repo>/X-gate and its publish lease is
// pool/<repo>/X-publish, so a ticket named X-gate would build in the
// directory X's gate resets and cleans. Every command that takes a ticket
// must refuse such an id before any lease exists.
func TestReservedLeaseSuffixTicketRefused(t *testing.T) {
	fx, home := newFixture(t, fixture.Opts{})

	for _, id := range []string{fx.Ticket + "-gate", fx.Ticket + "-GATE", fx.Ticket + "-publish"} {
		src := filepath.Join(fx.StoreDir, fx.Ticket)
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
			{"run", id, "--backend", "fake", "--scenario", fx.ScenarioDir},
			{"gate", id, "--scenario", fx.ScenarioDir},
			{"publish", id, "--yes"},
			{"status", id},
		} {
			r := runJig(t, fx.StoreDir, args...)
			if r.Code == 0 || !strings.Contains(r.Stdout, "reserves") || !strings.Contains(r.Stdout, id) {
				t.Errorf("jig %s: exit %d, want a non-zero exit naming %s as a reserved id\nstdout:\n%s\nstderr:\n%s",
					strings.Join(args, " "), r.Code, id, r.Stdout, r.Stderr)
			}
		}

		if _, err := os.Stat(poolBuildLeaseDir(home, "fixture-repo", id)); !os.IsNotExist(err) {
			t.Errorf("a lease exists at %s after refusing ticket %s (stat err %v)", poolBuildLeaseDir(home, "fixture-repo", id), id, err)
		}
	}
}
