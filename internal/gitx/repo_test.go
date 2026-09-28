package gitx

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

// mustRun runs the git program in dir and fails t on error.
func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Run(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// writeFileT writes body to dir/name, creating parents.
func writeFileT(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newStoreRepo creates a repository shaped like a jig store (StoreAttributes
// at its root, one commit on main) and returns its directory.
func newStoreRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustRun(t, dir, "init", "-q", "-b", "main")
	mustRun(t, dir, "config", "user.name", "tester")
	mustRun(t, dir, "config", "user.email", "tester@example.invalid")
	writeFileT(t, dir, ".gitattributes", StoreAttributes)
	writeFileT(t, dir, ".gitignore", "*.lock\n")
	writeFileT(t, dir, "project.yaml", "schema_version: 1\n")
	mustRun(t, dir, "add", "-A")
	mustRun(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// newRemotePair returns a store repository with a bare origin it has pushed
// main to, and that origin's path.
func newRemotePair(t *testing.T) (work, remote string) {
	t.Helper()
	work = newStoreRepo(t)
	remote = filepath.Join(t.TempDir(), "origin.git")
	mustRun(t, "", "init", "-q", "--bare", "-b", "main", remote)
	mustRun(t, work, "remote", "add", "origin", remote)
	mustRun(t, work, "push", "-q", "origin", "main")
	return work, remote
}

// openRepo opens dir in process and fails t if it cannot.
func openRepo(t *testing.T, dir string) *Repo {
	t.Helper()
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo(%s): %v", dir, err)
	}
	return r
}

// TestOpenRepoOnlyWhenNothingConvertsLineEndings: go-git converts no line
// endings and runs no filters, so a repository is opened in process only
// when it rules both out itself, never from the machine's own config.
func TestOpenRepoOnlyWhenNothingConvertsLineEndings(t *testing.T) {
	cases := []struct {
		name string
		lay  func(t *testing.T, dir string)
		ok   bool
	}{
		{"says nothing", func(*testing.T, string) {}, false},
		{"its own core.autocrlf=false", func(t *testing.T, dir string) {
			mustRun(t, dir, "config", "core.autocrlf", "false")
		}, true},
		{"jig's store attributes", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitattributes", StoreAttributes)
		}, true},
		{"jig's store attributes with CRLF", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitattributes", "* -text\r\n")
		}, true},
		{"another text rule", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitattributes", "*.md text eol=crlf\n")
		}, false},
		{"a filter", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitattributes", "* -text\n*.bin filter=lfs\n")
		}, false},
		{"info/attributes as well", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".gitattributes", StoreAttributes)
			writeFileT(t, dir, ".git/info/attributes", "*.md text\n")
		}, false},
		{"autocrlf=false but an attributes file", func(t *testing.T, dir string) {
			mustRun(t, dir, "config", "core.autocrlf", "false")
			writeFileT(t, dir, ".gitattributes", "*.md text\n")
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			mustRun(t, dir, "init", "-q", "-b", "main")
			c.lay(t, dir)
			_, err := OpenRepo(dir)
			if c.ok && err != nil {
				t.Fatalf("OpenRepo = %v, want it opened", err)
			}
			if !c.ok && !errors.Is(err, ErrUseCLI) {
				t.Fatalf("OpenRepo = %v, want ErrUseCLI", err)
			}
		})
	}

	t.Run("a linked worktree", func(t *testing.T) {
		main := newStoreRepo(t)
		linked := filepath.Join(t.TempDir(), "linked")
		mustRun(t, main, "worktree", "add", "-q", "-b", "side", linked)
		if _, err := OpenRepo(linked); !errors.Is(err, ErrUseCLI) {
			t.Fatalf("OpenRepo(linked worktree) = %v, want ErrUseCLI", err)
		}
	})
}

// TestOpenRepoLeavesWhatTheRepositoryDeclaresToTheGitProgram: a repository
// whose own files or config ask for something go-git does not do - its own
// hooks, a shallow or grafted history, alternates, a format extension,
// group-shared permissions - is left to the git program, while git's hook
// samples alone change nothing.
func TestOpenRepoLeavesWhatTheRepositoryDeclaresToTheGitProgram(t *testing.T) {
	cases := []struct {
		name string
		lay  func(t *testing.T, dir string)
		ok   bool
	}{
		{"nothing declared", func(*testing.T, string) {}, true},
		{"only git's hook samples", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".git/hooks/pre-commit.sample", "#!/bin/sh\n")
		}, true},
		{"its own hook", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".git/hooks/post-commit", "#!/bin/sh\n")
		}, false},
		{"its own core.hooksPath", func(t *testing.T, dir string) {
			mustRun(t, dir, "config", "core.hooksPath", "hooks")
		}, false},
		{"a shallow history", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".git/shallow", mustRun(t, dir, "rev-parse", "HEAD")+"\n")
		}, false},
		{"grafts", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".git/info/grafts", "")
		}, false},
		{"alternates", func(t *testing.T, dir string) {
			writeFileT(t, dir, ".git/objects/info/alternates", t.TempDir()+"\n")
		}, false},
		{"a format extension", func(t *testing.T, dir string) {
			mustRun(t, dir, "config", "core.repositoryFormatVersion", "1")
			mustRun(t, dir, "config", "extensions.worktreeConfig", "true")
		}, false},
		{"group-shared", func(t *testing.T, dir string) {
			mustRun(t, dir, "config", "core.sharedRepository", "group")
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := newStoreRepo(t)
			c.lay(t, dir)
			_, err := OpenRepo(dir)
			if c.ok && err != nil {
				t.Fatalf("OpenRepo = %v, want it opened", err)
			}
			if !c.ok && !errors.Is(err, ErrUseCLI) {
				t.Fatalf("OpenRepo = %v, want ErrUseCLI", err)
			}
		})
	}
}

// TestOpenRepoAnythingGoGitCannotOpen: a directory go-git cannot open as a
// repository is the git program's to report on.
func TestOpenRepoAnythingGoGitCannotOpen(t *testing.T) {
	dir := t.TempDir()
	writeFileT(t, dir, ".git/HEAD", "not a ref\n")
	if _, err := OpenRepo(dir); !errors.Is(err, ErrUseCLI) {
		t.Fatalf("OpenRepo(broken) = %v, want ErrUseCLI", err)
	}
	if _, err := OpenRepo(t.TempDir()); !errors.Is(err, ErrUseCLI) {
		t.Fatalf("OpenRepo(no repository) = %v, want ErrUseCLI", err)
	}
}

// TestRepoState pins what one in-process look reports: the branch (an
// unborn one included, none when HEAD is detached), unmerged entries, and
// whether anything is left to commit, ignored files aside.
func TestRepoState(t *testing.T) {
	cases := []struct {
		name string
		lay  func(t *testing.T, dir string)
		want RepoState
	}{
		{"clean", func(*testing.T, string) {}, RepoState{Branch: "main"}},
		{"an ignored file", func(t *testing.T, dir string) {
			writeFileT(t, dir, "journal.lock", "")
		}, RepoState{Branch: "main"}},
		{"an untracked file", func(t *testing.T, dir string) {
			writeFileT(t, dir, "T-1/journal.ndjson", "{}\n")
		}, RepoState{Branch: "main", Dirty: true}},
		{"a modified file", func(t *testing.T, dir string) {
			writeFileT(t, dir, "project.yaml", "schema_version: 2\n")
		}, RepoState{Branch: "main", Dirty: true}},
		{"a staged change", func(t *testing.T, dir string) {
			writeFileT(t, dir, "project.yaml", "schema_version: 2\n")
			mustRun(t, dir, "add", "-A")
		}, RepoState{Branch: "main", Dirty: true}},
		{"a deleted file", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "project.yaml")); err != nil {
				t.Fatal(err)
			}
		}, RepoState{Branch: "main", Dirty: true}},
		{"a detached HEAD", func(t *testing.T, dir string) {
			mustRun(t, dir, "checkout", "-q", "--detach")
		}, RepoState{}},
		{"unmerged entries", func(t *testing.T, dir string) {
			writeFileT(t, dir, "project.yaml", "schema_version: 2\n")
			mustRun(t, dir, "stash", "-q")
			writeFileT(t, dir, "project.yaml", "schema_version: 3\n")
			mustRun(t, dir, "commit", "-q", "-am", "three")
			if _, err := Run(dir, "stash", "pop"); err == nil {
				t.Fatal("setup: stash pop did not conflict")
			}
		}, RepoState{Branch: "main", Unmerged: true, Dirty: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := newStoreRepo(t)
			c.lay(t, dir)
			got, err := openRepo(t, dir).State()
			if err != nil {
				t.Fatalf("State: %v", err)
			}
			if got != c.want {
				t.Fatalf("State = %+v, want %+v", got, c.want)
			}
		})
	}

	t.Run("an unborn branch", func(t *testing.T) {
		dir := t.TempDir()
		mustRun(t, dir, "init", "-q", "-b", "trunk")
		writeFileT(t, dir, ".gitattributes", StoreAttributes)
		got, err := openRepo(t, dir).State()
		if err != nil {
			t.Fatalf("State: %v", err)
		}
		if want := (RepoState{Branch: "trunk", Dirty: true}); got != want {
			t.Fatalf("State = %+v, want %+v", got, want)
		}
	})
}

// TestCommitAllMatchesTheGitProgram: an in-process commit records added,
// changed and deleted files, leaves the git program seeing a clean tree and
// a sound repository, and reports when there was nothing to commit.
func TestCommitAllMatchesTheGitProgram(t *testing.T) {
	dir := newStoreRepo(t)
	writeFileT(t, dir, "gone.txt", "soon deleted\n")
	mustRun(t, dir, "add", "-A")
	mustRun(t, dir, "commit", "-q", "-m", "gone")
	writeFileT(t, dir, "T-1/journal.ndjson", "{\"event\":\"dispatch\"}\n")
	writeFileT(t, dir, "project.yaml", "schema_version: 2\n")
	writeFileT(t, dir, "journal.lock", "")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	committed, err := openRepo(t, dir).CommitAll("jig: record", "jig", "jig@invalid")
	if err != nil || !committed {
		t.Fatalf("CommitAll = %v, %v; want a commit", committed, err)
	}
	if st := mustRun(t, dir, "status", "--porcelain", "--untracked-files=all", "--ignored"); st != "!! journal.lock" {
		t.Fatalf("git status after CommitAll = %q, want only the ignored lock file", st)
	}
	if files := mustRun(t, dir, "ls-files"); files != ".gitattributes\n.gitignore\nT-1/journal.ndjson\nproject.yaml" {
		t.Fatalf("tracked after CommitAll = %q", files)
	}
	if msg := mustRun(t, dir, "log", "-1", "--format=%an <%ae>|%B"); msg != "jig <jig@invalid>|jig: record" {
		t.Fatalf("commit = %q", msg)
	}
	mustRun(t, dir, "fsck", "--strict")

	committed, err = openRepo(t, dir).CommitAll("again", "jig", "jig@invalid")
	if err != nil || committed {
		t.Fatalf("CommitAll with nothing new = %v, %v; want no commit", committed, err)
	}
}

// TestCommitAllHonorsTheIdentityEnvironment: GIT_AUTHOR_* and
// GIT_COMMITTER_* win over the identity passed in, as they do for the git
// program, and a pinned date go-git cannot read hands the commit to the git
// program before anything is staged.
func TestCommitAllHonorsTheIdentityEnvironment(t *testing.T) {
	dir := newStoreRepo(t)
	t.Setenv("GIT_AUTHOR_NAME", "pinned author")
	t.Setenv("GIT_AUTHOR_EMAIL", "author@example.invalid")
	t.Setenv("GIT_AUTHOR_DATE", "2026-01-01T00:00:00Z")
	t.Setenv("GIT_COMMITTER_NAME", "pinned committer")
	t.Setenv("GIT_COMMITTER_EMAIL", "committer@example.invalid")
	t.Setenv("GIT_COMMITTER_DATE", "1767225600 +0100")
	writeFileT(t, dir, "a.txt", "a\n")
	if _, err := openRepo(t, dir).CommitAll("pinned", "jig", "jig@invalid"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	got := mustRun(t, dir, "log", "-1", "--format=%an <%ae> %aI|%cn <%ce> %cI")
	want := "pinned author <author@example.invalid> 2026-01-01T00:00:00Z|pinned committer <committer@example.invalid> 2026-01-01T01:00:00+01:00"
	if got != want {
		t.Fatalf("identity = %q, want %q", got, want)
	}

	t.Setenv("GIT_AUTHOR_DATE", "yesterday noon")
	writeFileT(t, dir, "b.txt", "b\n")
	if _, err := openRepo(t, dir).CommitAll("unparsed", "jig", "jig@invalid"); !errors.Is(err, ErrUseCLI) {
		t.Fatalf("CommitAll with an unreadable date = %v, want ErrUseCLI", err)
	}
	if st := mustRun(t, dir, "status", "--porcelain"); st != "?? b.txt" {
		t.Fatalf("status after the refused commit = %q, want b.txt still unstaged", st)
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
// configuration) and a non-bare one are not, and nothing is attempted.
func TestPushAndFetchLeaveOtherRemotesToTheGitProgram(t *testing.T) {
	work := newStoreRepo(t)
	nonBare := newStoreRepo(t)
	for name, u := range map[string]string{"net": "https://example.invalid/store.git", "scp": "git@example.invalid:store.git", "nonbare": nonBare} {
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

// commitFile writes body to file in dir and commits it with the git
// program, returning the new commit.
func commitFile(t *testing.T, dir, file, body string) string {
	t.Helper()
	writeFileT(t, dir, file, body)
	mustRun(t, dir, "add", "-A")
	mustRun(t, dir, "-c", "user.name=o", "-c", "user.email=o@example.invalid", "commit", "-q", "-m", file)
	return mustRun(t, dir, "rev-parse", "HEAD")
}

// readFileT reads dir/name.
func readFileT(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// removeLooseObject deletes object h from dir's repository, which must hold
// it loose.
func removeLooseObject(t *testing.T, gitDir, h string) {
	t.Helper()
	if err := os.Remove(filepath.Join(gitDir, "objects", h[:2], h[2:])); err != nil {
		t.Fatalf("remove loose object %s: %v", h, err)
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

// TestWriteRefComparesUnderTheLock: a ref moves only from the value the
// caller read; when it holds another, nothing is written and the lock is
// released.
func TestWriteRefComparesUnderTheLock(t *testing.T) {
	dir := newStoreRepo(t)
	head := mustRun(t, dir, "rev-parse", "HEAD")
	next := commitFile(t, dir, "a.txt", "a\n")
	repo, err := openRepo(t, dir).open()
	if err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(dir, ".git")
	name := plumbing.NewBranchReferenceName("main")
	stale := plumbing.NewHash(head)
	if err := writeRef(repo, gitDir, name, stale, &stale); !errors.Is(err, errRefMoved) {
		t.Fatalf("writeRef from a stale value = %v, want errRefMoved", err)
	}
	if got := mustRun(t, dir, "rev-parse", "main"); got != next {
		t.Fatalf("main = %s after a refused write, want %s", got, next)
	}
	if _, err := os.Stat(filepath.Join(gitDir, "refs", "heads", "main.lock")); !os.IsNotExist(err) {
		t.Fatalf("lock after a refused write: %v", err)
	}
	cur := plumbing.NewHash(next)
	if err := writeRef(repo, gitDir, name, stale, &cur); err != nil {
		t.Fatalf("writeRef from the current value: %v", err)
	}
	if got := mustRun(t, dir, "rev-parse", "main"); got != head {
		t.Fatalf("main = %s, want %s", got, head)
	}
	mustRun(t, dir, "pack-refs", "--all")
	if err := writeRef(repo, gitDir, name, cur, &cur); !errors.Is(err, errRefMoved) {
		t.Fatalf("writeRef against a packed ref from a stale value = %v, want errRefMoved", err)
	}
	if err := writeRef(repo, gitDir, name, cur, &stale); err != nil {
		t.Fatalf("writeRef against a packed ref: %v", err)
	}
	if got := mustRun(t, dir, "rev-parse", "main"); got != next {
		t.Fatalf("main = %s, want %s", got, next)
	}
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

// TestParseGitDate covers the date forms signature reads.
func TestParseGitDate(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", true},
		{"2026-01-01T02:00:00+02:00", "2026-01-01T02:00:00+02:00", true},
		{"1767225600 +0000", "2026-01-01T00:00:00Z", true},
		{"@1767225600 -0530", "2025-12-31T18:30:00-05:30", true},
		{"yesterday", "", false},
		{"1767225600", "", false},
		{"1767225600 +00", "", false},
	}
	for _, c := range cases {
		got, ok := parseGitDate(c.in)
		if ok != c.ok {
			t.Errorf("parseGitDate(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && got.Format(time.RFC3339) != c.want {
			t.Errorf("parseGitDate(%q) = %s, want %s", c.in, got.Format(time.RFC3339), c.want)
		}
	}
}

// TestLooseObjectsPastAutoGC: git's own estimate of too many loose objects,
// the loose objects in objects/17 above gc.auto/256, from the repository's
// own gc.auto (default 6700; 0 turns automatic gc off).
func TestLooseObjectsPastAutoGC(t *testing.T) {
	// fill leaves exactly n loose objects in objects/17, counting any the
	// repository's own commits happened to put there.
	fill := func(t *testing.T, dir string, n int) {
		t.Helper()
		obj := filepath.Join(dir, ".git", "objects", "17")
		if err := os.MkdirAll(obj, 0o755); err != nil {
			t.Fatal(err)
		}
		existing, err := os.ReadDir(obj)
		if err != nil {
			t.Fatal(err)
		}
		for i := len(existing); i < n; i++ {
			name := strings.Repeat("0", 36) + string("0123456789abcdef"[i/16]) + string("0123456789abcdef"[i%16])
			if err := os.WriteFile(filepath.Join(obj, name), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// Not a loose object's name: never counted.
		if err := os.WriteFile(filepath.Join(obj, "tmp_obj_x"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name   string
		gcAuto string
		loose  int
		want   bool
	}{
		{"default limit, at it", "", 27, false},
		{"default limit, past it", "", 28, true},
		{"its own lower limit", "512", 3, true},
		{"automatic gc off", "0", 60, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := newStoreRepo(t)
			if c.gcAuto != "" {
				mustRun(t, dir, "config", "gc.auto", c.gcAuto)
			}
			fill(t, dir, c.loose)
			if got := openRepo(t, dir).LooseObjectsPastAutoGC(); got != c.want {
				t.Fatalf("LooseObjectsPastAutoGC = %v, want %v", got, c.want)
			}
		})
	}
}
