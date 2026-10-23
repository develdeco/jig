package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/setup"
)

func TestHintGlobalWritesTheMemoryFilesOfTheHarnessesInUse(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	claudeMd := filepath.Join(ie.userHome, ".claude", "CLAUDE.md")
	codexMd := filepath.Join(ie.userHome, ".codex", "AGENTS.md")
	geminiMd := filepath.Join(ie.userHome, ".gemini", "GEMINI.md")
	mine := "# Global rules\n\n- never use the em dash\n"
	writeTestFile(t, claudeMd, mine)

	// A person with Claude Code alone gets Claude Code's file alone.
	if out, code := runMain(t, ie.env, "", "hint", "--global", "--yes"); code != 0 {
		t.Fatalf("claude alone: exit %d\n%s", code, out)
	}
	if exists(codexMd) || exists(geminiMd) || !strings.Contains(readFileOrFail(t, claudeMd), "jig here") {
		t.Fatal("the hint went to a harness the person has no directory for")
	}
	if out, code := runMain(t, ie.env, "", "hint", "--global", "--uninstall", "--yes"); code != 0 {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if readFileOrFail(t, claudeMd) != mine {
		t.Fatalf("CLAUDE.md after the uninstall:\n%s", readFileOrFail(t, claudeMd))
	}
	for _, dir := range []string{".codex", ".gemini"} {
		if err := os.MkdirAll(filepath.Join(ie.userHome, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	out, code := runMain(t, ie.env, "", "hint")
	if code != 2 || !strings.Contains(out, "NEEDS_INPUT") || !strings.Contains(out, "--global") {
		t.Errorf("no scope: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "hint", "--global")
	if code != 2 || !strings.Contains(out, "NEEDS_INPUT") || !strings.Contains(out, "Run `jig hint --global --yes` to do it") {
		t.Errorf("no --yes and no terminal: exit %d\n%s", code, out)
	}
	if exists(codexMd) || readFileOrFail(t, claudeMd) != mine {
		t.Fatal("a refused hint wrote something")
	}

	out, code = runMain(t, ie.env, "", "hint", "--global", "--yes")
	if code != 0 || !strings.Contains(out, "hint:") || !strings.Contains(out, "result: installed") {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	if got := readFileOrFail(t, claudeMd); !strings.HasPrefix(got, mine) || !strings.Contains(got, setup.HintBlock()) {
		t.Errorf("CLAUDE.md:\n%s", got)
	}
	for _, f := range []string{codexMd, geminiMd} {
		if got := readFileOrFail(t, f); got != setup.HintBlock() {
			t.Errorf("%s:\n%s", f, got)
		}
	}
	if out, code := runMain(t, ie.env, "", "hint", "--global"); code != 0 || !strings.Contains(out, "result: unchanged") {
		t.Errorf("second install: exit %d\n%s", code, out)
	}

	out, code = runMain(t, ie.env, "", "hint", "--global", "--uninstall")
	if code != 2 || !strings.Contains(out, "Run `jig hint --global --uninstall --yes` to do it") {
		t.Errorf("uninstall without --yes: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "hint", "--global", "--uninstall", "--yes")
	if code != 0 || !strings.Contains(out, "result: removed") || !strings.Contains(out, "Run `jig hint --global --yes` to put it back") {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if readFileOrFail(t, claudeMd) != mine {
		t.Errorf("CLAUDE.md after the uninstall:\n%s", readFileOrFail(t, claudeMd))
	}
	if exists(codexMd) || exists(geminiMd) {
		t.Error("memory files made by jig are still there")
	}
}

func TestHintHarnessNarrowsTheFiles(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	if out, code := runMain(t, ie.env, "", "hint", "--global", "--harness", "codex", "--yes"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !exists(filepath.Join(ie.userHome, ".codex", "AGENTS.md")) || exists(filepath.Join(ie.userHome, ".claude", "CLAUDE.md")) {
		t.Error("--harness codex wrote the wrong files")
	}
	out, code := runMain(t, ie.env, "", "hint", "--global", "--harness", "cursor", "--yes")
	if code != 2 || !strings.Contains(out, "VALIDATION_ERROR") || !strings.Contains(out, "claude, codex, gemini") {
		t.Errorf("a harness with no memory file: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, ie.env, "", "hint", "--global", "--tracked", "--yes"); code != 2 || !strings.Contains(out, "--tracked is for") {
		t.Errorf("--tracked with --global: exit %d\n%s", code, out)
	}
}

func TestHintProjectForms(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.markedProject(t, "app", "project: demo\nkind: project\n")
	e := ie.env.inDir(repo)

	out, code := runMain(t, e, "", "hint", "--here", "--yes")
	if code != 0 || !strings.Contains(out, "scope: here") {
		t.Fatalf("untracked: exit %d\n%s", code, out)
	}
	if got := readFileOrFail(t, filepath.Join(repo, "CLAUDE.local.md")); got != setup.HintBlock() {
		t.Errorf("CLAUDE.local.md:\n%s", got)
	}
	if _, err := gitx.RunEnv(repo, ie.gitEnv(), "check-ignore", "-q", "--", "CLAUDE.local.md"); err != nil {
		t.Errorf("git does not ignore CLAUDE.local.md: %v", err)
	}
	if out, code := runMain(t, e, "", "hint", "--here", "--uninstall", "--yes"); code != 0 || !strings.Contains(out, "result: removed") {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if exists(filepath.Join(repo, "CLAUDE.local.md")) {
		t.Error("CLAUDE.local.md is still there")
	}
	if ex := readFileOrFail(t, filepath.Join(repo, ".git", "info", "exclude")); strings.Contains(ex, "CLAUDE.local.md") {
		t.Errorf("the exclude line is still there:\n%s", ex)
	}

	// Tracked: the AGENTS.md the project shares; a CLAUDE.md that imports it is
	// left alone, so the text is not there twice.
	writeTestFile(t, filepath.Join(repo, "CLAUDE.md"), "@AGENTS.md\n")
	if out, code := runMain(t, e, "", "hint", "--here", "--tracked", "--yes"); code != 0 {
		t.Fatalf("tracked: exit %d\n%s", code, out)
	}
	if !strings.Contains(readFileOrFail(t, filepath.Join(repo, "AGENTS.md")), "jig here") {
		t.Error("AGENTS.md has no hint")
	}
	if got := readFileOrFail(t, filepath.Join(repo, "CLAUDE.md")); got != "@AGENTS.md\n" {
		t.Errorf("a CLAUDE.md that imports AGENTS.md was written: %q", got)
	}
	if exists(filepath.Join(repo, "CLAUDE.local.md")) {
		t.Error("the tracked form wrote CLAUDE.local.md")
	}
}

func TestHintAsksOnATerminal(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	e := ie.env.atTerminal()
	out, code := runMain(t, e, "n\n", "hint", "--global")
	if code != 0 || !strings.Contains(out, "Apply? [y/N]") || !strings.Contains(out, "result: declined") {
		t.Fatalf("declined: exit %d\n%s", code, out)
	}
	if exists(filepath.Join(ie.userHome, ".claude")) {
		t.Fatal("a declined hint wrote something")
	}
	if out, code := runMain(t, e, "y\n", "hint", "--global"); code != 0 || !strings.Contains(out, "result: installed") {
		t.Errorf("accepted: exit %d\n%s", code, out)
	}
}
