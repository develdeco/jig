package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

func TestExcludeFile(t *testing.T) {
	t.Parallel()
	g := testGit(t)
	repo := initRepo(t, g)
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	writeOrFail(t, exclude, "# mine\n*.log", 0o644) // no final newline, on purpose
	settings := filepath.Join(repo, "sub", ".claude", "settings.local.json")

	changed, err := ExcludeFile(settings)
	if err != nil || !changed {
		t.Fatalf("ExcludeFile = %v, %v", changed, err)
	}
	want := "# mine\n*.log\n" + blockBegin + "\n/sub/.claude/settings.local.json\n" + blockEnd + "\n"
	if got := readOrFail(t, exclude); got != want {
		t.Errorf("exclude =\n%q\nwant\n%q", got, want)
	}
	writeOrFail(t, settings, "{}", 0o644)
	if _, err := gitx.RunEnv(repo, g.Env, "check-ignore", "-q", "--", "sub/.claude/settings.local.json"); err != nil {
		t.Errorf("git does not ignore the file: %v", err)
	}
	if again, err := ExcludeFile(settings); err != nil || again {
		t.Errorf("second ExcludeFile = %v, %v, want nothing to do", again, err)
	}

	// A second file joins the one block.
	other := filepath.Join(repo, "CLAUDE.local.md")
	if changed, err := ExcludeFile(other); err != nil || !changed {
		t.Fatalf("ExcludeFile(second) = %v, %v", changed, err)
	}
	if got := readOrFail(t, exclude); strings.Count(got, blockBegin) != 1 || !strings.Contains(got, "/CLAUDE.local.md\n") || !strings.Contains(got, "/sub/.claude/settings.local.json\n") {
		t.Errorf("exclude after the second file:\n%s", got)
	}

	if changed, err := UnexcludeFile(settings); err != nil || !changed {
		t.Fatalf("UnexcludeFile = %v, %v", changed, err)
	}
	if got := readOrFail(t, exclude); strings.Contains(got, "settings.local.json") || !strings.Contains(got, "/CLAUDE.local.md") {
		t.Errorf("exclude after removing one file:\n%s", got)
	}
	if _, err := UnexcludeFile(other); err != nil {
		t.Fatal(err)
	}
	if got := readOrFail(t, exclude); got != "# mine\n*.log\n" {
		t.Errorf("exclude after removing the last file = %q, want the person's lines alone", got)
	}
	if changed, err := UnexcludeFile(other); err != nil || changed {
		t.Errorf("a second UnexcludeFile = %v, %v", changed, err)
	}
}

func TestExcludeFileLeavesThePersonsOwnLine(t *testing.T) {
	t.Parallel()
	g := testGit(t)
	repo := initRepo(t, g)
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	writeOrFail(t, exclude, "/CLAUDE.local.md\n", 0o644)
	if changed, err := ExcludeFile(filepath.Join(repo, "CLAUDE.local.md")); err != nil || changed {
		t.Errorf("ExcludeFile = %v, %v, want the person's own line to be enough", changed, err)
	}
	if got := readOrFail(t, exclude); got != "/CLAUDE.local.md\n" {
		t.Errorf("exclude = %q", got)
	}
}

func TestExcludeFileOutsideAnyRepository(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if changed, err := ExcludeFile(filepath.Join(dir, "a", "b.json")); err != nil || changed {
		t.Errorf("ExcludeFile outside a repository = %v, %v, want nothing", changed, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Errorf("ExcludeFile made a directory: %v", err)
	}
}
