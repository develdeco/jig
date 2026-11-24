package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

// scopeRepo is a repository with a main branch of two commits and n side
// branches, each one commit past the first.
func scopeRepo(t *testing.T, n int) (dir string, tips map[string]string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := RunEnv(dir, rewritePinned, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "one")
	first := run("rev-parse", "HEAD")
	run("commit", "-q", "--allow-empty", "-m", "two")
	tips = map[string]string{"refs/heads/main": run("rev-parse", "HEAD")}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("side-%d", i)
		run("checkout", "-q", "-b", name, first)
		run("commit", "-q", "--allow-empty", "-m", name)
		tips["refs/heads/"+name] = run("rev-parse", "HEAD")
	}
	run("checkout", "-q", "main")
	return dir, tips
}

// TestScopeAnswersBranchTipsFromOneListing: inside a scope every branch of the
// repo is a lookup in one listing, whatever the number of branches asked
// about, and a branch that is not there is not found without asking again.
// Outside it, a question is asked as it always was and sees what changed.
// Not parallel: it counts the process's git.
func TestScopeAnswersBranchTipsFromOneListing(t *testing.T) {
	dir, tips := scopeRepo(t, 8)
	end := BeginScope("", "", dir)
	defer end()
	asked := gittest.CountGit(t, func() {
		for name, want := range tips {
			if got, found, err := ResolveCommit(dir, name); err != nil || !found || got != want {
				t.Errorf("%s^{commit} = %q, %v, %v, want %q", name, got, found, err, want)
			}
			if got, found, err := Resolve(dir, name); err != nil || !found || got != want {
				t.Errorf("%s = %q, %v, %v, want %q", name, got, found, err, want)
			}
		}
		if got, found, err := ResolveCommit(dir, "refs/heads/not-there"); err != nil || found || got != "" {
			t.Errorf("a branch that is not there = %q, %v, %v", got, found, err)
		}
		if _, found, _ := ResolveCommit(dir, "refs/remotes/origin/main"); found {
			t.Errorf("a remote branch that is not there was found")
		}
	})
	if asked != 1 {
		t.Errorf("%d git processes for %d branches, want the one listing", asked, len(tips))
	}
	end()
	// Out of the scope the repo is asked as before.
	if _, err := RunEnv(dir, rewritePinned, "branch", "late"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := ResolveCommit(dir, "refs/heads/late"); !found {
		t.Errorf("a branch made after the scope ended is not found")
	}
}

// TestScopeLeavesWhatItCannotListToGit: only a full branch name is answered
// from the listing; a name git resolves by its short-name rules, a tag, an
// object name and a directory the scope was not opened for are asked of git.
func TestScopeLeavesWhatItCannotListToGit(t *testing.T) {
	dir, tips := scopeRepo(t, 1)
	other, otherTips := scopeRepo(t, 1)
	end := BeginScope("", "", dir)
	defer end()
	if _, err := RunEnv(dir, rewritePinned, "tag", "v1"); err != nil {
		t.Fatal(err)
	}
	for rev, want := range map[string]string{
		"main":                  tips["refs/heads/main"],
		"refs/tags/v1":          tips["refs/heads/main"],
		tips["refs/heads/main"]: tips["refs/heads/main"],
	} {
		if got, found, err := ResolveCommit(dir, rev); err != nil || !found || got != want {
			t.Errorf("%s = %q, %v, %v, want %q", rev, got, found, err, want)
		}
	}
	// A repo the scope was not opened for sees a branch made since.
	if _, err := RunEnv(other, rewritePinned, "branch", "fresh"); err != nil {
		t.Fatal(err)
	}
	if got, found, _ := ResolveCommit(other, "refs/heads/fresh"); !found || got != otherTips["refs/heads/main"] {
		t.Errorf("a repo outside the scope = %q, %v", got, found)
	}
}

// TestScopeKeepsAncestryOnThisMachine: whether one commit is in another's
// history is asked of git once, by this process or any other: a second scope on
// the same file, with nothing in memory, answers every pair it was asked
// before without a process. A pair git cannot answer is not kept. Not parallel:
// it counts the process's git.
func TestScopeKeepsAncestryOnThisMachine(t *testing.T) {
	dir, tips := scopeRepo(t, 4)
	file := filepath.Join(t.TempDir(), "cache", "ancestry.txt")
	pairs := func() map[string]bool {
		got := map[string]bool{}
		for name := range tips {
			if name == "refs/heads/main" {
				continue
			}
			is, err := IsAncestor(dir, name, "refs/heads/main")
			if err != nil {
				t.Fatalf("IsAncestor(%s): %v", name, err)
			}
			got[name] = is
			back, err := IsAncestor(dir, "refs/heads/main", name)
			if err != nil || back {
				t.Fatalf("main is not in %s: %v, %v", name, back, err)
			}
		}
		return got
	}

	end := BeginScope(file, "", dir)
	var first map[string]bool
	asked := gittest.CountGit(t, func() { first = pairs() })
	end()
	// 1 listing, and each of the 4 side branches asked in each direction.
	if asked != 1+2*4 {
		t.Errorf("a first read asked git %d times, want 9", asked)
	}
	for name, is := range first {
		if is {
			t.Errorf("%s is not in main's history, said it was", name)
		}
	}

	// Another process: nothing is held in memory, the file is read.
	keptFiles.Lock()
	delete(keptFiles.by, file)
	keptFiles.Unlock()
	end = BeginScope(file, "", dir)
	defer end()
	var second map[string]bool
	asked = gittest.CountGit(t, func() { second = pairs() })
	if asked != 1 {
		t.Errorf("a read with the answers kept asked git %d times, want the listing alone", asked)
	}
	for name, is := range first {
		if second[name] != is {
			t.Errorf("%s: kept %v, first said %v", name, second[name], is)
		}
	}

	// A commit the repo lacks is an error, and an error is not kept.
	missing := strings.Repeat("a", 40)
	if _, err := IsAncestor(dir, missing, tips["refs/heads/main"]); err == nil {
		t.Fatalf("a commit the repo lacks was answered")
	}
	end()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), missing) {
		t.Errorf("an error was kept:\n%s", data)
	}
	if n := strings.Count(string(data), "\n"); n != 8 {
		t.Errorf("the file holds %d answers, want 8:\n%s", n, data)
	}
}
