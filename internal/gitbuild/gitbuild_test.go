package gitbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

func pinned() []string {
	return []string{
		"GIT_AUTHOR_NAME=builder", "GIT_AUTHOR_EMAIL=builder@example.invalid", "GIT_AUTHOR_DATE=1767225600 +0000",
		"GIT_COMMITTER_NAME=builder", "GIT_COMMITTER_EMAIL=builder@example.invalid", "GIT_COMMITTER_DATE=1767225600 +0000",
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.RunEnv(dir, pinned(), args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newPair returns a repository gitx serves (no conversion declared) on main
// with a bare remote that already has main, and the remote's path.
func newPair(t *testing.T) (work, remote string) {
	t.Helper()
	work = t.TempDir()
	git(t, work, "init", "-q", "-b", "main")
	write(t, work, ".gitattributes", gitx.StoreAttributes)
	write(t, work, "a.txt", "a\n")
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "init")
	remote = filepath.Join(t.TempDir(), "remote.git")
	git(t, "", "init", "-q", "--bare", "-b", "main", remote)
	git(t, work, "remote", "add", "origin", remote)
	git(t, work, "push", "-q", "origin", "main")
	return work, remote
}

// counting returns a Builder whose fallback counts the git programs it runs.
func counting(t *testing.T) (*Builder, *[][]string) {
	t.Helper()
	b := New(t, pinned())
	var calls [][]string
	b.Fallback = func(dir string, env []string, args ...string) (string, error) {
		calls = append(calls, args)
		return gitx.RunEnv(dir, env, args...)
	}
	return b, &calls
}

// TestCommitIsInProcessWhereGitxAccepts: a repository gitx accepts is
// committed, pushed and fetched without the git program, and the commit is
// the one the git program makes under the same environment.
func TestCommitIsInProcessWhereGitxAccepts(t *testing.T) {
	work, remote := newPair(t)
	b, calls := counting(t)

	write(t, work, "b.txt", "b\n")
	b.Commit(work, "step")
	inProcess := git(t, work, "rev-parse", "HEAD")
	b.Push(work, "origin", "main")
	b.Fetch(work, "origin", "main")
	if len(*calls) != 0 {
		t.Fatalf("Fallback ran %v, want no git program", *calls)
	}
	if got := git(t, remote, "rev-parse", "main"); got != inProcess {
		t.Fatalf("remote main = %s, want the pushed %s", got, inProcess)
	}

	git(t, work, "reset", "-q", "--soft", "HEAD~1")
	git(t, work, "commit", "-q", "-m", "step")
	if cli := git(t, work, "rev-parse", "HEAD"); cli != inProcess {
		t.Fatalf("git program's commit = %s, Commit's = %s; want the same hash", cli, inProcess)
	}
}

// TestDeclinesFallBack: a repository gitx declines still gets its move,
// through Fallback, once per git program the move needs.
func TestDeclinesFallBack(t *testing.T) {
	t.Run("the first push of a branch", func(t *testing.T) {
		work, remote := newPair(t)
		git(t, work, "switch", "-q", "-c", "topic")
		write(t, work, "t.txt", "t\n")
		b, calls := counting(t)
		b.Commit(work, "topic step")
		b.Push(work, "origin", "topic")
		if len(*calls) != 1 || (*calls)[0][0] != "push" {
			t.Fatalf("Fallback ran %v, want the one push", *calls)
		}
		if got, want := git(t, remote, "rev-parse", "topic"), git(t, work, "rev-parse", "HEAD"); got != want {
			t.Fatalf("remote topic = %s, want %s", got, want)
		}
	})
	t.Run("a hook in the repository", func(t *testing.T) {
		work, _ := newPair(t)
		write(t, filepath.Join(work, ".git", "hooks"), "pre-commit", "#!/bin/sh\nexit 0\n")
		write(t, work, "h.txt", "h\n")
		before := git(t, work, "rev-parse", "HEAD")
		b, calls := counting(t)
		b.Commit(work, "with a hook")
		if len(*calls) != 2 || (*calls)[0][0] != "add" || (*calls)[1][0] != "commit" {
			t.Fatalf("Fallback ran %v, want add and commit", *calls)
		}
		if git(t, work, "rev-parse", "HEAD") == before {
			t.Fatal("HEAD did not move")
		}
	})
	t.Run("a remote that is not bare", func(t *testing.T) {
		work, _ := newPair(t)
		other := t.TempDir()
		git(t, other, "init", "-q", "-b", "main")
		git(t, work, "remote", "add", "plain", other)
		git(t, work, "switch", "-q", "-c", "side")
		write(t, work, "s.txt", "s\n")
		b, calls := counting(t)
		b.Commit(work, "side step")
		b.Push(work, "plain", "side")
		if len(*calls) != 1 || (*calls)[0][0] != "push" {
			t.Fatalf("Fallback ran %v, want the one push", *calls)
		}
		if got, want := git(t, other, "rev-parse", "side"), git(t, work, "rev-parse", "HEAD"); got != want {
			t.Fatalf("plain remote side = %s, want %s", got, want)
		}
	})
}

// failRecorder stands in for the test of a Builder, to see that a move fails
// it without ending this test.
type failRecorder struct {
	testing.TB
	failed bool
}

func (f *failRecorder) Helper() {}
func (f *failRecorder) Fatalf(string, ...any) {
	f.failed = true
	panic(f)
}

// TestRejectedPushFails: a push the remote rejects as not a fast-forward
// fails the test, and the remote's branch stays where it was.
func TestRejectedPushFails(t *testing.T) {
	work, remote := newPair(t)
	other := filepath.Join(t.TempDir(), "other")
	git(t, "", "clone", "-q", remote, other)
	write(t, other, "o.txt", "o\n")
	git(t, other, "add", "-A")
	git(t, other, "commit", "-q", "-m", "remote moved on")
	git(t, other, "push", "-q", "origin", "main")
	moved := git(t, remote, "rev-parse", "main")

	write(t, work, "b.txt", "b\n")
	New(t, pinned()).Commit(work, "diverging")
	rec := &failRecorder{TB: t}
	func() {
		defer func() {
			if r := recover(); r != nil && r != rec {
				panic(r)
			}
		}()
		New(rec, pinned()).Push(work, "origin", "main")
	}()
	if !rec.failed {
		t.Fatal("a rejected push did not fail the test")
	}
	if got := git(t, remote, "rev-parse", "main"); got != moved {
		t.Fatalf("remote main = %s, want it untouched at %s", got, moved)
	}
}

// TestAGlobalHooksPathFallsBack: a core.hooksPath in the global config the git
// program would read is a hook gitx never sees, so the commit and the push go
// to the git program and the hook runs, as it does for a repository with
// hooks of its own.
func TestAGlobalHooksPathFallsBack(t *testing.T) {
	work, _ := newPair(t)
	hooks := t.TempDir()
	ran := filepath.Join(t.TempDir(), "ran")
	write(t, hooks, "pre-commit", "#!/bin/sh\necho commit >> \""+filepath.ToSlash(ran)+"\"\n")
	write(t, hooks, "pre-push", "#!/bin/sh\necho push >> \""+filepath.ToSlash(ran)+"\"\n")
	global := filepath.Join(t.TempDir(), "gitconfig")
	write(t, filepath.Dir(global), filepath.Base(global), "[core]\n\thooksPath = "+filepath.ToSlash(hooks)+"\n")

	b := New(t, append(pinned(), "GIT_CONFIG_GLOBAL="+global))
	var calls [][]string
	b.Fallback = func(dir string, env []string, args ...string) (string, error) {
		calls = append(calls, args)
		return gitx.RunEnv(dir, env, args...)
	}
	write(t, work, "g.txt", "g\n")
	b.Commit(work, "hooked")
	b.Push(work, "origin", "main")
	if len(calls) != 3 {
		t.Fatalf("Fallback ran %v, want add, commit and push", calls)
	}
	got, err := os.ReadFile(ran)
	if err != nil || strings.TrimSpace(strings.ReplaceAll(string(got), "\r\n", "\n")) != "commit\npush" {
		t.Fatalf("hooks ran %q (%v), want the commit and the push hook", got, err)
	}
}
