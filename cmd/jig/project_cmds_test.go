package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
)

// cmdGitRepo makes dir a git repository on main with one commit and returns it.
func cmdGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"commit", "--allow-empty", "-m", "init"}} {
		if _, err := gitx.Run(dir, args...); err != nil {
			t.Fatalf("git %v in %s: %v", args, dir, err)
		}
	}
	return dir
}

// cmdBareRemote makes a bare repository and returns its path, slash-separated.
func cmdBareRemote(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if _, err := gitx.Run(filepath.Dir(dir), "init", "--bare", "-b", "main", dir); err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(dir)
}

// TestInitAsksBeforeMakingAFolderAGitRepo: an orchestrator is a git
// repository, and init runs git init in a plain folder only on the person's
// word: --yes, or a yes at the prompt. A no leaves the folder as it was.
func TestInitAsksBeforeMakingAFolderAGitRepo(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (shop string, e env) {
		t.Helper()
		shop = filepath.Join(t.TempDir(), "shop")
		cmdGitRepo(t, filepath.Join(shop, "api"))
		return shop, testEnv(t.TempDir()).inDir(shop)
	}

	t.Run("declined", func(t *testing.T) {
		t.Parallel()
		shop, e := setup(t)
		out, code := runMain(t, e.atTerminal(), "n\n", "init", "--orchestrator")
		if code != 2 || !strings.Contains(out, "Run git init there? [y/N]") || !strings.Contains(out, "NEEDS_INPUT") {
			t.Errorf("exit %d, want 2 after the question:\n%s", code, out)
		}
		for _, p := range []string{filepath.Join(shop, ".git"), filepath.Join(shop, "jig.yaml")} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("the declined init left %s: %v", p, err)
			}
		}
	})

	t.Run("accepted", func(t *testing.T) {
		t.Parallel()
		shop, e := setup(t)
		out, code := runMain(t, e.atTerminal(), "y\n", "init", "--orchestrator")
		if code != 0 || !strings.Contains(out, "git: initialized") {
			t.Fatalf("exit %d, want 0 with git initialized:\n%s", code, out)
		}
		if !project.IsRepo(shop) {
			t.Error("the orchestrator is not a repository")
		}
	})

	t.Run("no terminal, no yes", func(t *testing.T) {
		t.Parallel()
		shop, e := setup(t)
		out, code := runMain(t, e, "", "init", "--orchestrator")
		if code != 2 || strings.Contains(out, "[y/N]") || !strings.Contains(out, "jig init --orchestrator --yes") {
			t.Errorf("exit %d, want 2 pointing at --yes without asking:\n%s", code, out)
		}
		if _, err := os.Stat(filepath.Join(shop, ".git")); !os.IsNotExist(err) {
			t.Errorf("init made the folder a repository unasked: %v", err)
		}
	})
}

// TestSharePromptsBeforePushing: share shows what it will do and asks; a no
// pushes nothing and sets nothing, a yes does it.
func TestSharePromptsBeforePushing(t *testing.T) {
	t.Parallel()
	repo := cmdGitRepo(t, filepath.Join(t.TempDir(), "demo"))
	e := testEnv(t.TempDir()).inDir(repo)
	if out, code := runMain(t, e, "", "init"); code != 0 {
		t.Fatalf("init: exit %d\n%s", code, out)
	}
	storeRemote, repoRemote := cmdBareRemote(t, "demo-tickets.git"), cmdBareRemote(t, "demo.git")
	args := []string{"share", "--store-remote", storeRemote, "--remote", "demo=" + repoRemote}

	out, code := runMain(t, e.atTerminal(), "no\n", args...)
	if code != 2 || !strings.Contains(out, "This will:") || !strings.Contains(out, "push main of the store") || !strings.Contains(out, "Apply? [y/N]") {
		t.Errorf("declined share: exit %d, want 2 after showing the plan:\n%s", code, out)
	}
	if remotes, _ := gitx.Run(repo, "remote"); remotes != "" {
		t.Errorf("a declined share set remotes %q", remotes)
	}
	if refs, _ := gitx.Run(repoRemote, "for-each-ref"); refs != "" {
		t.Errorf("a declined share pushed:\n%s", refs)
	}

	out, code = runMain(t, e.atTerminal(), "yes\n", args...)
	if code != 0 || !strings.Contains(out, "marker_committed: yes") {
		t.Fatalf("accepted share: exit %d\n%s", code, out)
	}
	if refs, _ := gitx.Run(storeRemote, "for-each-ref", "--format=%(refname)"); refs != "refs/heads/main" {
		t.Errorf("the store remote has %q, want main", refs)
	}
}

// TestShareCreateNeedsGh: --create goes through gh, and without it on the PATH
// says so, before anything is changed.
func TestShareCreateNeedsGh(t *testing.T) {
	t.Parallel()
	repo := cmdGitRepo(t, filepath.Join(t.TempDir(), "demo"))
	e := testEnv(t.TempDir()).inDir(repo).withLookPath(func(string) (string, error) { return "", errors.New("not found") })
	if out, code := runMain(t, e, "", "init"); code != 0 {
		t.Fatalf("init: exit %d\n%s", code, out)
	}
	out, code := runMain(t, e, "", "share", "--create", "--yes")
	if code != 1 || !strings.Contains(out, "GH_NOT_INSTALLED") || !strings.Contains(out, "cli.github.com") {
		t.Errorf("share --create without gh: exit %d\n%s", code, out)
	}
	if remotes, _ := gitx.Run(repo, "remote"); remotes != "" {
		t.Errorf("the failed share set remotes %q", remotes)
	}
	if m, err := project.LoadMarker(filepath.Join(repo, "jig.yaml")); err != nil || m.Store != "" {
		t.Errorf("the failed share changed jig.yaml: %+v, %v", m, err)
	}
}

// TestShareOutsideAProject and friends: share and repos refuse a directory under
// no jig.yaml, naming jig init.
func TestShareAndReposOutsideAProject(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir()).inDir(t.TempDir())
	for _, args := range [][]string{{"share", "--yes"}, {"repos"}, {"repos", "sync"}, {"repos", "add", "x"}, {"repos", "remove", "x"}} {
		out, code := runMain(t, e, "", args...)
		if code != 2 || !strings.Contains(out, "not in a jig project") || !strings.Contains(out, "jig init") {
			t.Errorf("jig %s outside a project: exit %d\n%s", strings.Join(args, " "), code, out)
		}
	}
}

// TestReposSyncAsksAboutAnUnfetchedSubmodule: a listed submodule the checkout
// has not fetched is asked about at a terminal (a no drops it), and kept when
// there is no one to ask or --yes takes the default.
func TestReposSyncAsksAboutAnUnfetchedSubmodule(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (shop string, e env) {
		t.Helper()
		shop = filepath.Join(t.TempDir(), "shop")
		cmdGitRepo(t, filepath.Join(shop, "api"))
		lib := cmdGitRepo(t, filepath.Join(shop, "lib"))
		e = testEnv(t.TempDir()).inDir(shop)
		if out, code := runMain(t, e, "", "init", "--orchestrator", "--yes"); code != 0 {
			t.Fatalf("init: exit %d\n%s", code, out)
		}
		// lib becomes a submodule this checkout has not fetched.
		if err := os.RemoveAll(lib); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(lib, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(shop, ".gitmodules"), []byte("[submodule \"lib\"]\n\tpath = lib\n\turl = https://example.invalid/org/lib.git\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return shop, e
	}
	listed := func(t *testing.T, shop string) string {
		t.Helper()
		m, err := project.LoadMarker(filepath.Join(shop, "jig.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, r := range m.Repos {
			names = append(names, project.RepoName(r))
		}
		return strings.Join(names, ",")
	}

	t.Run("no", func(t *testing.T) {
		t.Parallel()
		shop, e := setup(t)
		out, code := runMain(t, e.atTerminal(), "n\n", "repos", "sync")
		if code != 0 || !strings.Contains(out, "lib is a submodule not checked out here. Keep it in the project? [Y/n]") || !strings.Contains(out, "removed: 1") {
			t.Errorf("exit %d, want lib asked about and removed:\n%s", code, out)
		}
		if !strings.Contains(out, "still declared in .gitmodules") {
			t.Errorf("a dropped submodule still in .gitmodules is not flagged:\n%s", out)
		}
		if got := listed(t, shop); got != "api" {
			t.Errorf("listed = %s, want api", got)
		}
	})
	t.Run("yes at the prompt", func(t *testing.T) {
		t.Parallel()
		shop, e := setup(t)
		out, code := runMain(t, e.atTerminal(), "\n", "repos", "sync")
		if code != 0 || !strings.Contains(out, "removed: 0") || !strings.Contains(out, "not_here: 1") {
			t.Errorf("exit %d, want lib kept:\n%s", code, out)
		}
		if got := listed(t, shop); got != "api,lib" {
			t.Errorf("listed = %s, want api,lib", got)
		}
	})
	t.Run("--yes does not ask", func(t *testing.T) {
		t.Parallel()
		shop, e := setup(t)
		out, code := runMain(t, e.atTerminal(), "n\n", "repos", "sync", "--yes")
		if code != 0 || strings.Contains(out, "[Y/n]") {
			t.Errorf("exit %d, want no question:\n%s", code, out)
		}
		if got := listed(t, shop); got != "api,lib" {
			t.Errorf("listed = %s, want api,lib", got)
		}
	})
}

// TestShareMakesARelativeRemoteAbsolute: a remote given as a path is relative
// to where the person is, not to the repo git sets it on, so the store, which
// sits beside the repo, would otherwise read it from the wrong place.
func TestShareMakesARelativeRemoteAbsolute(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	repo := cmdGitRepo(t, filepath.Join(parent, "demo"))
	for _, name := range []string{"s.git", "d.git"} {
		if _, err := gitx.Run(parent, "init", "--bare", "-b", "main", name); err != nil {
			t.Fatal(err)
		}
	}
	e := testEnv(t.TempDir()).inDir(repo)
	if out, code := runMain(t, e, "", "init"); code != 0 {
		t.Fatalf("init: exit %d\n%s", code, out)
	}
	out, code := runMain(t, e, "", "share", "--store-remote", "../s.git", "--remote", "demo=../d.git", "--yes")
	if code != 0 {
		t.Fatalf("share: exit %d\n%s", code, out)
	}
	want := filepath.ToSlash(filepath.Join(parent, "s.git"))
	if got, _ := gitx.Run(repo+"-tickets", "remote", "get-url", "origin"); got != want {
		t.Errorf("the store's origin = %q, want %q", got, want)
	}
	if m, err := project.LoadMarker(filepath.Join(repo, "jig.yaml")); err != nil || m.Store != want {
		t.Errorf("jig.yaml store = %q, %v; want %q", m.Store, err, want)
	}
}
