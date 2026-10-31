package main

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/setup"
)

func TestSkillPrintsTheEmbeddedBody(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	for _, name := range embeddedNames() {
		want, _ := embeddedBody(name)
		out, code := runMain(t, e, "", "skill", name)
		if code != 0 || out != string(want) {
			t.Errorf("jig skill %s: exit %d, output differs from the embedded text (%d vs %d bytes)", name, code, len(out), len(want))
		}
	}
	out, code := runMain(t, e, "", "skill")
	if code != 0 || !strings.Contains(out, "skills["+strconv.Itoa(len(embeddedNames()))+"]{name,use it when}:") || !strings.Contains(out, "jig skill <name>") {
		t.Errorf("jig skill: exit %d\n%s", code, out)
	}
	out, code = runMain(t, e, "", "skill", "nonesuch")
	if code != 2 || !strings.Contains(out, "VALIDATION_ERROR") || !strings.Contains(out, "here") {
		t.Errorf("an unknown skill: exit %d\n%s", code, out)
	}
	for _, bad := range []string{"../x", "a/b", "HERE"} {
		if out, code := runMain(t, e, "", "skill", bad); code != 2 {
			t.Errorf("jig skill %q: exit %d\n%s", bad, code, out)
		}
	}
	if out, code := runMain(t, e, "", "skill", "here", "intake"); code != 2 || !strings.Contains(out, "at most one") {
		t.Errorf("two names: exit %d\n%s", code, out)
	}
}

// TestHelperSkillsAreCarriedAndInTheSets: the two helpers exist, are not in the
// project set, and the project set only names skills with a reason to be there.
func TestHelperSkillsAreCarriedAndInTheSets(t *testing.T) {
	t.Parallel()
	for _, name := range helperSkillNames() {
		if _, ok := embeddedBody(name); !ok {
			t.Errorf("helper skill %s is not embedded", name)
		}
		if slices.Contains(projectSkillNames(), name) {
			t.Errorf("helper %s is in the project set", name)
		}
	}
	if got, want := projectSkillNames(), []string{"here", "intake", "chart", "build", "review", "publish"}; !slices.Equal(got, want) {
		t.Errorf("project set = %v, want %v", got, want)
	}
	if got, want := helperSkillNames(), []string{"jig-init", "jig-attach"}; !slices.Equal(got, want) {
		t.Errorf("helpers = %v, want %v", got, want)
	}
}

func TestSkillsPieceWritesBothRootsAndTakesBackOnlyJigs(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	out, code := runMain(t, ie.env, "", "skills")
	if code != 2 || !strings.Contains(out, "NEEDS_INPUT") {
		t.Errorf("no scope: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "skills", "--global")
	if code != 0 || !strings.Contains(out, "skills:") || !strings.Contains(out, "result: installed") {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	for _, name := range projectSkills(t) {
		for _, root := range []string{".claude", ".agents"} {
			data := readFileOrFail(t, filepath.Join(ie.userHome, root, "skills", name, "SKILL.md"))
			if v, ok := setup.SkillStamp([]byte(data)); !ok || v != binaryVersion() {
				t.Errorf("%s/%s: stamp %q, %v", root, name, v, ok)
			}
		}
	}
	if exists(filepath.Join(ie.userHome, ".claude", "skills", "jig-init")) {
		t.Error("the default installed the helpers")
	}
	if out, code := runMain(t, ie.env, "", "skills", "--global"); code != 0 || !strings.Contains(out, "result: unchanged") {
		t.Errorf("second install: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, ie.env, "", "skills", "--global", "--helpers"); code != 0 || !strings.Contains(out, "result: installed") {
		t.Fatalf("helpers: exit %d\n%s", code, out)
	}
	if !exists(filepath.Join(ie.userHome, ".agents", "skills", "jig-attach", "SKILL.md")) {
		t.Error("--helpers did not install jig-attach")
	}
	if out, code := runMain(t, ie.env, "", "skills", "--global", "--all", "retro"); code != 0 {
		t.Fatalf("all and an extra: exit %d\n%s", code, out)
	}

	// A skill of the person's own, named like none of jig's, and a stale copy of
	// one the old install wrote, are told apart.
	writeTestFile(t, filepath.Join(ie.userHome, ".claude", "skills", "mine", "SKILL.md"), "---\nname: mine\n---\n")
	out, code = runMain(t, ie.env, "", "skills", "--global", "--uninstall")
	if code != 0 || !strings.Contains(out, "result: removed") {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if !exists(filepath.Join(ie.userHome, ".claude", "skills", "mine", "SKILL.md")) {
		t.Error("the person's own skill was removed")
	}
	for _, name := range append(projectSkills(t), "jig-init", "retro") {
		if exists(filepath.Join(ie.userHome, ".claude", "skills", name)) || exists(filepath.Join(ie.userHome, ".agents", "skills", name)) {
			t.Errorf("%s is still installed", name)
		}
	}
	if out, code := runMain(t, ie.env, "", "skills", "--global", "--uninstall"); code != 0 || !strings.Contains(out, "result: unchanged") {
		t.Errorf("second uninstall: exit %d\n%s", code, out)
	}
}

// TestSkillsUninstallRemovesWhatTheOlderInstallWrote: the files `skills
// install` wrote carry no stamp; a text equal to the embedded one is still
// recognized as jig's, and anything else is left.
func TestSkillsUninstallRemovesWhatTheOlderInstallWrote(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	if out, code := runMain(t, ie.env, "", "skills", "install"); code != 0 {
		t.Fatalf("skills install: exit %d\n%s", code, out)
	}
	// The older install overwrote a person's skill named like jig's; here one
	// was edited afterwards.
	edited := filepath.Join(ie.userHome, ".claude", "skills", "retro", "SKILL.md")
	if err := os.WriteFile(edited, []byte("my own retro\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, ie.env, "", "skills", "--global", "--uninstall"); code != 0 {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	for _, name := range embeddedNames() {
		path := filepath.Join(ie.userHome, ".claude", "skills", name, "SKILL.md")
		if name == "retro" {
			if !exists(path) {
				t.Error("an edited skill was removed")
			}
			continue
		}
		if exists(path) {
			t.Errorf("%s, written by skills install, is still there", name)
		}
	}
}

func TestSkillsPieceProjectForms(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.markedProject(t, "app", "project: demo\nkind: project\n")
	e := ie.env.inDir(repo)

	if out, code := runMain(t, e, "", "skills", "--here"); code != 0 || !strings.Contains(out, "scope: here") {
		t.Fatalf("stub: exit %d\n%s", code, out)
	}
	if !exists(filepath.Join(repo, ".agents", "skills", "jig", "SKILL.md")) || exists(filepath.Join(repo, ".claude", "skills", "here")) {
		t.Error("the untracked form is one stub")
	}
	if out, code := runMain(t, e, "", "skills", "--here", "--uninstall"); code != 0 || !strings.Contains(out, "result: removed") {
		t.Fatalf("remove stub: exit %d\n%s", code, out)
	}
	if exists(filepath.Join(repo, ".claude")) || exists(filepath.Join(repo, ".agents")) {
		t.Error("the empty directories of the stub are still there")
	}
	if out, code := runMain(t, e, "", "skills", "--here", "--tracked", "intake"); code != 0 {
		t.Fatalf("tracked: exit %d\n%s", code, out)
	}
	if !exists(filepath.Join(repo, ".claude", "skills", "intake", "SKILL.md")) || exists(filepath.Join(repo, ".claude", "skills", "here")) {
		t.Error("tracked, naming a skill, writes that skill in full")
	}
}

func TestSkillsInstallStillWorksAsBefore(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	out, code := runMain(t, testEnv(t.TempDir()), "", "skills", "install", "--dest", dest)
	if code != 0 || !strings.Contains(out, "installed[") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, name := range embeddedNames() {
		want, _ := embeddedBody(name)
		got, err := os.ReadFile(filepath.Join(dest, name, "SKILL.md"))
		if err != nil || string(got) != string(want) {
			t.Errorf("%s: %v (installed text differs from the embedded one)", name, err)
		}
	}
	// Its flags are its own: --project is a switch there and a name elsewhere.
	if out, code := runMain(t, testEnv(t.TempDir()), "", "skills", "install", "-h"); code != 0 || !strings.Contains(out, "flags{skills install}") || !strings.Contains(out, "--dest") {
		t.Errorf("skills install -h: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, testEnv(t.TempDir()), "", "skills", "-h"); code != 0 || !strings.Contains(out, "flags{skills}") || !strings.Contains(out, "--helpers") {
		t.Errorf("skills -h: exit %d\n%s", code, out)
	}
}

// TestSkillsStubLeavesAPersonsOwnJigSkill: the stub is named jig, which is no
// skill jig ships, so a skill of that name that jig did not write is the
// person's, and the install leaves it.
func TestSkillsStubLeavesAPersonsOwnJigSkill(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.markedProject(t, "app", "project: demo\nkind: project\n")
	mine := filepath.Join(repo, ".claude", "skills", "jig", "SKILL.md")
	writeTestFile(t, mine, "---\nname: jig\ndescription: mine\n---\nmy own\n")
	out, code := runMain(t, ie.env.inDir(repo), "", "skills", "--here")
	if code != 0 || !strings.Contains(out, "skipped") || !strings.Contains(out, "a skill of your own") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if got, _ := os.ReadFile(mine); !strings.Contains(string(got), "my own") {
		t.Errorf("the person's skill was overwritten: %q", got)
	}
	// The other root had none and got the stub.
	if !exists(filepath.Join(repo, ".agents", "skills", "jig", "SKILL.md")) {
		t.Error("the root without a skill of the person's got no stub")
	}
	// An uninstall leaves the person's skill too.
	if out, code := runMain(t, ie.env.inDir(repo), "", "skills", "--here", "--uninstall"); code != 0 {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if !exists(mine) || exists(filepath.Join(repo, ".agents")) {
		t.Error("the uninstall took the person's skill or left the stub")
	}
}
