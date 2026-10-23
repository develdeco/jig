package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	skillspkg "github.com/develdeco/jig/skills"
)

// skillsRepoRoot returns the repository root, found relative to this file
// via runtime.Caller so the test works regardless of the working directory
// it is run from.
func skillsRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("skills: runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// wantSkillNames lists the repo's skills/*/SKILL.md directory names, read
// straight off disk so the test fails loudly if a skill is added or removed
// without this list changing too.
func wantSkillNames(t *testing.T) []string {
	t.Helper()
	root := skillsRepoRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatalf("glob skills/*/SKILL.md: %v", err)
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = filepath.Base(filepath.Dir(m))
	}
	sort.Strings(names)
	return names
}

// embeddedSkillNames lists the skills the binary embeds (skillspkg.FS), the
// number `jig skills install` writes: adding or removing a skill changes it
// with no test edit.
func embeddedSkillNames(t *testing.T) []string {
	t.Helper()
	entries, err := skillspkg.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded skills: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// TestCmdSkillsInstallDest installs to a temp --dest and checks that every
// skill landed, byte-equal to the repo's own SKILL.md. The count it expects
// is the number of embedded skills, and the embedded ones are the repo's.
func TestCmdSkillsInstallDest(t *testing.T) {
	t.Parallel()
	root := skillsRepoRoot(t)
	names := wantSkillNames(t)
	if embedded := embeddedSkillNames(t); len(names) == 0 || !slices.Equal(names, embedded) {
		t.Fatalf("skills/*/SKILL.md on disk = %v, embedded = %v, want the same non-empty list", names, embedded)
	}

	dest := t.TempDir()
	var buf bytes.Buffer
	code := cmdSkills(testEnv(t.TempDir()), []string{"install", "--dest", dest}, &buf)
	if code != 0 {
		t.Fatalf("cmdSkills install exit code = %d, output:\n%s", code, buf.String())
	}

	out := buf.String()
	if want := fmt.Sprintf("installed[%d]{name,path}:", len(names)); !strings.Contains(out, want) {
		t.Errorf("expected %q table, got:\n%s", want, out)
	}
	if !strings.Contains(out, "help[1]:") {
		t.Errorf("expected a help hint, got:\n%s", out)
	}

	for _, name := range names {
		want, err := os.ReadFile(filepath.Join(root, "skills", name, "SKILL.md"))
		if err != nil {
			t.Fatalf("read repo SKILL.md for %s: %v", name, err)
		}
		got, err := os.ReadFile(filepath.Join(dest, name, "SKILL.md"))
		if err != nil {
			t.Fatalf("installed SKILL.md missing for %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: installed SKILL.md does not match the repo's", name)
		}
		if !strings.Contains(out, name) {
			t.Errorf("expected installed output to name %q, got:\n%s", name, out)
		}
	}
}

// TestCmdSkillsInstallIdempotent checks that a second install overwrites in
// place rather than erroring or duplicating.
func TestCmdSkillsInstallIdempotent(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	var buf1, buf2 bytes.Buffer
	if code := cmdSkills(testEnv(t.TempDir()), []string{"install", "--dest", dest}, &buf1); code != 0 {
		t.Fatalf("first install exit code = %d, output:\n%s", code, buf1.String())
	}
	if code := cmdSkills(testEnv(t.TempDir()), []string{"install", "--dest", dest}, &buf2); code != 0 {
		t.Fatalf("second install exit code = %d, output:\n%s", code, buf2.String())
	}
	if buf1.String() != buf2.String() {
		t.Errorf("re-running install changed the output:\n--- first ---\n%s\n--- second ---\n%s", buf1.String(), buf2.String())
	}

	names := wantSkillNames(t)
	for _, name := range names {
		entries, err := os.ReadDir(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("read dest dir for %s: %v", name, err)
		}
		if len(entries) != 1 {
			t.Errorf("%s: dest dir has %d entries after two installs, want 1", name, len(entries))
		}
	}
}

// TestCmdSkillsInstallProject checks that --project installs under
// ./.claude/skills of the current directory.
func TestCmdSkillsInstallProject(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	e := testEnv(t.TempDir()).inDir(cwd)

	var buf bytes.Buffer
	code := cmdSkills(e, []string{"install", "--project"}, &buf)
	if code != 0 {
		t.Fatalf("cmdSkills install --project exit code = %d, output:\n%s", code, buf.String())
	}

	names := wantSkillNames(t)
	for _, name := range names {
		path := filepath.Join(cwd, ".claude", "skills", name, "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: expected file at %s: %v", name, path, err)
		}
	}
}

// TestCmdSkillsUnknownSubcommand checks the VALIDATION_ERROR path for a
// skills subcommand other than "install".
func TestCmdSkillsUnknownSubcommand(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	code := cmdSkills(testEnv(t.TempDir()), []string{"bogus"}, &buf)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output:\n%s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "VALIDATION_ERROR") {
		t.Fatalf("expected VALIDATION_ERROR, got:\n%s", buf.String())
	}
}

// TestCmdSkillsInstallDefaultsToTheOperatorsHome checks that with neither
// --dest nor --project the skills land under <home>/.claude/skills, the home
// being the env's own: the test never writes to a real home directory.
func TestCmdSkillsInstallDefaultsToTheOperatorsHome(t *testing.T) {
	t.Parallel()

	userHome := t.TempDir()
	e := envFrom(map[string]string{"HOME": userHome, "USERPROFILE": userHome})

	var buf bytes.Buffer
	if code := cmdSkills(e, []string{"install"}, &buf); code != 0 {
		t.Fatalf("cmdSkills install exit code = %d, output:\n%s", code, buf.String())
	}
	for _, name := range wantSkillNames(t) {
		path := filepath.Join(userHome, ".claude", "skills", name, "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: expected file at %s: %v", name, path, err)
		}
	}
}
