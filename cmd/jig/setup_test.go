package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/setup"
)

// projectSkills are the skills of the project set that this binary carries;
// a test never names one a later ticket adds.
func projectSkills(t *testing.T) []string {
	t.Helper()
	have, _, err := skillSelection{project: true}.resolve()
	if err != nil || len(have) == 0 {
		t.Fatalf("project skills = %v, %v", have, err)
	}
	return skillNames(have)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// markedProject makes a git repository with a jig.yaml marker.
func (ie installerEnv) markedProject(t *testing.T, name, marker string) string {
	t.Helper()
	dir := ie.repo(t, name)
	if err := os.WriteFile(filepath.Join(dir, project.MarkerFile), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSetupNeedsAScopeAndPieces(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)

	out, code := runMain(t, ie.env, "", "setup")
	for _, want := range []string{"code: NEEDS_INPUT", "--global", "--project <name>", "--here", "--artifacts", "--helpers", "--all", "--skills --hooks --githook --hint"} {
		if code != 2 || !strings.Contains(out, want) {
			t.Fatalf("no params: exit %d, missing %q\n%s", code, want, out)
		}
	}
	out, code = runMain(t, ie.env, "", "setup", "--global")
	if code != 2 || !strings.Contains(out, "What: --skills") || strings.Contains(out, "Where: --global") {
		t.Errorf("a scope and no pieces: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "setup", "--artifacts")
	if code != 2 || !strings.Contains(out, "Where: --global") || strings.Contains(out, "What: --skills") {
		t.Errorf("pieces and no scope: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "setup", "--global", "--here", "--skills")
	if code != 2 || !strings.Contains(out, "code: VALIDATION_ERROR") {
		t.Errorf("two scopes: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "setup", "--global", "--skills", "--harness", "emacs")
	if code != 2 || !strings.Contains(out, "code: VALIDATION_ERROR") || !strings.Contains(out, "Supported:") {
		t.Errorf("an unknown harness: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "setup", "--global", "nonesuch")
	if code != 2 || !strings.Contains(out, `jig has no skill named "nonesuch"`) {
		t.Errorf("an unknown skill: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "setup", "--global", "--skills", "--tracked")
	if code != 2 || !strings.Contains(out, "--tracked is for --project and --here") {
		t.Errorf("--tracked with --global: exit %d\n%s", code, out)
	}
	if exists(ie.userHome) || exists(ie.jigHome) {
		t.Error("a refused run wrote something")
	}
}

func TestSetupGlobalInstallsEveryPieceAndTakesOnlyItsOwnBack(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	// The person's own files, which setup must leave as they are.
	mine := "{\n  \"model\": \"opus\"\n}\n"
	writeTestFile(t, ie.settings(), mine)
	mineMd := "# My notes\n\nUse tabs.\n"
	writeTestFile(t, filepath.Join(ie.userHome, ".claude", "CLAUDE.md"), mineMd)
	for _, dir := range []string{".codex", ".gemini"} { // harnesses the person uses
		if err := os.MkdirAll(filepath.Join(ie.userHome, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(ie.userHome, ".claude", "skills", "mine", "SKILL.md"), "---\nname: mine\n---\n")

	// Without --yes and without a terminal nothing is written, not even the
	// pieces that need no yes.
	out, code := runMain(t, ie.env, "", "setup", "--global", "--artifacts", "--helpers")
	if code != 2 || !strings.Contains(out, "code: NEEDS_INPUT") || !strings.Contains(out, "Run `jig setup --global --artifacts --helpers --yes` to do it") {
		t.Fatalf("no --yes: exit %d\n%s", code, out)
	}
	if exists(filepath.Join(ie.userHome, ".claude", "skills", "here")) || ie.globalHooksPath(t) != "" {
		t.Fatal("a run that needed a yes wrote before it had one")
	}

	out, code = runMain(t, ie.env, "", "setup", "--global", "--artifacts", "--helpers", "--yes")
	if code != 0 || !strings.Contains(out, "result: installed") {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	names := append(projectSkills(t), helperSkillNames()...)
	for _, root := range []string{".claude", ".agents"} {
		for _, name := range names {
			data := readFileOrFail(t, filepath.Join(ie.userHome, root, "skills", name, "SKILL.md"))
			body, _ := embeddedBody(name)
			if v, ok := setup.SkillStamp([]byte(data)); !ok || v != binaryVersion() || !strings.HasPrefix(data, strings.TrimRight(string(body), "\n")) {
				t.Errorf("%s/skills/%s: stamp %q, %v; content differs from the embedded text", root, name, v, ok)
			}
		}
	}
	if data := readFileOrFail(t, ie.settings()); !strings.Contains(data, `"model": "opus"`) || !strings.Contains(data, "observe --harness claude") {
		t.Errorf("settings:\n%s", data)
	}
	if md := readFileOrFail(t, filepath.Join(ie.userHome, ".claude", "CLAUDE.md")); !strings.HasPrefix(md, mineMd) || !strings.Contains(md, "jig here") {
		t.Errorf("CLAUDE.md:\n%s", md)
	}
	for _, f := range []string{".codex/AGENTS.md", ".gemini/GEMINI.md"} {
		if !strings.Contains(readFileOrFail(t, filepath.Join(ie.userHome, filepath.FromSlash(f))), "jig here") {
			t.Errorf("%s has no hint", f)
		}
	}
	if got, want := ie.globalHooksPath(t), filepath.ToSlash(home.HooksDir(ie.jigHome)); !strings.EqualFold(filepath.ToSlash(got), want) {
		t.Errorf("core.hooksPath = %q, want %q", got, want)
	}

	// A second run changes nothing and needs no yes.
	out, code = runMain(t, ie.env, "", "setup", "--global", "--artifacts", "--helpers")
	if code != 0 || !strings.Contains(out, "result: unchanged") || strings.Contains(out, "installed,") {
		t.Errorf("second run: exit %d\n%s", code, out)
	}

	out, code = runMain(t, ie.env, "", "setup", "--global", "--artifacts", "--helpers", "--uninstall", "--yes")
	if code != 0 || !strings.Contains(out, "result: removed") || !strings.Contains(out, "action: uninstall") {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if got := readFileOrFail(t, ie.settings()); got != mine {
		t.Errorf("settings after uninstall = %q, want the person's own", got)
	}
	if got := readFileOrFail(t, filepath.Join(ie.userHome, ".claude", "CLAUDE.md")); got != mineMd {
		t.Errorf("CLAUDE.md after uninstall = %q, want the person's own", got)
	}
	if !exists(filepath.Join(ie.userHome, ".claude", "skills", "mine", "SKILL.md")) {
		t.Error("the person's own skill was removed")
	}
	for _, name := range names {
		if exists(filepath.Join(ie.userHome, ".claude", "skills", name)) || exists(filepath.Join(ie.userHome, ".agents", "skills", name)) {
			t.Errorf("skill %s is still there after the uninstall", name)
		}
	}
	for _, f := range []string{".codex/AGENTS.md", ".gemini/GEMINI.md"} {
		if exists(filepath.Join(ie.userHome, filepath.FromSlash(f))) {
			t.Errorf("%s, which jig made, is still there", f)
		}
	}
	if ie.globalHooksPath(t) != "" {
		t.Errorf("core.hooksPath is still set: %q", ie.globalHooksPath(t))
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSetupAsksOnceOnATerminal(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	e := ie.env.atTerminal()

	out, code := runMain(t, e, "n\n", "setup", "--global", "--skills", "--hooks", "--hint")
	if code != 0 || !strings.Contains(out, "This will:") || !strings.Contains(out, "Apply? [y/N]") || !strings.Contains(out, "result: declined") {
		t.Fatalf("declined: exit %d\n%s", code, out)
	}
	if strings.Count(out, "Apply? [y/N]") != 1 {
		t.Errorf("asked more than once:\n%s", out)
	}
	if exists(ie.userHome) {
		t.Fatal("a declined run wrote something")
	}
	out, code = runMain(t, e, "y\n", "setup", "--global", "--skills", "--hooks", "--hint")
	if code != 0 || !strings.Contains(out, "result: installed") {
		t.Fatalf("accepted: exit %d\n%s", code, out)
	}
	if !exists(ie.settings()) || !exists(filepath.Join(ie.userHome, ".claude", "CLAUDE.md")) {
		t.Error("an accepted run wrote nothing")
	}
}

func TestSetupPromptsForWhatIsMissingOnATerminal(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	e := ie.env.atTerminal()

	// Scope and pieces by their defaults, then a yes.
	out, code := runMain(t, e, "\n\ny\n", "setup")
	if code != 0 || !strings.Contains(out, "Where should jig be set up?") || !strings.Contains(out, "What should it set up?") || !strings.Contains(out, "result: installed") {
		t.Fatalf("defaults: exit %d\n%s", code, out)
	}
	if !exists(ie.settings()) || !exists(filepath.Join(ie.userHome, ".claude", "skills", projectSkills(t)[0])) {
		t.Error("the defaults (global, artifacts) were not installed")
	}

	// A scope given on the command line is not asked again.
	ie2 := newInstallerEnv(t)
	out, code = runMain(t, ie2.env.atTerminal(), "helpers\n", "setup", "--global")
	if code != 0 || strings.Contains(out, "Where should jig be set up?") || !strings.Contains(out, "What should it set up?") {
		t.Fatalf("scope given: exit %d\n%s", code, out)
	}
	if !exists(filepath.Join(ie2.userHome, ".claude", "skills", "jig-init", "SKILL.md")) || exists(ie2.settings()) {
		t.Error("helpers only was not installed as asked")
	}

	out, code = runMain(t, ie.env.atTerminal(), "global\nnonsense\n", "setup")
	if code != 2 || !strings.Contains(out, "code: VALIDATION_ERROR") {
		t.Errorf("an answer that is no choice: exit %d\n%s", code, out)
	}
}

func TestSetupExtraSkillsByName(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	out, code := runMain(t, ie.env, "", "setup", "--global", "retro", "platform-sync")
	if code != 0 || !strings.Contains(out, "result: installed") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, name := range []string{"retro", "platform-sync"} {
		if !exists(filepath.Join(ie.userHome, ".claude", "skills", name, "SKILL.md")) {
			t.Errorf("%s was not installed", name)
		}
	}
	if exists(filepath.Join(ie.userHome, ".claude", "skills", "here")) {
		t.Error("naming skills installed the project set too")
	}
	// Uninstalling by name leaves the others.
	if out, code := runMain(t, ie.env, "", "setup", "--global", "retro", "--uninstall"); code != 0 || !strings.Contains(out, "result: removed") {
		t.Fatalf("uninstall retro: exit %d\n%s", code, out)
	}
	if exists(filepath.Join(ie.userHome, ".claude", "skills", "retro")) || !exists(filepath.Join(ie.userHome, ".claude", "skills", "platform-sync", "SKILL.md")) {
		t.Error("the uninstall did not take exactly the skill it was told to")
	}
}

func TestSetupHarnessAllWritesEveryHarnessAndUninstallRemovesThem(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	out, code := runMain(t, ie.env, "", "setup", "--global", "--hooks", "--harness", "all", "--yes")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var files []string
	for _, h := range setup.Harnesses() {
		f := h.GlobalFile(ie.userHome)
		files = append(files, f)
		if !exists(f) {
			t.Errorf("%s: %s was not written", h.Name, f)
		}
	}
	if data := readFileOrFail(t, files[len(files)-1]); !strings.Contains(data, "jig-hooks-plugin") {
		t.Errorf("the opencode plugin:\n%s", data)
	}
	if out, code := runMain(t, ie.env, "", "setup", "--global", "--hooks", "--harness", "all", "--uninstall", "--yes"); code != 0 {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	for _, h := range setup.Harnesses() {
		f := h.GlobalFile(ie.userHome)
		switch h.Name {
		case "claude", "gemini":
			// Settings files that are the person's: left, with jig's entries out.
			if data := readFileOrFail(t, f); strings.Contains(data, "observe") {
				t.Errorf("%s still has jig's entries:\n%s", f, data)
			}
		default:
			if exists(f) {
				t.Errorf("%s: %s, a file made for jig's hooks, is still there", h.Name, f)
			}
		}
	}
}

func TestSetupProjectUntrackedForm(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.markedProject(t, "app", "project: demo\nkind: project\n")
	if err := project.SaveMachine(ie.jigHome, map[string]project.MachineProject{
		"demo": {Store: filepath.Join(ie.root, "store"), Clones: map[string]string{"app": repo}, Root: repo},
	}); err != nil {
		t.Fatal(err)
	}
	// Committed so that status shows only what setup adds.
	if _, err := gitx.RunEnv(repo, []string{"GIT_CONFIG_GLOBAL=" + ie.gitGlobal}, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.RunEnv(repo, []string{"GIT_CONFIG_GLOBAL=" + ie.gitGlobal}, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "init"); err != nil {
		t.Fatal(err)
	}

	out, code := runMain(t, ie.env, "", "setup", "--project", "demo", "--artifacts", "--yes")
	if code != 0 || !strings.Contains(out, "scope: project demo") || !strings.Contains(out, "result: installed") {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	for _, rel := range []string{
		".claude/skills/jig/SKILL.md", ".agents/skills/jig/SKILL.md", ".claude/settings.local.json", "CLAUDE.local.md",
	} {
		if !exists(filepath.Join(repo, filepath.FromSlash(rel))) {
			t.Errorf("%s was not written", rel)
		}
	}
	stub := readFileOrFail(t, filepath.Join(repo, ".claude", "skills", "jig", "SKILL.md"))
	for _, name := range projectSkills(t) {
		if !strings.Contains(stub, "`"+name+"`") {
			t.Errorf("the stub does not list %s:\n%s", name, stub)
		}
	}
	if v, ok := setup.SkillStamp([]byte(stub)); !ok || v != binaryVersion() {
		t.Errorf("stub stamp = %q, %v", v, ok)
	}
	if exists(filepath.Join(repo, ".claude", "skills", projectSkills(t)[0])) {
		t.Error("the untracked form wrote a skill in full")
	}
	if status, err := gitx.Run(repo, "status", "--porcelain"); err != nil || status != "" {
		t.Errorf("git status after the install = %q, %v, want a clean repository", status, err)
	}

	out, code = runMain(t, ie.env, "", "setup", "--project", "demo", "--artifacts", "--uninstall", "--yes")
	if code != 0 || !strings.Contains(out, "result: removed") {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	for _, d := range []string{".claude", ".agents", "CLAUDE.local.md"} {
		if exists(filepath.Join(repo, d)) {
			t.Errorf("%s is still there after the uninstall", d)
		}
	}
	if ex := readFileOrFail(t, filepath.Join(repo, ".git", "info", "exclude")); strings.Contains(ex, "jig") {
		t.Errorf("the exclude block is still there:\n%s", ex)
	}
}

func TestSetupProjectTrackedForm(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.markedProject(t, "app", "project: demo\nkind: project\n")
	writeTestFile(t, filepath.Join(repo, "CLAUDE.md"), "# the team's notes\n")
	e := ie.env.inDir(repo)

	out, code := runMain(t, e, "", "setup", "--here", "--skills", "--hooks", "--hint", "--tracked", "--yes")
	if code != 0 || !strings.Contains(out, "result: installed") {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	for _, name := range projectSkills(t) {
		for _, root := range []string{".claude", ".agents"} {
			if !exists(filepath.Join(repo, root, "skills", name, "SKILL.md")) {
				t.Errorf("%s/skills/%s was not written in full", root, name)
			}
		}
	}
	if exists(filepath.Join(repo, ".claude", "skills", "jig")) {
		t.Error("the tracked form wrote a stub")
	}
	if !exists(filepath.Join(repo, ".claude", "settings.json")) || exists(filepath.Join(repo, ".claude", "settings.local.json")) {
		t.Error("tracked hooks do not go in .claude/settings.json")
	}
	if !strings.Contains(readFileOrFail(t, filepath.Join(repo, "AGENTS.md")), "jig here") {
		t.Error("tracked hint is not in AGENTS.md")
	}
	if !strings.Contains(readFileOrFail(t, filepath.Join(repo, "CLAUDE.md")), "jig here") {
		t.Error("tracked hint is not in a CLAUDE.md that does not import AGENTS.md")
	}
	if exists(filepath.Join(repo, "CLAUDE.local.md")) {
		t.Error("the tracked form wrote CLAUDE.local.md")
	}
	// Tracked files are the team's: git sees them.
	status, err := gitx.Run(repo, "status", "--porcelain", "-uall")
	if err != nil || !strings.Contains(status, ".claude/settings.json") || !strings.Contains(status, "AGENTS.md") {
		t.Errorf("git status = %q, %v, want the tracked files listed", status, err)
	}

	if out, code := runMain(t, e, "", "setup", "--here", "--skills", "--hooks", "--hint", "--tracked", "--uninstall", "--yes"); code != 0 {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if got := readFileOrFail(t, filepath.Join(repo, "CLAUDE.md")); got != "# the team's notes\n" {
		t.Errorf("CLAUDE.md after the uninstall = %q", got)
	}
	for _, f := range []string{"AGENTS.md", ".claude"} {
		if exists(filepath.Join(repo, f)) {
			t.Errorf("%s, made by jig, is still there", f)
		}
	}
}

func TestSetupMembersSetsUpEachMemberRepository(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	top := filepath.Join(ie.root, "orch")
	if err := os.MkdirAll(top, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := "project: demo\nkind: orchestrator\nrepos:\n  - name: api\n    path: api\n  - name: web\n    path: web\n"
	writeTestFile(t, filepath.Join(top, project.MarkerFile), marker)
	var members []string
	for _, n := range []string{"api", "web"} {
		dir := filepath.Join(top, n)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := gitx.RunEnv(dir, []string{"GIT_CONFIG_GLOBAL=" + ie.gitGlobal}, "init", "-q", "-b", "main"); err != nil {
			t.Fatal(err)
		}
		members = append(members, dir)
	}
	e := ie.env.inDir(top)

	if out, code := runMain(t, e, "", "setup", "--here", "--skills", "--members", "--yes"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, dir := range append([]string{top}, members...) {
		if !exists(filepath.Join(dir, ".claude", "skills", "jig", "SKILL.md")) {
			t.Errorf("%s has no stub", dir)
		}
	}
	for _, dir := range members {
		if ex := readFileOrFail(t, filepath.Join(dir, ".git", "info", "exclude")); !strings.Contains(ex, "/.claude/skills/jig/SKILL.md") {
			t.Errorf("%s does not exclude its stub:\n%s", dir, ex)
		}
	}

	// Without --members only the folder itself is set up.
	other := newInstallerEnv(t)
	top2 := filepath.Join(other.root, "orch")
	writeTestFile(t, filepath.Join(top2, project.MarkerFile), marker)
	if err := os.MkdirAll(filepath.Join(top2, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, other.env.inDir(top2), "", "setup", "--here", "--skills"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if exists(filepath.Join(top2, "api", ".claude")) {
		t.Error("a member was set up without --members")
	}
	// --members is for an orchestrator.
	plain := ie.markedProject(t, "plain", "project: demo\nkind: project\n")
	if out, code := runMain(t, ie.env.inDir(plain), "", "setup", "--here", "--skills", "--members"); code != 2 || !strings.Contains(out, "orchestrator") {
		t.Errorf("--members outside an orchestrator: exit %d\n%s", code, out)
	}
}

func TestSetupHonorsTheHarnessesAProjectExpects(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.markedProject(t, "app", "project: demo\nkind: project\nexpects:\n  harness: [codex]\n  hooks: true\n")
	e := ie.env.inDir(repo)
	if out, code := runMain(t, e, "", "setup", "--here", "--hooks", "--yes"); code != 0 || !strings.Contains(out, "hooks,codex,") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !exists(filepath.Join(repo, ".codex", "hooks.json")) || exists(filepath.Join(repo, ".claude", "settings.local.json")) {
		t.Error("the hooks did not go to the harness the marker expects")
	}
	// --harness wins over the marker.
	if out, code := runMain(t, e, "", "setup", "--here", "--hooks", "--harness", "claude", "--yes"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !exists(filepath.Join(repo, ".claude", "settings.local.json")) {
		t.Error("--harness claude was not honored")
	}
}

func TestSetupGithookPieceForAClone(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.repo(t, "app")
	e := ie.env.inDir(repo)
	out, code := runMain(t, e, "", "setup", "--here", "--githook")
	if code != 0 || !strings.Contains(out, "githook,,") || !strings.Contains(out, "installed") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if data := readFileOrFail(t, filepath.Join(repo, ".git", "hooks", "post-commit")); !strings.Contains(data, "githook post-commit") {
		t.Errorf("post-commit:\n%s", data)
	}
	if out, code := runMain(t, e, "", "setup", "--here", "--githook", "--uninstall"); code != 0 || !strings.Contains(out, "removed") {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if exists(filepath.Join(repo, ".git", "hooks", "post-commit")) {
		t.Error("the post-commit jig made is still there")
	}
	// A folder with no repository is skipped, not an error.
	out, code = runMain(t, ie.env.inDir(ie.root), "", "setup", "--here", "--githook")
	if code != 1 || !strings.Contains(out, "NOT_IN_PROJECT") {
		t.Errorf("--here outside any project or repository: exit %d\n%s", code, out)
	}
}

func TestSetupCloudPrintsTheScriptAndWritesNothing(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	out, code := runMain(t, ie.env, "", "setup", "--cloud")
	if code != 0 || !strings.HasPrefix(out, "#!/bin/sh\n") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"curl -fsSL https://raw.githubusercontent.com/develdeco/jig/main/scripts/install.sh | sh",
		"jig setup --global --artifacts --helpers --yes\n",
		"$HOME/.local/bin",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("script lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "help[") || strings.Contains(out, "error:") {
		t.Errorf("the script carries framing:\n%s", out)
	}
	out, code = runMain(t, ie.env, "", "setup", "--cloud", "--hooks", "--harness", "codex,gemini")
	if code != 0 || !strings.Contains(out, "jig setup --global --hooks --harness codex,gemini --yes\n") || strings.Contains(out, "--artifacts") {
		t.Errorf("a script for hooks only: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, ie.env, "", "setup", "--cloud", "--here"); code != 2 || !strings.Contains(out, "VALIDATION_ERROR") {
		t.Errorf("--cloud with a scope: exit %d\n%s", code, out)
	}
	if exists(ie.userHome) || exists(ie.jigHome) {
		t.Error("--cloud wrote something")
	}
}

// TestSetupAndHooksHelpSayTheKnownLimits: the commands that install hooks say,
// in their own help, where a harness ignores them.
func TestSetupAndHooksHelpSayTheKnownLimits(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	for _, args := range [][]string{{"setup", "-h"}, {"hooks", "-h"}, {"attach", "-h"}} {
		out, code := runMain(t, e, "", args...)
		if code != 0 || !strings.Contains(out, "untrusted folder") || !strings.Contains(out, "--bare") {
			t.Errorf("jig %s: exit %d, no mention of untrusted folders and --bare:\n%s", strings.Join(args, " "), code, out)
		}
	}
}
