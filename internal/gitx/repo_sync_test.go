package gitx

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// removeLooseObject deletes object h from dir's repository, which must hold
// it loose.
func removeLooseObject(t *testing.T, gitDir, h string) {
	t.Helper()
	if err := os.Remove(filepath.Join(gitDir, "objects", h[:2], h[2:])); err != nil {
		t.Fatalf("remove loose object %s: %v", h, err)
	}
}

// TestPushFastForwardsOnly: a push to a local bare origin moves its branch
// and the local remote-tracking branch, leaves a branch origin lacks to the
// git program, and is rejected, changing nothing, once origin moved on.
func TestPushFastForwardsOnly(t *testing.T) {
	work, remote := newRemotePair(t)
	writeFileT(t, work, "a.txt", "a\n")
	mustRun(t, work, "add", "-A")
	mustRun(t, work, "commit", "-q", "-m", "a")
	head := mustRun(t, work, "rev-parse", "HEAD")

	if err := openRepo(t, work).Push("origin", "main"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := mustRun(t, remote, "rev-parse", "main"); got != head {
		t.Fatalf("origin's main = %s, want %s", got, head)
	}
	if got := mustRun(t, work, "rev-parse", "origin/main"); got != head {
		t.Fatalf("origin/main = %s, want %s", got, head)
	}
	mustRun(t, remote, "fsck", "--strict")
	if err := openRepo(t, work).Push("origin", "main"); err != nil {
		t.Fatalf("Push with nothing new: %v", err)
	}

	mustRun(t, work, "checkout", "-q", "-b", "side")
	if err := openRepo(t, work).Push("origin", "side"); !errors.Is(err, ErrUseCLI) {
		t.Fatalf("Push of a branch origin lacks = %v, want ErrUseCLI", err)
	}
	if _, err := Run(remote, "rev-parse", "--verify", "-q", "refs/heads/side"); err == nil {
		t.Fatal("origin has a side branch after a push left to the git program")
	}
	mustRun(t, work, "checkout", "-q", "main")

	other := filepath.Join(t.TempDir(), "other")
	mustRun(t, "", "clone", "-q", remote, other)
	writeFileT(t, other, "b.txt", "b\n")
	mustRun(t, other, "add", "-A")
	mustRun(t, other, "-c", "user.name=o", "-c", "user.email=o@example.invalid", "commit", "-q", "-m", "b")
	mustRun(t, other, "push", "-q", "origin", "main")
	moved := mustRun(t, remote, "rev-parse", "main")

	writeFileT(t, work, "c.txt", "c\n")
	mustRun(t, work, "add", "-A")
	mustRun(t, work, "commit", "-q", "-m", "c")
	if err := openRepo(t, work).Push("origin", "main"); !errors.Is(err, ErrPushRejected) {
		t.Fatalf("Push after origin moved on = %v, want ErrPushRejected", err)
	}
	if got := mustRun(t, remote, "rev-parse", "main"); got != moved {
		t.Fatalf("a rejected push moved origin's main to %s", got)
	}
}

// TestPushAndFetchLeaveOtherRemotesToTheGitProgram: only a bare repository
// at a local path is served in process; a network remote (credentials, SSH
// configuration) and a non-bare one are not, even named by its .git
// directory, and nothing is attempted.
func TestPushAndFetchLeaveOtherRemotesToTheGitProgram(t *testing.T) {
	work := newStoreRepo(t)
	nonBare := newStoreRepo(t)
	for name, u := range map[string]string{"net": "https://example.invalid/store.git", "scp": "git@example.invalid:store.git", "nonbare": nonBare, "dotgit": filepath.Join(nonBare, ".git")} {
		mustRun(t, work, "remote", "add", name, u)
		r := openRepo(t, work)
		if err := r.Push(name, "main"); !errors.Is(err, ErrUseCLI) {
			t.Errorf("Push to %s = %v, want ErrUseCLI", u, err)
		}
		if _, err := r.Fetch(name, "main"); !errors.Is(err, ErrUseCLI) {
			t.Errorf("Fetch from %s = %v, want ErrUseCLI", u, err)
		}
	}
}

// TestPushAndFetchLeaveWhatTheRemoteConfigAsksToTheGitProgram: a local bare
// origin is still the git program's when either side's config asks for what
// an in-process copy does not do, and then neither side changes.
func TestPushAndFetchLeaveWhatTheRemoteConfigAsksToTheGitProgram(t *testing.T) {
	cases := []struct {
		name string
		lay  func(t *testing.T, work, remote string)
	}{
		{"a push URL", func(t *testing.T, work, remote string) {
			mustRun(t, work, "config", "remote.origin.pushurl", remote)
		}},
		{"its own receive-pack", func(t *testing.T, work, _ string) {
			mustRun(t, work, "config", "remote.origin.receivepack", "git-receive-pack")
		}},
		{"URL rewriting", func(t *testing.T, work, remote string) {
			mustRun(t, work, "config", "url."+remote+".insteadOf", "store:")
		}},
		{"another fetch refspec", func(t *testing.T, work, _ string) {
			mustRun(t, work, "config", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main")
		}},
		{"objects checked on fetch", func(t *testing.T, work, _ string) {
			mustRun(t, work, "config", "fetch.fsckObjects", "true")
		}},
		{"objects checked on receive", func(t *testing.T, _, remote string) {
			mustRun(t, remote, "config", "receive.fsckObjects", "true")
		}},
		{"objects checked on every transfer, as a bare key", func(t *testing.T, _, remote string) {
			writeFileT(t, remote, "config", readFileT(t, remote, "config")+"[transfer]\n\tfsckObjects\n")
		}},
		{"the remote's own hook", func(t *testing.T, _, remote string) {
			writeFileT(t, remote, "hooks/post-receive", "#!/bin/sh\n")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			work, remote := newRemotePair(t)
			writeFileT(t, work, "a.txt", "a\n")
			mustRun(t, work, "add", "-A")
			mustRun(t, work, "commit", "-q", "-m", "a")
			before := mustRun(t, remote, "rev-parse", "main")
			c.lay(t, work, remote)
			r := openRepo(t, work)
			if err := r.Push("origin", "main"); !errors.Is(err, ErrUseCLI) {
				t.Errorf("Push = %v, want ErrUseCLI", err)
			}
			if _, err := r.Fetch("origin", "main"); !errors.Is(err, ErrUseCLI) {
				t.Errorf("Fetch = %v, want ErrUseCLI", err)
			}
			if after := mustRun(t, remote, "rev-parse", "main"); after != before {
				t.Errorf("origin's main moved from %s to %s", before, after)
			}
		})
	}
}

// TestPushReadsOnlyWhatChangedSinceTheRemotesTip: a push walks back from
// the local branch only to origin's commit, and copies only what each new
// commit changed, so it never reads the history below origin's commit or
// what did not change: with those objects gone from the local repository,
// it still pushes, and origin is sound afterwards.
func TestPushReadsOnlyWhatChangedSinceTheRemotesTip(t *testing.T) {
	work, remote := newRemotePair(t)
	commitFile(t, work, "old/deep/a.txt", "a\n")
	mustRun(t, work, "push", "-q", "origin", "main")
	root := mustRun(t, work, "rev-list", "--max-parents=0", "HEAD")
	unchanged := mustRun(t, work, "rev-parse", "HEAD:old")
	commitFile(t, work, "new/b.txt", "b\n")
	head := commitFile(t, work, "new/c.txt", "c\n")

	removeLooseObject(t, filepath.Join(work, ".git"), root)
	removeLooseObject(t, filepath.Join(work, ".git"), unchanged)
	if err := openRepo(t, work).Push("origin", "main"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := mustRun(t, remote, "rev-parse", "main"); got != head {
		t.Fatalf("origin's main = %s, want %s", got, head)
	}
	mustRun(t, remote, "fsck", "--strict")
	if got := mustRun(t, remote, "show", "main:new/b.txt"); got != "b" {
		t.Fatalf("origin's new/b.txt = %q", got)
	}
}

// TestPushAndFetchLeaveMergesAndUnrelatedHistoriesToTheGitProgram: jig's
// store history is linear; a merge on either side, or two histories with
// nothing in common, are the git program's, and nothing moves.
func TestPushAndFetchLeaveMergesAndUnrelatedHistoriesToTheGitProgram(t *testing.T) {
	t.Run("a merge", func(t *testing.T) {
		work, remote := newRemotePair(t)
		base := mustRun(t, work, "rev-parse", "HEAD")
		mustRun(t, work, "checkout", "-q", "-b", "side")
		commitFile(t, work, "side.txt", "s\n")
		mustRun(t, work, "checkout", "-q", "main")
		commitFile(t, work, "main.txt", "m\n")
		mustRun(t, work, "-c", "user.name=o", "-c", "user.email=o@example.invalid", "merge", "-q", "--no-ff", "-m", "merge", "side")
		if err := openRepo(t, work).Push("origin", "main"); !errors.Is(err, ErrUseCLI) {
			t.Fatalf("Push of a merge = %v, want ErrUseCLI", err)
		}
		if got := mustRun(t, remote, "rev-parse", "main"); got != base {
			t.Fatalf("origin's main moved to %s", got)
		}
	})

	t.Run("unrelated histories", func(t *testing.T) {
		work, remote := newRemotePair(t)
		other := t.TempDir()
		mustRun(t, other, "init", "-q", "-b", "main")
		commitFile(t, other, "x.txt", "an unrelated root\n")
		commitFile(t, other, "y.txt", "y\n")
		mustRun(t, other, "push", "-q", "--force", remote, "main")
		theirs := mustRun(t, remote, "rev-parse", "main")
		r := openRepo(t, work)
		if _, err := r.Fetch("origin", "main"); !errors.Is(err, ErrUseCLI) {
			t.Fatalf("Fetch of an unrelated history = %v, want ErrUseCLI", err)
		}
		if err := r.Push("origin", "main"); !errors.Is(err, ErrUseCLI) {
			t.Fatalf("Push onto an unrelated history = %v, want ErrUseCLI", err)
		}
		if got := mustRun(t, remote, "rev-parse", "main"); got != theirs {
			t.Fatalf("origin's main moved to %s", got)
		}
	})
}

// TestPushAndCommitRespectGitsLocks: a ref or index lock file another
// process holds hands the step to the git program, which reports it as it
// always does; nothing moves and the lock is left where it was. A step
// that runs leaves no lock of its own behind.
func TestPushAndCommitRespectGitsLocks(t *testing.T) {
	t.Run("origin's branch", func(t *testing.T) {
		work, remote := newRemotePair(t)
		commitFile(t, work, "a.txt", "a\n")
		before := mustRun(t, remote, "rev-parse", "main")
		writeFileT(t, remote, "refs/heads/main.lock", "")
		if err := openRepo(t, work).Push("origin", "main"); !errors.Is(err, ErrUseCLI) {
			t.Fatalf("Push with origin's main locked = %v, want ErrUseCLI", err)
		}
		if got := mustRun(t, remote, "rev-parse", "main"); got != before {
			t.Fatalf("origin's main moved to %s under another process's lock", got)
		}
		if _, err := os.Stat(filepath.Join(remote, "refs", "heads", "main.lock")); err != nil {
			t.Fatalf("the other process's lock is gone: %v", err)
		}
	})

	for _, lock := range []string{".git/index.lock", ".git/refs/heads/main.lock"} {
		t.Run(lock, func(t *testing.T) {
			dir := newStoreRepo(t)
			writeFileT(t, dir, lock, "")
			writeFileT(t, dir, "a.txt", "a\n")
			if _, err := openRepo(t, dir).CommitAll("locked", "jig", "jig@invalid"); !errors.Is(err, ErrUseCLI) {
				t.Fatalf("CommitAll with %s held = %v, want ErrUseCLI", lock, err)
			}
			if got := mustRun(t, dir, "log", "-1", "--format=%s"); got != "init" {
				t.Fatalf("a commit was made under another process's lock: %q", got)
			}
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(lock))); err != nil {
				t.Fatalf("the other process's lock is gone: %v", err)
			}
		})
	}

	t.Run("none left behind", func(t *testing.T) {
		work, remote := newRemotePair(t)
		writeFileT(t, work, "a.txt", "a\n")
		r := openRepo(t, work)
		if _, err := r.CommitAll("a", "jig", "jig@invalid"); err != nil {
			t.Fatalf("CommitAll: %v", err)
		}
		if err := r.Push("origin", "main"); err != nil {
			t.Fatalf("Push: %v", err)
		}
		for _, dir := range []string{filepath.Join(work, ".git"), remote} {
			filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err == nil && strings.HasSuffix(p, ".lock") {
					t.Errorf("lock left behind: %s", p)
				}
				return nil
			})
		}
	})
}

// looseIn17 returns contents for n files whose blobs all land in
// objects/17, the directory git's loose-object estimate counts.
func looseIn17(n int) []string {
	var out []string
	for i := 0; len(out) < n; i++ {
		body := fmt.Sprintf("%d\n", i)
		sum := sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(body), body)))
		if sum[0] == 0x17 {
			out = append(out, body)
		}
	}
	return out
}

// TestPushStartsTheRemotesAutomaticMaintenance: the git program's
// receive-pack starts `git maintenance run --auto` on the remote after a
// push; an in-process push does the same once the remote's loose objects
// may have passed its gc.auto, unless its receive.autoGC is off.
func TestPushStartsTheRemotesAutomaticMaintenance(t *testing.T) {
	for _, c := range []struct {
		name   string
		autoGC string
		packed bool
	}{
		{"receive.autoGC on", "", true},
		{"receive.autoGC off", "false", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			work, remote := newRemotePair(t)
			mustRun(t, remote, "config", "gc.auto", "1")
			if c.autoGC != "" {
				mustRun(t, remote, "config", "receive.autoGC", c.autoGC)
			}
			for i, body := range looseIn17(2) {
				writeFileT(t, work, fmt.Sprintf("f%d.txt", i), body)
			}
			mustRun(t, work, "add", "-A")
			mustRun(t, work, "commit", "-q", "-m", "two loose objects in 17")
			if err := openRepo(t, work).Push("origin", "main"); err != nil {
				t.Fatalf("Push: %v", err)
			}
			entries, err := os.ReadDir(filepath.Join(remote, "objects", "17"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if packed := len(entries) == 0; packed != c.packed {
				t.Fatalf("origin's objects/17 has %d loose objects; packed = %v, want %v", len(entries), packed, c.packed)
			}
			mustRun(t, remote, "fsck", "--strict")
		})
	}
}

// TestFetch: the remote's branch lands in the remote-tracking branch, the
// relation to the local branch is reported, and neither the local branch
// nor the working tree (untracked and ignored files included) changes.
func TestFetch(t *testing.T) {
	setup := func(t *testing.T) (work, other, remote string) {
		work, remote = newRemotePair(t)
		other = filepath.Join(t.TempDir(), "other")
		mustRun(t, "", "clone", "-q", remote, other)
		return work, other, remote
	}
	commitIn := func(t *testing.T, dir, file, body string) string {
		writeFileT(t, dir, file, body)
		mustRun(t, dir, "add", "-A")
		mustRun(t, dir, "-c", "user.name=o", "-c", "user.email=o@example.invalid", "commit", "-q", "-m", file)
		return mustRun(t, dir, "rev-parse", "HEAD")
	}

	fetch := func(t *testing.T, work string, want Relation) {
		t.Helper()
		head := mustRun(t, work, "rev-parse", "HEAD")
		writeFileT(t, work, "journal.lock", "held")
		writeFileT(t, work, "scratch.txt", "untracked\n")
		got, err := openRepo(t, work).Fetch("origin", "main")
		if err != nil || got != want {
			t.Fatalf("Fetch = %v, %v; want %v", got, err, want)
		}
		if after := mustRun(t, work, "rev-parse", "HEAD"); after != head {
			t.Fatalf("Fetch moved HEAD from %s to %s", head, after)
		}
		if st := mustRun(t, work, "status", "--porcelain", "--ignored"); st != "?? scratch.txt\n!! journal.lock" {
			t.Fatalf("git status after Fetch = %q, want the working tree untouched", st)
		}
		mustRun(t, work, "fsck", "--strict")
	}

	t.Run("same", func(t *testing.T) {
		work, _, _ := setup(t)
		fetch(t, work, Same)
	})

	t.Run("ahead", func(t *testing.T) {
		work, _, remote := setup(t)
		commitIn(t, work, "a.txt", "a\n")
		fetch(t, work, Ahead)
		if got, want := mustRun(t, work, "rev-parse", "origin/main"), mustRun(t, remote, "rev-parse", "main"); got != want {
			t.Fatalf("origin/main = %s, want %s", got, want)
		}
	})

	t.Run("behind", func(t *testing.T) {
		work, other, _ := setup(t)
		theirs := commitIn(t, other, "T-1/state.yaml", "state: green\n")
		mustRun(t, other, "push", "-q", "origin", "main")
		fetch(t, work, Behind)
		if got := mustRun(t, work, "rev-parse", "origin/main"); got != theirs {
			t.Fatalf("origin/main = %s, want the fetched %s", got, theirs)
		}
		// The git program can fast-forward to what was fetched.
		mustRun(t, work, "merge", "--ff-only", "--quiet", "refs/remotes/origin/main")
		if b, err := os.ReadFile(filepath.Join(work, "T-1", "state.yaml")); err != nil || string(b) != "state: green\n" {
			t.Fatalf("fast-forwarded file = %q, %v", b, err)
		}
	})

	t.Run("diverged", func(t *testing.T) {
		work, other, _ := setup(t)
		before := mustRun(t, work, "rev-parse", "origin/main")
		commitIn(t, other, "b.txt", "b\n")
		mustRun(t, other, "push", "-q", "origin", "main")
		commitIn(t, work, "a.txt", "a\n")
		fetch(t, work, Diverged)
		// The git program's rebase fetches for itself.
		if got := mustRun(t, work, "rev-parse", "origin/main"); got != before {
			t.Fatalf("origin/main = %s, want it left at %s for the git program", got, before)
		}
	})
}
