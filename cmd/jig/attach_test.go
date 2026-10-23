package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachRefusesOutsideAProject(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	// A git repository with no marker is not a project.
	repo := ie.repo(t, "plain")
	for _, dir := range []string{repo, ie.root} {
		out, code := runMain(t, ie.env.inDir(dir), "", "attach", "--yes")
		if code != 1 || !strings.Contains(out, "code: NOT_IN_PROJECT") || !strings.Contains(out, "jig init") {
			t.Errorf("attach in %s: exit %d\n%s", dir, code, out)
		}
	}
	if exists(filepath.Join(repo, ".claude")) || exists(ie.userHome) {
		t.Error("a refused attach wrote something")
	}
}

func TestAttachSetsUpTheProjectItIsIn(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.markedProject(t, "app", "project: demo\nkind: project\n")
	sub := filepath.Join(repo, "internal", "deep")
	writeTestFile(t, filepath.Join(sub, "x.go"), "package x\n")
	e := ie.env.inDir(sub)

	// Consent is asked for, as for the hooks: no terminal means NEEDS_INPUT.
	out, code := runMain(t, e, "", "attach")
	if code != 2 || !strings.Contains(out, "NEEDS_INPUT") || !strings.Contains(out, "Run `jig attach --yes` to do it") {
		t.Fatalf("attach without --yes: exit %d\n%s", code, out)
	}
	if exists(filepath.Join(repo, ".claude")) {
		t.Fatal("a run that needed a yes wrote before it had one")
	}

	out, code = runMain(t, e, "", "attach", "--yes")
	if code != 0 || !strings.Contains(out, "attach:") || !strings.Contains(out, "scope: here") || !strings.Contains(out, "result: installed") {
		t.Fatalf("attach: exit %d\n%s", code, out)
	}
	// Four pieces, in the folder of the marker and not the one it ran from.
	for _, rel := range []string{".claude/skills/jig/SKILL.md", ".claude/settings.local.json", "CLAUDE.local.md"} {
		if !exists(filepath.Join(repo, filepath.FromSlash(rel))) {
			t.Errorf("%s was not written in the project's folder", rel)
		}
	}
	if exists(filepath.Join(sub, ".claude")) {
		t.Error("attach wrote in the folder it was run from")
	}
	if !strings.Contains(out, "githook,") {
		t.Errorf("attach did not set up the git hook:\n%s", out)
	}

	// Naming pieces narrows it.
	ie2 := newInstallerEnv(t)
	repo2 := ie2.markedProject(t, "app", "project: demo\nkind: project\n")
	if out, code := runMain(t, ie2.env.inDir(repo2), "", "attach", "--skills"); code != 0 || strings.Contains(out, "hooks,") {
		t.Fatalf("attach --skills: exit %d\n%s", code, out)
	}
	if exists(filepath.Join(repo2, ".claude", "settings.local.json")) {
		t.Error("attach --skills wrote hooks")
	}

	out, code = runMain(t, e, "", "attach", "--uninstall", "--yes")
	if code != 0 || !strings.Contains(out, "result: removed") {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if exists(filepath.Join(repo, ".claude")) || exists(filepath.Join(repo, "CLAUDE.local.md")) {
		t.Error("the uninstall left jig's files behind")
	}
}
