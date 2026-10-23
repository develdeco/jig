package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// testGit is a GitConfig whose global config is a file of the test's own, so
// nothing a test writes reaches the machine's or another test's.
func testGit(t *testing.T) GitConfig {
	t.Helper()
	return GitConfig{Env: []string{"GIT_CONFIG_GLOBAL=" + filepath.Join(t.TempDir(), "gitconfig")}}
}

func (g GitConfig) mustGlobal(t *testing.T) string {
	t.Helper()
	v, err := g.get("", "--global", hooksPathKey)
	if err != nil {
		t.Fatal(err)
	}
	return v.Raw
}

func initRepo(t *testing.T, g GitConfig) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := gitx.RunEnv(dir, g.Env, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func commitAll(t *testing.T, g GitConfig, dir, msg string) {
	t.Helper()
	if _, err := gitx.RunEnv(dir, g.Env, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.RunEnv(dir, g.Env, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", msg); err != nil {
		t.Fatal(err)
	}
}

func readOrFail(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeOrFail(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

func TestWrapperScripts(t *testing.T) {
	t.Parallel()
	hooks := ClientHooks()
	for _, banned := range []string{"reference-transaction", "fsmonitor-watchman"} {
		if slices.Contains(hooks, banned) {
			t.Errorf("%s is wrapped; it must not be", banned)
		}
	}
	for _, must := range []string{"pre-commit", "post-commit", "commit-msg", "pre-push", "post-rewrite", "post-checkout"} {
		if !slices.Contains(hooks, must) {
			t.Errorf("%s is not wrapped", must)
		}
	}
	seen := map[string]bool{}
	for _, hook := range hooks {
		if seen[hook] {
			t.Errorf("%s is listed twice", hook)
		}
		seen[hook] = true
		s := WrapperScript(hook, `"/x y/jig"`)
		if !strings.HasPrefix(s, "#!/bin/sh\n"+wrapperMarker+": "+hook+"\n") || !isWrapper([]byte(s)) {
			t.Errorf("%s: wrapper does not open as one:\n%s", hook, s)
		}
		for _, want := range []string{"hook=" + hook + "\n", `jig_bin="/x y/jig"` + "\n", `"$common/hooks/$hook"`, `"$previous/$hook"`, "exit \"$status\""} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: wrapper lacks %q", hook, want)
			}
		}
		if got, want := strings.Contains(s, "githook post-commit"), hook == "post-commit"; got != want {
			t.Errorf("%s: jig's own step present = %v, want %v", hook, got, want)
		}
		if got, want := strings.Contains(s, "mktemp"), hook == "pre-push" || hook == "post-rewrite"; got != want {
			t.Errorf("%s: keeps a copy of stdin = %v, want %v", hook, got, want)
		}
	}
	if isWrapper([]byte("#!/bin/sh\necho mine\n")) || isWrapper(nil) {
		t.Errorf("a file that is not jig's passes for a wrapper")
	}
}

func TestInstallGlobalSavesAndChainsThePreviousHooksPath(t *testing.T) {
	t.Parallel()
	g := testGit(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	if err := g.set(t.TempDir(), "--global", hooksPathKey, "~/prev-hooks"); err != nil {
		t.Fatal(err)
	}

	res, err := g.InstallGlobal(hooksDir, "jig")
	if err != nil {
		t.Fatal(err)
	}
	if res.Config != "set" || res.Previous != "~/prev-hooks" || !res.Changed || len(res.Wrote) != len(ClientHooks()) {
		t.Errorf("first install = %+v", res)
	}
	if got := readOrFail(t, filepath.Join(hooksDir, previousFile)); got != "~/prev-hooks\n" {
		t.Errorf(".previous = %q, want the value as written", got)
	}
	if got := g.mustGlobal(t); got != filepath.ToSlash(hooksDir) {
		t.Errorf("core.hooksPath = %q, want %q", got, filepath.ToSlash(hooksDir))
	}
	for _, hook := range ClientHooks() {
		info, err := os.Stat(filepath.Join(hooksDir, hook))
		if err != nil {
			t.Fatalf("no wrapper for %s: %v", hook, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			t.Errorf("wrapper %s is not executable: %v", hook, info.Mode())
		}
	}

	again, err := g.InstallGlobal(hooksDir, "jig")
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed || again.Config != "unchanged" || len(again.Wrote) != 0 || again.Previous != "~/prev-hooks" {
		t.Errorf("second install = %+v, want nothing to do and the same previous", again)
	}
	st, err := g.InspectGlobal(hooksDir, "jig")
	if err != nil || !st.Active || st.Previous != "~/prev-hooks" || len(st.Stale) != 0 {
		t.Errorf("inspect = %+v, %v", st, err)
	}
	if st, _ := g.InspectGlobal(hooksDir, `"/elsewhere/jig"`); len(st.Stale) != len(ClientHooks()) {
		t.Errorf("wrappers for another jig path are not stale: %v", st.Stale)
	}

	moved, err := g.InstallGlobal(hooksDir, `"/elsewhere/jig"`)
	if err != nil || !moved.Changed || len(moved.Wrote) != len(ClientHooks()) || moved.Config != "unchanged" {
		t.Errorf("install for a moved binary = %+v, %v, want every wrapper rewritten", moved, err)
	}

	out, err := g.UninstallGlobal(hooksDir)
	if err != nil {
		t.Fatal(err)
	}
	if out.Config != "restored" || !out.Changed || len(out.Removed) != len(ClientHooks()) {
		t.Errorf("uninstall = %+v", out)
	}
	if got := g.mustGlobal(t); got != "~/prev-hooks" {
		t.Errorf("core.hooksPath after uninstall = %q, want it back as it was", got)
	}
	if _, err := os.Stat(hooksDir); !os.IsNotExist(err) {
		t.Errorf("the hooks directory is still there: %v", err)
	}
	if rest, err := g.UninstallGlobal(hooksDir); err != nil || rest.Changed || rest.Config != "left" {
		t.Errorf("a second uninstall = %+v, %v, want nothing to do", rest, err)
	}
}

func TestInstallGlobalWithNothingBeforeUnsetsOnUninstall(t *testing.T) {
	t.Parallel()
	g := testGit(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")

	res, err := g.InstallGlobal(hooksDir, "jig")
	if err != nil || res.Previous != "" {
		t.Fatalf("install = %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, previousFile)); !os.IsNotExist(err) {
		t.Errorf(".previous exists with nothing to save: %v", err)
	}
	out, err := g.UninstallGlobal(hooksDir)
	if err != nil || out.Config != "unset" {
		t.Fatalf("uninstall = %+v, %v", out, err)
	}
	if got := g.mustGlobal(t); got != "" {
		t.Errorf("core.hooksPath = %q after uninstall, want it unset", got)
	}
}

func TestUninstallGlobalLeavesAHooksPathSomeoneElseSet(t *testing.T) {
	t.Parallel()
	g := testGit(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	if _, err := g.InstallGlobal(hooksDir, "jig"); err != nil {
		t.Fatal(err)
	}
	if err := g.set(t.TempDir(), "--global", hooksPathKey, "/their/hooks"); err != nil {
		t.Fatal(err)
	}
	out, err := g.UninstallGlobal(hooksDir)
	if err != nil || out.Config != "left" || len(out.Removed) != len(ClientHooks()) {
		t.Errorf("uninstall = %+v, %v, want the wrappers gone and the setting left", out, err)
	}
	if got := g.mustGlobal(t); got != "/their/hooks" {
		t.Errorf("core.hooksPath = %q, want theirs left alone", got)
	}
}

func TestInstallGlobalChainsTheSystemHooksPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	system := filepath.Join(dir, "system-gitconfig")
	writeOrFail(t, system, "[core]\n\thooksPath = /sys/hooks\n", 0o644)
	g := GitConfig{Env: []string{
		"GIT_CONFIG_GLOBAL=" + filepath.Join(dir, "gitconfig"),
		"GIT_CONFIG_SYSTEM=" + system,
		"GIT_CONFIG_NOSYSTEM=0",
	}}
	if g.systemHooksPath() != "/sys/hooks" {
		t.Skip("this git does not read GIT_CONFIG_SYSTEM")
	}
	hooksDir := filepath.Join(dir, "hooks")
	res, err := g.InstallGlobal(hooksDir, "jig")
	if err != nil || res.Previous != "/sys/hooks" {
		t.Fatalf("install = %+v, %v, want the system value chained", res, err)
	}
	if got := readOrFail(t, filepath.Join(hooksDir, previousFile)); got != "/sys/hooks\n" {
		t.Errorf(".previous = %q", got)
	}
	out, err := g.UninstallGlobal(hooksDir)
	if err != nil || out.Config != "unset" {
		t.Fatalf("uninstall = %+v, %v, want the global setting unset, the system one never having been in it", out, err)
	}
	if got := g.mustGlobal(t); got != "" {
		t.Errorf("global core.hooksPath = %q after uninstall", got)
	}
}

func TestInstallGlobalRefusesAFileThatIsNotAWrapper(t *testing.T) {
	t.Parallel()
	g := testGit(t)
	hooksDir := filepath.Join(t.TempDir(), "hooks")
	writeOrFail(t, filepath.Join(hooksDir, "pre-commit"), "#!/bin/sh\necho mine\n", 0o755)

	_, err := g.InstallGlobal(hooksDir, "jig")
	if err == nil || !strings.Contains(err.Error(), "pre-commit") {
		t.Fatalf("install = %v, want a refusal naming the file", err)
	}
	if got := g.mustGlobal(t); got != "" {
		t.Errorf("core.hooksPath = %q after a refused install, want it untouched", got)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "post-commit")); !os.IsNotExist(err) {
		t.Errorf("a wrapper was written by a refused install")
	}
	// An uninstall leaves the file, and so the directory.
	if _, err := g.UninstallGlobal(hooksDir); err != nil {
		t.Fatal(err)
	}
	if got := readOrFail(t, filepath.Join(hooksDir, "pre-commit")); got != "#!/bin/sh\necho mine\n" {
		t.Errorf("an uninstall touched a file that is not jig's: %q", got)
	}
}

func TestInstallCloneAppendsABlockAndUninstallRestores(t *testing.T) {
	t.Parallel()
	g := testGit(t)
	repo := initRepo(t, g)
	hooksDir := filepath.Join(t.TempDir(), "jig-hooks")
	hook := filepath.Join(repo, ".git", "hooks", "post-commit")
	const original = "#!/bin/sh\necho mine"
	writeOrFail(t, hook, original, 0o755) // no final newline, on purpose

	res, err := g.InstallClone(repo, hooksDir, "jig")
	if err != nil || res.State != CloneHasBlock || !res.Changed || !sameFileOnDisk(res.Path, hook) {
		t.Fatalf("install = %+v, %v", res, err)
	}
	got := readOrFail(t, hook)
	if want := original + "\n" + CloneBlock("jig"); got != want {
		t.Errorf("post-commit =\n%q\nwant\n%q", got, want)
	}
	if info, _ := os.Stat(hook); runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the hook lost its execute bit: %v", info.Mode())
	}

	if res, err := g.InstallClone(repo, hooksDir, "jig"); err != nil || res.Changed {
		t.Errorf("second install = %+v, %v, want nothing to do", res, err)
	}
	moved, err := g.InstallClone(repo, hooksDir, `"/elsewhere/jig"`)
	if err != nil || !moved.Changed {
		t.Fatalf("install for a moved binary = %+v, %v", moved, err)
	}
	got = readOrFail(t, hook)
	if strings.Count(got, blockBegin) != 1 || !strings.Contains(got, `"/elsewhere/jig" githook post-commit`) {
		t.Errorf("the block was not replaced in place:\n%s", got)
	}

	out, err := g.UninstallClone(repo, hooksDir)
	if err != nil || !out.Changed || out.State != CloneNone {
		t.Fatalf("uninstall = %+v, %v", out, err)
	}
	if got := readOrFail(t, hook); got != original+"\n" {
		t.Errorf("post-commit after uninstall = %q, want the person's script", got)
	}
	if out, err := g.UninstallClone(repo, hooksDir); err != nil || out.Changed {
		t.Errorf("second uninstall = %+v, %v", out, err)
	}
}

func TestInstallCloneCreatesAndRemovesItsOwnHook(t *testing.T) {
	t.Parallel()
	g := testGit(t)
	repo := initRepo(t, g)
	hook := filepath.Join(repo, ".git", "hooks", "post-commit")
	if _, err := os.Stat(hook); err == nil {
		t.Fatal("a fresh repository has a post-commit")
	}
	res, err := g.InstallClone(repo, filepath.Join(t.TempDir(), "h"), "jig")
	if err != nil || res.State != CloneHasBlock {
		t.Fatalf("install = %+v, %v", res, err)
	}
	if got := readOrFail(t, hook); got != "#!/bin/sh\n"+CloneBlock("jig") {
		t.Errorf("new post-commit = %q", got)
	}
	if _, err := g.UninstallClone(repo, filepath.Join(t.TempDir(), "h")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hook); !os.IsNotExist(err) {
		t.Errorf("the post-commit jig made is still there after the uninstall: %v", err)
	}
}

func TestInstallCloneLeavesWhatItCannotEdit(t *testing.T) {
	t.Parallel()
	g := testGit(t)
	repo := initRepo(t, g)
	hook := filepath.Join(repo, ".git", "hooks", "post-commit")
	const py = "#!/usr/bin/env python3\nprint('mine')\n"
	writeOrFail(t, hook, py, 0o755)

	res, err := g.InstallClone(repo, filepath.Join(t.TempDir(), "h"), "jig")
	if err != nil || res.State != CloneSkipped || res.Changed || !strings.Contains(res.Reason, "python3") {
		t.Fatalf("install = %+v, %v, want it skipped, naming the interpreter", res, err)
	}
	if got := readOrFail(t, hook); got != py {
		t.Errorf("the python hook was edited: %q", got)
	}

	// An env shebang that names a shell is a shell script.
	writeOrFail(t, hook, "#!/usr/bin/env -S bash -e\necho mine\n", 0o755)
	if res, err := g.InstallClone(repo, filepath.Join(t.TempDir(), "h"), "jig"); err != nil || res.State != CloneHasBlock {
		t.Errorf("install into an env bash script = %+v, %v", res, err)
	}
	// A block opened and never closed is not guessed at.
	writeOrFail(t, hook, "#!/bin/sh\n"+blockBegin+"\necho half\n", 0o755)
	if _, err := g.InstallClone(repo, filepath.Join(t.TempDir(), "h"), "jig"); err == nil {
		t.Errorf("install into a hook with an unclosed block did not fail")
	}
}

func TestInstallCloneWithItsOwnHooksPath(t *testing.T) {
	t.Parallel()
	hooksDir := filepath.Join(t.TempDir(), "jig-hooks")

	t.Run("covered by the wrappers", func(t *testing.T) {
		t.Parallel()
		g := testGit(t)
		repo := initRepo(t, g)
		if _, err := g.InstallGlobal(hooksDir, "jig"); err != nil {
			t.Fatal(err)
		}
		res, err := g.InstallClone(repo, hooksDir, "jig")
		if err != nil || res.State != CloneCovered || res.Changed {
			t.Errorf("a repository under the global wrappers = %+v, %v, want covered", res, err)
		}
		if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", "post-commit")); err == nil {
			t.Errorf("a covered repository got a post-commit anyway")
		}
	})

	t.Run("outside the work tree", func(t *testing.T) {
		t.Parallel()
		g := testGit(t)
		repo := initRepo(t, g)
		own := t.TempDir()
		if _, err := gitx.RunEnv(repo, g.Env, "config", "core.hooksPath", filepath.ToSlash(own)); err != nil {
			t.Fatal(err)
		}
		res, err := g.InstallClone(repo, hooksDir, "jig")
		if err != nil || res.State != CloneHasBlock || !sameFileOnDisk(res.Path, filepath.Join(own, "post-commit")) {
			t.Fatalf("install = %+v, %v, want the block in the repository's own hooks path", res, err)
		}
		if out, err := g.UninstallClone(repo, hooksDir); err != nil || !out.Changed {
			t.Errorf("uninstall = %+v, %v", out, err)
		}
		if _, err := os.Stat(filepath.Join(own, "post-commit")); !os.IsNotExist(err) {
			t.Errorf("the post-commit jig made is still there: %v", err)
		}
	})

	t.Run("set for every repository", func(t *testing.T) {
		t.Parallel()
		g := testGit(t)
		repo := initRepo(t, g)
		shared := t.TempDir()
		if err := g.set(t.TempDir(), "--global", hooksPathKey, filepath.ToSlash(shared)); err != nil {
			t.Fatal(err)
		}
		res, err := g.InstallClone(repo, hooksDir, "jig")
		if err != nil || res.State != CloneSkipped || res.Changed || !strings.Contains(res.Reason, "jig githook --global") {
			t.Fatalf("install = %+v, %v, want the shared hooks directory left alone", res, err)
		}
		if entries, _ := os.ReadDir(shared); len(entries) != 0 {
			t.Errorf("the shared hooks directory was written to: %v", entries)
		}
		if out, err := g.UninstallClone(repo, hooksDir); err != nil || out.Changed {
			t.Errorf("uninstall = %+v, %v", out, err)
		}
	})

	t.Run("inside the work tree and tracked", func(t *testing.T) {
		t.Parallel()
		g := testGit(t)
		repo := initRepo(t, g)
		if _, err := gitx.RunEnv(repo, g.Env, "config", "core.hooksPath", ".githooks"); err != nil {
			t.Fatal(err)
		}
		const mine = "#!/bin/sh\necho tracked\n"
		writeOrFail(t, filepath.Join(repo, ".githooks", "post-commit"), mine, 0o755)
		commitAll(t, g, repo, "hooks")
		res, err := g.InstallClone(repo, hooksDir, "jig")
		if err != nil || res.State != CloneSkipped || !strings.Contains(res.Reason, "work tree") {
			t.Fatalf("install = %+v, %v, want a tracked hook left alone", res, err)
		}
		if got := readOrFail(t, filepath.Join(repo, ".githooks", "post-commit")); got != mine {
			t.Errorf("the tracked hook was edited: %q", got)
		}
	})

	t.Run("inside the work tree and ignored", func(t *testing.T) {
		t.Parallel()
		g := testGit(t)
		repo := initRepo(t, g)
		if _, err := gitx.RunEnv(repo, g.Env, "config", "core.hooksPath", ".githooks"); err != nil {
			t.Fatal(err)
		}
		writeOrFail(t, filepath.Join(repo, ".git", "info", "exclude"), "/.githooks/\n", 0o644)
		writeOrFail(t, filepath.Join(repo, ".githooks", "post-commit"), "#!/bin/sh\necho generated\n", 0o755)
		res, err := g.InstallClone(repo, hooksDir, "jig")
		if err != nil || res.State != CloneHasBlock {
			t.Fatalf("install = %+v, %v, want the block into an ignored hook", res, err)
		}
	})
}

// sameFileOnDisk reports whether a and b are one file. git reports a hook's
// path with its symlinks resolved (on macOS /var is /private/var), so the
// paths need not match as text.
func sameFileOnDisk(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}
