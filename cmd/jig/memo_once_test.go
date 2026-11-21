package main

import (
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// TestOneCommandAsksGitOncePerAnswer: a run that reads a repository's remote
// URL and top level at several call sites asks git for each once, and a
// second run under the same memo asks for none.
func TestOneCommandAsksGitOncePerAnswer(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.markedProject(t, "app", "project: demo\nkind: project\n")
	if _, err := gitx.RunEnv(repo, []string{"GIT_CONFIG_GLOBAL=" + ie.gitGlobal}, "remote", "add", "origin", "https://example.invalid/app.git"); err != nil {
		t.Fatal(err)
	}
	m := gitx.NewMemo()
	e := ie.env.inDir(repo)
	e.memo = m
	for i := 1; i <= 2; i++ {
		if out, code := runMain(t, e, "", "here"); code != 0 {
			t.Fatalf("here #%d: exit %d\n%s", i, code, out)
		}
	}
	// The remote URL and the top level, once each, whatever the number of
	// locators and loaders that wanted them.
	if got := m.Asked(); got != 2 {
		t.Errorf("two runs under one memo asked git %d questions, want 2 (one remote URL, one top level)", got)
	}
}

// TestShareAnswersWithTheRemoteItChanged: sharing sets a repository's origin;
// what the memo answers afterwards is the new remote, not the "no origin" the
// plan read before.
func TestShareAnswersWithTheRemoteItChanged(t *testing.T) {
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
	m := gitx.NewMemo()
	e.memo = m
	out, code := runMain(t, e, "", "share", "--store-remote", "../s.git", "--remote", "demo=../d.git", "--yes")
	if code != 0 {
		t.Fatalf("share: exit %d\n%s", code, out)
	}
	want := filepath.ToSlash(filepath.Join(parent, "d.git"))
	if got, err := m.OriginURL(repo, "origin"); err != nil || got != want {
		t.Errorf("origin after share = %q, %v; want %q", got, err, want)
	}
	// The plan read "no origin" and the run read the new one: both went
	// through the memo, and the first did not outlive the change.
	if m.Asked() < 2 {
		t.Errorf("share asked git %d times through the memo, want its two reads of the origin", m.Asked())
	}
}
