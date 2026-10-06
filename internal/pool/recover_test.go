package pool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// TestAcquireRecoversWhatACrashedSessionLeftUnfinished: a reused lease that
// an earlier session left mid-operation (stopped on a conflict, as a session
// that died there leaves it) is acquired with the operation ended and named
// in Lease.Recovered, a clean tree, and every commit the branch held before
// the acquire still on it: am, cherry-pick and revert commit as they go, so
// ending them must keep what they applied, and a rebase's progress never
// reaches the branch. A later acquire finds nothing to recover.
func TestAcquireRecoversWhatACrashedSessionLeftUnfinished(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// start leaves the lease mid-operation on the lease's branch and
		// returns the commit the branch must still point at afterwards.
		start func(t *testing.T, r leaseRepo) string
		want  string
	}{
		{"merge", func(t *testing.T, r leaseRepo) string {
			r.sideConflict(t)
			tip := r.head(t)
			r.mustConflict(t, "merge", "side")
			return tip
		}, "merge"},
		{"rebase", func(t *testing.T, r leaseRepo) string {
			r.sideConflict(t)
			tip := r.head(t)
			r.mustConflict(t, "rebase", "side")
			return tip
		}, "rebase"},
		{"rebase, apply backend", func(t *testing.T, r leaseRepo) string {
			r.sideConflict(t)
			tip := r.head(t)
			r.mustConflict(t, "rebase", "--apply", "side")
			return tip
		}, "rebase"},
		{"cherry-pick of two, the second conflicting", func(t *testing.T, r leaseRepo) string {
			r.git(t, "checkout", "-q", "-b", "side")
			r.commit(t, "clean.txt", "clean\n")
			r.commit(t, "conflict.txt", "theirs\n")
			r.git(t, "checkout", "-q", r.branch)
			r.commit(t, "conflict.txt", "ours\n")
			r.mustConflict(t, "cherry-pick", "side~1", "side")
			return r.head(t) // the first pick is on the branch now
		}, "cherry-pick"},
		{"am of two, the second conflicting", func(t *testing.T, r leaseRepo) string {
			r.git(t, "checkout", "-q", "-b", "side")
			r.commit(t, "clean.txt", "clean\n")
			r.commit(t, "conflict.txt", "theirs\n")
			patches := t.TempDir()
			r.git(t, "format-patch", "-q", "-o", patches, "side~2..side")
			r.git(t, "checkout", "-q", r.branch)
			r.commit(t, "conflict.txt", "ours\n")
			files, _ := filepath.Glob(filepath.Join(patches, "*.patch"))
			r.mustConflict(t, append([]string{"am", "-3"}, files...)...)
			return r.head(t) // the first patch is on the branch now
		}, "am"},
		{"revert", func(t *testing.T, r leaseRepo) string {
			r.commit(t, "conflict.txt", "one\n")
			r.commit(t, "conflict.txt", "two\n")
			tip := r.head(t)
			r.mustConflict(t, "revert", "--no-edit", "HEAD~1")
			return tip
		}, "revert"},
		{"a conflicted stash pop, which git keeps no marker for", func(t *testing.T, r leaseRepo) string {
			r.commit(t, "conflict.txt", "base\n")
			if err := os.WriteFile(filepath.Join(r.dir, "conflict.txt"), []byte("stashed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			r.git(t, "stash", "-q")
			r.commit(t, "conflict.txt", "committed\n")
			tip := r.head(t)
			r.mustConflict(t, "stash", "pop")
			return tip
		}, "unmerged index"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			remote := newSourceAndRemote(t)
			lease, err := Acquire(home, "fixture", remote, "main", "jig/T-1", "T-1", Build)
			if err != nil {
				t.Fatalf("first Acquire: %v", err)
			}
			if lease.Recovered != "" {
				t.Fatalf("a fresh lease recovered %q", lease.Recovered)
			}
			r := leaseRepo{dir: lease.Dir, branch: "jig/T-1"}
			r.identity(t)
			wantTip := tc.start(t, r)

			again, err := Acquire(home, "fixture", remote, "main", "jig/T-1", "T-1", Build)
			if err != nil {
				t.Fatalf("Acquire over what was left unfinished: %v", err)
			}
			if again.Recovered != tc.want {
				t.Errorf("Recovered = %q, want %q", again.Recovered, tc.want)
			}
			if got := r.head(t); got != wantTip {
				t.Errorf("branch tip = %s, want %s: recovery must keep every commit and move no branch", got, wantTip)
			}
			if unmerged, _ := gitx.Run(lease.Dir, "ls-files", "--unmerged"); strings.TrimSpace(unmerged) != "" {
				t.Errorf("unmerged entries after recovery: %q", unmerged)
			}
			if third, err := Acquire(home, "fixture", remote, "main", "jig/T-1", "T-1", Build); err != nil || third.Recovered != "" {
				t.Errorf("a later Acquire = (%q, %v), want nothing to recover", third.Recovered, err)
			}
		})
	}
}

// leaseRepo drives git in a lease for the recovery tests.
type leaseRepo struct{ dir, branch string }

func (r leaseRepo) git(t *testing.T, args ...string) string {
	t.Helper()
	out, err := gitx.Run(r.dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(out)
}

// identity sets a repo-level committer, as the pool tests' source repos do.
func (r leaseRepo) identity(t *testing.T) {
	t.Helper()
	r.git(t, "config", "user.email", "fixture@example.invalid")
	r.git(t, "config", "user.name", "jig-fixture")
}

func (r leaseRepo) head(t *testing.T) string {
	t.Helper()
	return r.git(t, "rev-parse", "refs/heads/"+r.branch)
}

func (r leaseRepo) commit(t *testing.T, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r.git(t, "add", "-A")
	r.git(t, "commit", "-q", "-m", file+": "+strings.TrimSpace(content))
}

// sideConflict makes a side branch and the lease's branch each change
// conflict.txt differently, leaving the lease's branch checked out.
func (r leaseRepo) sideConflict(t *testing.T) {
	t.Helper()
	r.git(t, "checkout", "-q", "-b", "side")
	r.commit(t, "conflict.txt", "theirs\n")
	r.git(t, "checkout", "-q", r.branch)
	r.commit(t, "conflict.txt", "ours\n")
}

// mustConflict runs git args, which must stop on a conflict.
func (r leaseRepo) mustConflict(t *testing.T, args ...string) {
	t.Helper()
	if _, err := gitx.Run(r.dir, args...); err == nil {
		t.Fatalf("git %v did not stop on its conflict", args)
	}
}
