package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/setup"
)

// installerEnv is what the commands that install jig's pieces run on in a
// test: a jig home, a user home and a git global config of the test's own, and
// a jig binary that is named jig and is not on PATH.
type installerEnv struct {
	env       env
	root      string // the scratch directory everything below sits in
	jigHome   string
	userHome  string
	gitGlobal string
	exe       string
}

func newInstallerEnv(t *testing.T) installerEnv {
	t.Helper()
	root := t.TempDir()
	ie := installerEnv{
		root:      root,
		jigHome:   filepath.Join(root, "jig-home"),
		userHome:  filepath.Join(root, "user-home"),
		gitGlobal: filepath.Join(root, "gitconfig"),
		exe:       filepath.Join(root, "bin", "jig"),
	}
	if runtime.GOOS == "windows" {
		ie.exe += ".exe"
	}
	ie.env = envFrom(map[string]string{
		"JIG_HOME":          ie.jigHome,
		"HOME":              ie.userHome,
		"USERPROFILE":       ie.userHome,
		"GIT_CONFIG_GLOBAL": ie.gitGlobal,
	}).withExecutable(ie.exe).withLookPath(func(string) (string, error) { return "", errors.New("not on PATH") })
	return ie
}

// quotedExe is the program part of a command that runs the test's jig by path.
func (ie installerEnv) quotedExe() string { return `"` + filepath.ToSlash(ie.exe) + `"` }

func (ie installerEnv) settings() string {
	return filepath.Join(ie.userHome, ".claude", "settings.json")
}

// repo makes an initialized git repository under the scratch directory.
func (ie installerEnv) repo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(ie.root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.RunEnv(dir, []string{"GIT_CONFIG_GLOBAL=" + ie.gitGlobal}, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func readFileOrFail(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestHooksNeedAScopeAndConsent(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)

	out, code := runMain(t, ie.env, "", "hooks")
	if code != 2 || !strings.Contains(out, "code: NEEDS_INPUT") || !strings.Contains(out, "--global") || !strings.Contains(out, "--here") {
		t.Errorf("no scope: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "hooks", "--global", "--here")
	if code != 2 || !strings.Contains(out, "code: VALIDATION_ERROR") {
		t.Errorf("two scopes: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "hooks", "--global", "--harness", "emacs")
	if code != 2 || !strings.Contains(out, "emacs") || !strings.Contains(out, "Supported: claude, codex, gemini, cursor, copilot, opencode") {
		t.Errorf("a harness jig does not know: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "hooks", "--global", "stray")
	if code != 2 || !strings.Contains(out, "unexpected argument") {
		t.Errorf("a stray argument: exit %d\n%s", code, out)
	}

	// Without a terminal to ask on, it says what it would do and writes nothing.
	out, code = runMain(t, ie.env, "", "hooks", "--global")
	if code != 2 || !strings.Contains(out, "code: NEEDS_INPUT") || !strings.Contains(out, "Run `jig hooks --global --yes`") {
		t.Errorf("no --yes and no terminal: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(ie.settings()); !os.IsNotExist(err) {
		t.Fatalf("a refused install wrote %s: %v", ie.settings(), err)
	}

	out, code = runMain(t, ie.env, "", "hooks", "--global", "--yes")
	if code != 0 || !strings.Contains(out, "result: installed") {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	data := readFileOrFail(t, ie.settings())
	events, err := setup.ClaudeEvents([]byte(data))
	if err != nil || len(events) != len(setup.ClaudeHooks()) {
		t.Errorf("settings events = %v, %v\n%s", events, err, data)
	}
	if want := ie.quotedExe() + " observe --harness claude"; !strings.Contains(data, strings.ReplaceAll(want, `"`, `\"`)) {
		t.Errorf("settings do not run %s:\n%s", want, data)
	}

	// Nothing to change: no consent needed, and nothing is written.
	out, code = runMain(t, ie.env, "", "hooks", "--global")
	if code != 0 || !strings.Contains(out, "result: unchanged") {
		t.Errorf("second install: exit %d\n%s", code, out)
	}

	out, code = runMain(t, ie.env, "", "hooks", "--global", "--uninstall")
	if code != 2 || !strings.Contains(out, "Run `jig hooks --global --uninstall --yes`") {
		t.Errorf("uninstall without --yes: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "hooks", "--global", "--uninstall", "--yes")
	if code != 0 || !strings.Contains(out, "result: removed") {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if got := readFileOrFail(t, ie.settings()); got != "{}\n" {
		t.Errorf("settings after the uninstall = %q, want an empty object (the global file is never deleted)", got)
	}
	out, code = runMain(t, ie.env, "", "hooks", "--global", "--uninstall")
	if code != 0 || !strings.Contains(out, "result: unchanged") {
		t.Errorf("second uninstall: exit %d\n%s", code, out)
	}
}

func TestHooksAskOnATerminal(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	e := ie.env.atTerminal()

	out, code := runMain(t, e, "n\n", "hooks", "--global")
	if code != 0 || !strings.Contains(out, "Apply? [y/N]") || !strings.Contains(out, "result: declined") {
		t.Errorf("declined: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(ie.settings()); !os.IsNotExist(err) {
		t.Fatalf("a declined install wrote the file: %v", err)
	}
	out, code = runMain(t, e, "", "hooks", "--global")
	if code != 0 || !strings.Contains(out, "result: declined") {
		t.Errorf("an empty answer: exit %d\n%s", code, out)
	}
	out, code = runMain(t, e, "yes\n", "hooks", "--global")
	if code != 0 || !strings.Contains(out, "result: installed") {
		t.Errorf("accepted: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(ie.settings()); err != nil {
		t.Errorf("an accepted install wrote nothing: %v", err)
	}
}

func TestHooksKeepThePersonsFileAndRefuseWhatItCannotRead(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	if err := os.MkdirAll(filepath.Dir(ie.settings()), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "{\n    \"model\": \"opus\",\n    \"hooks\": {\n        \"Stop\": [\n            {\n                \"hooks\": [\n                    {\n                        \"type\": \"command\",\n                        \"command\": \"echo mine\"\n                    }\n                ]\n            }\n        ]\n    }\n}\n"
	if err := os.WriteFile(ie.settings(), []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, ie.env, "", "hooks", "--global", "--yes"); code != 0 {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	data := readFileOrFail(t, ie.settings())
	if !strings.HasPrefix(data, "{\n    \"model\": \"opus\",\n    \"hooks\": {\n        \"Stop\": [\n") || !strings.Contains(data, "echo mine") {
		t.Errorf("the person's file lost its layout or content:\n%s", data)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(ie.settings()); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("settings mode = %v, %v, want 0600 kept", info.Mode(), err)
		}
	}
	if out, code := runMain(t, ie.env, "", "hooks", "--global", "--uninstall", "--yes"); code != 0 {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if got := readFileOrFail(t, ie.settings()); got != mine {
		t.Errorf("the file after install and uninstall =\n%s\nwant the person's own\n%s", got, mine)
	}

	broken := "{\n  // a comment\n  \"model\": \"opus\"\n}\n"
	if err := os.WriteFile(ie.settings(), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runMain(t, ie.env, "", "hooks", "--global", "--yes")
	if code != 1 || !strings.Contains(out, "settings.json") || !strings.Contains(out, "not valid JSON") {
		t.Errorf("a file jig cannot read: exit %d\n%s", code, out)
	}
	if got := readFileOrFail(t, ie.settings()); got != broken {
		t.Errorf("a file jig cannot read was changed:\n%s", got)
	}
}

func TestHooksCommandIsBareJigOnPath(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	if err := os.MkdirAll(filepath.Dir(ie.exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ie.exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := ie.env.withLookPath(func(name string) (string, error) {
		if name != "jig" {
			t.Errorf("looked up %q on PATH, want jig", name)
		}
		return ie.exe, nil
	})
	if out, code := runMain(t, e, "", "hooks", "--global", "--yes"); code != 0 {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	if data := readFileOrFail(t, ie.settings()); !strings.Contains(data, `"command": "jig observe --harness claude"`) {
		t.Errorf("a jig found on PATH is not written bare:\n%s", data)
	}

	// A different jig on PATH is not this one.
	other := filepath.Join(ie.root, "other-jig")
	if err := os.WriteFile(other, []byte("y"), 0o755); err != nil {
		t.Fatal(err)
	}
	e = ie.env.withLookPath(func(string) (string, error) { return other, nil })
	if out, code := runMain(t, e, "", "hooks", "--global", "--yes"); code != 0 || !strings.Contains(out, "result: installed") {
		t.Fatalf("install with another jig on PATH: exit %d\n%s", code, out)
	}
	data := readFileOrFail(t, ie.settings())
	if strings.Contains(data, `"jig observe`) || !strings.Contains(data, `observe --harness claude`) || strings.Count(data, "observe --harness claude") != len(setup.ClaudeHooks()) {
		t.Errorf("the entries did not move to the quoted path (and stay one per event):\n%s", data)
	}
}

func TestHooksRefuseABinaryNotNamedJig(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	e := ie.env.withExecutable(filepath.Join(ie.root, "bin", "jig-dev"))
	out, code := runMain(t, e, "", "hooks", "--global", "--yes")
	if code != 1 || !strings.Contains(out, "code: BINARY_NAME") {
		t.Errorf("install from a binary not named jig: exit %d\n%s", code, out)
	}
	// Taking the entries out needs no command, so any binary may do it.
	if out, code := runMain(t, ie.env, "", "hooks", "--global", "--yes"); code != 0 {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "hooks", "--global", "--uninstall", "--yes"); code != 0 || !strings.Contains(out, "result: removed") {
		t.Errorf("uninstall from a binary not named jig: exit %d\n%s", code, out)
	}
}

func TestHooksProjectScope(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.repo(t, "app")
	if err := project.SaveMachine(ie.jigHome, map[string]project.MachineProject{
		"demo": {Store: filepath.Join(ie.root, "store"), Clones: map[string]string{"app": repo}, Root: repo},
	}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(repo, ".claude", "settings.local.json")

	out, code := runMain(t, ie.env, "", "hooks", "--project", "demo", "--yes")
	if code != 0 || !strings.Contains(out, "scope: project demo") || !strings.Contains(out, "result: installed") {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("no settings.local.json in the project: %v", err)
	}
	if _, err := gitx.RunEnv(repo, []string{"GIT_CONFIG_GLOBAL=" + ie.gitGlobal}, "check-ignore", "-q", "--", ".claude/settings.local.json"); err != nil {
		t.Errorf("git does not ignore the settings file jig made: %v", err)
	}
	if status, err := gitx.Run(repo, "status", "--porcelain"); err != nil || status != "" {
		t.Errorf("git status after the install = %q, %v, want a clean repository", status, err)
	}

	out, code = runMain(t, ie.env, "", "hooks", "--project", "demo", "--uninstall", "--yes")
	if code != 0 || !strings.Contains(out, "result: removed") {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("the settings file jig made is still there after the uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(file)); !os.IsNotExist(err) {
		t.Errorf("the empty .claude directory is still there: %v", err)
	}
	if exclude := readFileOrFail(t, filepath.Join(repo, ".git", "info", "exclude")); strings.Contains(exclude, "settings.local.json") {
		t.Errorf("the exclude line is still there:\n%s", exclude)
	}

	// A file the person keeps other things in survives an uninstall.
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("{\"model\": \"opus\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, ie.env, "", "hooks", "--project", "demo", "--yes"); code != 0 {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, ie.env, "", "hooks", "--project", "demo", "--uninstall", "--yes"); code != 0 {
		t.Fatalf("uninstall: exit %d\n%s", code, out)
	}
	if got := readFileOrFail(t, file); got != "{\n  \"model\": \"opus\"\n}\n" && got != "{\"model\": \"opus\"}\n" {
		t.Errorf("the person's settings after install and uninstall = %q", got)
	}

	out, code = runMain(t, ie.env, "", "hooks", "--project", "nope", "--yes")
	if code != 2 || !strings.Contains(out, `no project "nope"`) || !strings.Contains(out, "Projects on this machine: demo") {
		t.Errorf("unknown project: exit %d\n%s", code, out)
	}
}

func TestHooksProjectWithoutARootNeedsOneClone(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	one, a, b := ie.repo(t, "one"), ie.repo(t, "a"), ie.repo(t, "b")
	if err := project.SaveMachine(ie.jigHome, map[string]project.MachineProject{
		"solo":  {Clones: map[string]string{"one": one}},
		"multi": {Clones: map[string]string{"a": a, "b": b}},
	}); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, ie.env, "", "hooks", "--project", "solo", "--yes"); code != 0 {
		t.Errorf("a project with one clone and no root: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(one, ".claude", "settings.local.json")); err != nil {
		t.Errorf("the one clone got no settings: %v", err)
	}
	out, code := runMain(t, ie.env, "", "hooks", "--project", "multi", "--yes")
	if code != 1 || !strings.Contains(out, "code: NO_PROJECT_ROOT") || !strings.Contains(out, "--here") {
		t.Errorf("a project with two clones and no root: exit %d\n%s", code, out)
	}
}

func TestHooksHere(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)

	// A project marker above the working directory is the project's folder.
	projectDir := filepath.Join(ie.root, "proj")
	deep := filepath.Join(projectDir, "src", "pkg")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.WriteMarker(projectDir, project.Marker{Project: "proj", Kind: project.KindProject}); err != nil {
		t.Fatal(err)
	}
	out, code := runMain(t, ie.env.inDir(deep), "", "hooks", "--here", "--yes")
	if code != 0 || !strings.Contains(out, "result: installed") {
		t.Fatalf("with a marker: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".claude", "settings.local.json")); err != nil {
		t.Errorf("no settings at the marker folder: %v", err)
	}

	// No marker, a repository: the repository.
	repo := ie.repo(t, "plain")
	sub := filepath.Join(repo, "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, ie.env.inDir(sub), "", "hooks", "--here", "--yes"); code != 0 {
		t.Fatalf("in a repository: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude", "settings.local.json")); err != nil {
		t.Errorf("no settings at the repository top: %v", err)
	}

	// Neither.
	nowhere := filepath.Join(ie.root, "nowhere")
	if err := os.MkdirAll(nowhere, 0o755); err != nil {
		t.Fatal(err)
	}
	out, code = runMain(t, ie.env.inDir(nowhere), "", "hooks", "--here", "--yes")
	if code != 1 || !strings.Contains(out, "code: NOT_IN_PROJECT") {
		t.Errorf("outside any project: exit %d\n%s", code, out)
	}
}
