package pool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// TestAcquireAbortsWhatACrashedSessionLeftUnfinished: a reused lease with a
// merge, rebase or cherry-pick stopped on a conflict, as a session that died
// mid-operation leaves it, is acquired with the operation aborted and named
// in Lease.Recovered; a clean reuse names nothing.
func TestAcquireAbortsWhatACrashedSessionLeftUnfinished(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		start func(t *testing.T, dir string)
	}{
		{"merge", func(t *testing.T, dir string) { conflictWith(t, dir, "merge", "crash-other") }},
		{"rebase", func(t *testing.T, dir string) { conflictWith(t, dir, "rebase", "crash-other") }},
		{"cherry-pick", func(t *testing.T, dir string) { conflictWith(t, dir, "cherry-pick", "crash-other") }},
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
			tc.start(t, lease.Dir)

			again, err := Acquire(home, "fixture", remote, "main", "jig/T-1", "T-1", Build)
			if err != nil {
				t.Fatalf("Acquire over an unfinished %s: %v", tc.name, err)
			}
			if again.Recovered != tc.name {
				t.Errorf("Recovered = %q, want %q", again.Recovered, tc.name)
			}
			if status, _ := gitx.Run(again.Dir, "status", "--porcelain"); status != "" {
				t.Errorf("status after recovery = %q, want a clean tree", status)
			}
			if third, err := Acquire(home, "fixture", remote, "main", "jig/T-1", "T-1", Build); err != nil || third.Recovered != "" {
				t.Errorf("a later Acquire = (%q, %v), want nothing to recover", third.Recovered, err)
			}
		})
	}
}

// conflictWith commits conflicting changes to one file on the lease's branch
// and on a side branch, then runs op (merge, rebase or cherry-pick) so it
// stops on the conflict.
func conflictWith(t *testing.T, dir, op, side string) {
	t.Helper()
	for _, kv := range [][2]string{{"user.email", "fixture@example.invalid"}, {"user.name", "jig-fixture"}} {
		if _, err := gitx.Run(dir, "config", kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "conflict.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := gitx.Run(dir, "add", "-A"); err != nil {
			t.Fatal(err)
		}
		if _, err := gitx.Run(dir, "commit", "-q", "-m", content); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	branch, err := gitx.Run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(dir, "checkout", "-q", "-b", side); err != nil {
		t.Fatal(err)
	}
	commit("theirs\n")
	if _, err := gitx.Run(dir, "checkout", "-q", branch); err != nil {
		t.Fatal(err)
	}
	commit("ours\n")
	if _, err := gitx.Run(dir, op, side); err == nil {
		t.Fatalf("%s %s did not stop on its conflict", op, side)
	}
}
