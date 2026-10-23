package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/project"
)

func (ie installerEnv) gitEnv() []string { return []string{"GIT_CONFIG_GLOBAL=" + ie.gitGlobal} }

func (ie installerEnv) globalHooksPath(t *testing.T) string {
	t.Helper()
	out, err := gitx.RunEnv(ie.root, ie.gitEnv(), "config", "--global", "--get", "core.hooksPath")
	if err != nil {
		return ""
	}
	return out
}

func TestGithookGlobalInstallsAndUninstalls(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	hooksDir := home.HooksDir(ie.jigHome)

	out, code := runMain(t, ie.env, "", "githook", "--global")
	if code != 0 || !strings.Contains(out, "result: installed") || !strings.Contains(out, "core.hooksPath: set") || !strings.Contains(out, "previous: none") {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	if got := ie.globalHooksPath(t); got != filepath.ToSlash(hooksDir) {
		t.Errorf("core.hooksPath = %q, want %q", got, filepath.ToSlash(hooksDir))
	}
	post := readFileOrFail(t, filepath.Join(hooksDir, "post-commit"))
	if !strings.Contains(post, "jig_bin="+ie.quotedExe()+"\n") || !strings.Contains(post, `"$jig_bin" githook post-commit`) {
		t.Errorf("the post-commit wrapper does not run this binary:\n%s", post)
	}

	out, code = runMain(t, ie.env, "", "githook", "--global")
	if code != 0 || !strings.Contains(out, "result: unchanged") || !strings.Contains(out, "wrappers_written: 0") {
		t.Errorf("second install: exit %d\n%s", code, out)
	}

	out, code = runMain(t, ie.env, "", "githook", "--global", "--uninstall")
	if code != 0 || !strings.Contains(out, "result: removed") || !strings.Contains(out, "core.hooksPath: unset") {
		t.Errorf("uninstall: exit %d\n%s", code, out)
	}
	if got := ie.globalHooksPath(t); got != "" {
		t.Errorf("core.hooksPath after the uninstall = %q", got)
	}
	if _, err := os.Stat(hooksDir); !os.IsNotExist(err) {
		t.Errorf("the wrappers directory is still there: %v", err)
	}
}

func TestGithookNeedsAScope(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	out, code := runMain(t, ie.env, "", "githook")
	if code != 2 || !strings.Contains(out, "code: NEEDS_INPUT") {
		t.Errorf("no scope: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "githook", "--global", "--here")
	if code != 2 || !strings.Contains(out, "code: VALIDATION_ERROR") {
		t.Errorf("two scopes: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "githook", "pre-commit")
	if code != 2 || !strings.Contains(out, `unexpected argument "pre-commit"`) {
		t.Errorf("a hook name: exit %d\n%s", code, out)
	}
}

func TestGithookHereAndProjectInstallPerClone(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	a, b := ie.repo(t, "a"), ie.repo(t, "b")
	if err := project.SaveMachine(ie.jigHome, map[string]project.MachineProject{
		"demo": {Clones: map[string]string{"a": a, "b": b}},
	}); err != nil {
		t.Fatal(err)
	}

	out, code := runMain(t, ie.env.inDir(a), "", "githook", "--here")
	if code != 0 || !strings.Contains(out, "result: installed") || !strings.Contains(out, "clones[1]{clone,state,detail}") {
		t.Fatalf("--here: exit %d\n%s", code, out)
	}
	if got := readFileOrFail(t, filepath.Join(a, ".git", "hooks", "post-commit")); !strings.Contains(got, blockMarkerForTest) {
		t.Errorf("no block in a:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(b, ".git", "hooks", "post-commit")); err == nil {
		t.Errorf("--here touched the other clone")
	}
	if got := ie.globalHooksPath(t); got != "" {
		t.Errorf("--here set the global core.hooksPath: %q", got)
	}

	out, code = runMain(t, ie.env, "", "githook", "--project", "demo")
	if code != 0 || !strings.Contains(out, "clones[2]{clone,state,detail}") || !strings.Contains(out, "result: installed") {
		t.Fatalf("--project: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "githook", "--project", "demo")
	if code != 0 || !strings.Contains(out, "result: unchanged") {
		t.Errorf("--project again: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env, "", "githook", "--project", "demo", "--uninstall")
	if code != 0 || !strings.Contains(out, "result: removed") {
		t.Errorf("--project --uninstall: exit %d\n%s", code, out)
	}
	for _, repo := range []string{a, b} {
		if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", "post-commit")); !os.IsNotExist(err) {
			t.Errorf("%s still has the post-commit jig made: %v", repo, err)
		}
	}

	// Covered by the global wrappers once those are in.
	if out, code := runMain(t, ie.env, "", "githook", "--global"); code != 0 {
		t.Fatalf("--global: exit %d\n%s", code, out)
	}
	out, code = runMain(t, ie.env.inDir(a), "", "githook", "--here")
	if code != 0 || !strings.Contains(out, "covered") || !strings.Contains(out, "result: unchanged") {
		t.Errorf("--here under the wrappers: exit %d\n%s", code, out)
	}

	nowhere := filepath.Join(ie.root, "nowhere")
	if err := os.MkdirAll(nowhere, 0o755); err != nil {
		t.Fatal(err)
	}
	out, code = runMain(t, ie.env.inDir(nowhere), "", "githook", "--here")
	if code != 1 || !strings.Contains(out, "code: NOT_IN_PROJECT") {
		t.Errorf("--here outside a repository: exit %d\n%s", code, out)
	}
}

// blockMarkerForTest is the line that opens the block in a repository's post-commit.
const blockMarkerForTest = "# >>> jig >>>"

func TestGithookPostCommitRecordsTheCommit(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	repo := ie.repo(t, "work")
	if _, err := gitx.Run(repo, "commit", "-q", "--allow-empty", "-m", "one"); err != nil {
		t.Fatal(err)
	}
	sha, err := gitx.RevParse(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 1, 12, 30, 0, 123456789, time.UTC)
	e := ie.env.inDir(repo).withClock(fixedClock(at))

	out, code := runMain(t, e, "", "githook", "post-commit")
	if code != 0 || out != "" {
		t.Fatalf("exit %d, output %q, want a silent success", code, out)
	}
	lines, err := ledger.Read(ie.jigHome)
	if err != nil || len(lines) != 1 {
		t.Fatalf("ledger = %+v, %v, want one line", lines, err)
	}
	l := lines[0]
	if l.Event != journal.EventGitCommit || l.Commit != sha || l.Cwd != repo || l.Branch != "main" || l.TS != "2026-10-01T12:30:00.123456789Z" {
		t.Errorf("ledger line = %+v, want a git-commit of %s on main in %s at the clock's time", l, sha, repo)
	}
	if l.Ticket != "" || l.Session != "" || l.Harness != "" {
		t.Errorf("ledger line carries more than commit, cwd and branch: %+v", l)
	}

	// A detached HEAD has no branch.
	if _, err := gitx.Run(repo, "checkout", "-q", "--detach"); err != nil {
		t.Fatal(err)
	}
	if _, code := runMain(t, e, "", "githook", "post-commit"); code != 0 {
		t.Fatalf("detached: exit %d", code)
	}
	lines, _ = ledger.Read(ie.jigHome)
	if len(lines) != 2 || lines[1].Branch != "" || lines[1].Commit != sha {
		t.Errorf("ledger after a detached commit = %+v", lines)
	}
}

func TestGithookPostCommitNeverFailsTheCommit(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)

	// Not a repository: it says so and exits 0.
	nowhere := filepath.Join(ie.root, "nowhere")
	if err := os.MkdirAll(nowhere, 0o755); err != nil {
		t.Fatal(err)
	}
	out, code := runMain(t, ie.env.inDir(nowhere), "", "githook", "post-commit")
	if code != 0 || !strings.HasPrefix(out, "jig githook post-commit: ") || strings.Count(out, "\n") != 1 {
		t.Errorf("outside a repository: exit %d, output %q, want one line and exit 0", code, out)
	}

	// No jig home to write to.
	repo := ie.repo(t, "work")
	if _, err := gitx.Run(repo, "commit", "-q", "--allow-empty", "-m", "one"); err != nil {
		t.Fatal(err)
	}
	bare := envFrom(map[string]string{}).inDir(repo)
	out, code = runMain(t, bare, "", "githook", "post-commit")
	if code != 0 || !strings.HasPrefix(out, "jig githook post-commit: ") {
		t.Errorf("with no jig home: exit %d, output %q", code, out)
	}

	// A ledger that cannot be written (its directory is a file).
	if err := os.MkdirAll(ie.jigHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ie.jigHome, "ledger"), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = runMain(t, ie.env.inDir(repo), "", "githook", "post-commit")
	if code != 0 || !strings.HasPrefix(out, "jig githook post-commit: ") {
		t.Errorf("with an unwritable ledger: exit %d, output %q", code, out)
	}
}

func TestGithookPostCommitIsHidden(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	out, code := runMain(t, ie.env, "", "-h")
	if code != 0 {
		t.Fatalf("help: exit %d", code)
	}
	if !strings.Contains(out, "githook,") || strings.Contains(out, "post-commit") {
		t.Errorf("help lists githook but not its hidden runner:\n%s", out)
	}
	v, rest, ok := lookupVerb([]string{"githook", "post-commit", "x"})
	if !ok || v.Spec.Name != "githook post-commit" || !v.NoFlagSet || len(rest) != 1 {
		t.Errorf("lookupVerb = %q, %v, %v", v.Spec.Name, rest, ok)
	}
	if v, _, ok := lookupVerb([]string{"githook", "--global"}); !ok || v.Spec.Name != "githook" {
		t.Errorf("lookupVerb(githook --global) = %q, %v", v.Spec.Name, ok)
	}
}
