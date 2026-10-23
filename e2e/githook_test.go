package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
)

// hookRepo is a clone-shaped repository a githook test commits in, with the
// environment git and jig run in.
type hookRepo struct {
	t      *testing.T
	env    jigEnv
	work   string // scratch directory beside the clone: markers, a remote
	clone  string
	gitEnv []string
}

// newHookRepo makes a repository with one commit in it, under a jig home and a
// git global config of the test's own (withGitGlobal), so what `jig githook
// --global` writes reaches nothing shared.
func newHookRepo(t *testing.T) *hookRepo {
	t.Helper()
	env := jigEnv{home: t.TempDir(), userHome: t.TempDir()}.withGitGlobal(t)
	work := t.TempDir()
	r := &hookRepo{
		t:      t,
		env:    env,
		work:   work,
		clone:  filepath.Join(work, "clone"),
		gitEnv: replaceEnv(env.environ(), pinnedIdentityEnv),
	}
	if err := os.MkdirAll(r.clone, 0o755); err != nil {
		t.Fatal(err)
	}
	r.git("init", "-q", "-b", "main")
	r.commit("first") // before any hook exists
	return r
}

func (r *hookRepo) git(args ...string) string {
	r.t.Helper()
	out, err := gitx.RunReplaceEnv(r.clone, r.gitEnv, args...)
	if err != nil {
		r.t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// tryCommit makes an empty commit and returns git's error, if any.
func (r *hookRepo) tryCommit(msg string) error {
	_, err := gitx.RunReplaceEnv(r.clone, r.gitEnv, "commit", "-q", "--allow-empty", "-m", msg)
	return err
}

func (r *hookRepo) commit(msg string) string {
	r.t.Helper()
	if err := r.tryCommit(msg); err != nil {
		r.t.Fatalf("git commit: %v", err)
	}
	return r.git("rev-parse", "HEAD")
}

// path is a file in the scratch directory, in the forward-slash spelling a
// POSIX sh script reads on every platform.
func (r *hookRepo) path(name string) string {
	return filepath.ToSlash(filepath.Join(r.work, name))
}

// hook writes an executable hook script into dir.
func (r *hookRepo) hook(dir, name, body string) {
	r.t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		r.t.Fatal(err)
	}
}

func (r *hookRepo) ownHooks() string { return filepath.Join(r.clone, ".git", "hooks") }

// lines is the lines of a scratch file, none if it is not there.
func (r *hookRepo) lines(name string) []string {
	r.t.Helper()
	data, err := os.ReadFile(filepath.Join(r.work, name))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.Fields(strings.TrimSpace(string(data)))
}

func (r *hookRepo) jig(args ...string) jigResult {
	r.t.Helper()
	return runJig(r.t, r.env, r.clone, args...)
}

func (r *hookRepo) globalHooksPath() string {
	out, err := gitx.RunReplaceEnv(r.work, r.gitEnv, "config", "--global", "--get", "core.hooksPath")
	if err != nil {
		return ""
	}
	return out
}

// gitCommits is the ledger lines that record a commit.
func (r *hookRepo) gitCommits() []journal.Line {
	var out []journal.Line
	for _, l := range readLedger(r.t, r.env) {
		if l.Event == journal.EventGitCommit {
			out = append(out, l)
		}
	}
	return out
}

// TestGitHookChainsCloneHooks pins what the global git hooks promise a clone:
// the hooks it keeps in its own .git/hooks keep running, and refusing, once
// core.hooksPath points at jig's wrappers, and a plain git commit leaves a
// git-commit line in the ledger beside them.
func TestGitHookChainsCloneHooks(t *testing.T) {
	t.Parallel()
	r := newHookRepo(t)
	marker, block := r.path("marker"), r.path("BLOCK")
	r.hook(r.ownHooks(), "post-commit", "echo ran >> \""+marker+"\"\n")
	r.hook(r.ownHooks(), "pre-commit", "if [ -e \""+block+"\" ]; then echo 'own pre-commit: BLOCK exists' >&2; exit 1; fi\n")

	// Before jig: the clone's own hooks run, and the ledger knows nothing.
	r.commit("before")
	if got := r.lines("marker"); len(got) != 1 {
		t.Fatalf("the clone own post-commit ran %d times before the install, want 1", len(got))
	}
	if got := r.gitCommits(); len(got) != 0 {
		t.Fatalf("the ledger has git-commit lines before the install: %+v", got)
	}
	if err := os.WriteFile(filepath.Join(r.work, "BLOCK"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.tryCommit("refused before"); err == nil || !strings.Contains(err.Error(), "BLOCK exists") {
		t.Fatalf("the clone own pre-commit did not refuse before the install: %v", err)
	}
	if err := os.Remove(filepath.Join(r.work, "BLOCK")); err != nil {
		t.Fatal(err)
	}

	// Install.
	res := r.jig("githook", "--global")
	if res.Code != 0 || !strings.Contains(res.Stdout, "result: installed") {
		t.Fatalf("jig githook --global = exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	hooksDir := home.HooksDir(r.env.home)
	if got := r.globalHooksPath(); got != filepath.ToSlash(hooksDir) {
		t.Fatalf("global core.hooksPath = %q, want %q", got, filepath.ToSlash(hooksDir))
	}

	// After: the same hooks, and the ledger line.
	sha := r.commit("after")
	if got := r.lines("marker"); len(got) != 2 {
		t.Errorf("the clone own post-commit ran %d times in all, want 2: it was not chained", len(got))
	}
	commits := r.gitCommits()
	if len(commits) != 1 {
		t.Fatalf("the ledger has %d git-commit lines after one commit, want 1: %+v", len(commits), commits)
	}
	if c := commits[0]; c.Commit != sha || c.Branch != "main" || !gitx.SameDir(c.Cwd, r.clone) {
		t.Errorf("git-commit line = %+v, want commit %s on main in %s", c, sha, r.clone)
	}

	// The refusal still refuses, and a refused commit is not recorded.
	if err := os.WriteFile(filepath.Join(r.work, "BLOCK"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.tryCommit("refused after"); err == nil || !strings.Contains(err.Error(), "BLOCK exists") {
		t.Errorf("the clone own pre-commit stopped refusing under the wrappers: %v", err)
	}
	if got := r.git("rev-parse", "HEAD"); got != sha {
		t.Errorf("a refused commit moved HEAD to %s", got)
	}
	if got := len(r.gitCommits()); got != 1 {
		t.Errorf("a refused commit left %d git-commit lines, want 1", got)
	}
	if got := r.lines("marker"); len(got) != 2 {
		t.Errorf("the clone own post-commit ran for a refused commit")
	}
}

// TestGitHookChainsThePreviousHooksPath pins the rest of the wrapper's chain:
// the core.hooksPath that was set before jig's still runs, a hook git feeds on
// stdin hands it to the repository's hook and the previous one alike, and an
// uninstall gives the setting back.
func TestGitHookChainsThePreviousHooksPath(t *testing.T) {
	t.Parallel()
	r := newHookRepo(t)
	previous := filepath.Join(r.work, "previous-hooks")
	r.hook(previous, "pre-commit", "echo prev >> \""+r.path("prev-pre-commit")+"\"\n")
	r.hook(previous, "pre-push", "cat > \""+r.path("prev-push-stdin")+"\"\n")
	r.hook(r.ownHooks(), "pre-push", "cat > \""+r.path("own-push-stdin")+"\"\n")
	if _, err := gitx.RunReplaceEnv(r.work, r.gitEnv, "config", "--global", "core.hooksPath", filepath.ToSlash(previous)); err != nil {
		t.Fatal(err)
	}
	origin := filepath.Join(r.work, "origin.git")
	if _, err := gitx.RunReplaceEnv(r.work, r.gitEnv, "init", "-q", "--bare", origin); err != nil {
		t.Fatal(err)
	}
	r.git("remote", "add", "origin", filepath.ToSlash(origin))

	if res := r.jig("githook", "--global"); res.Code != 0 || !strings.Contains(res.Stdout, "result: installed") {
		t.Fatalf("jig githook --global = exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	r.commit("one")
	if got := r.lines("prev-pre-commit"); len(got) != 1 {
		t.Errorf("the previous core.hooksPath pre-commit ran %d times, want 1", len(got))
	}

	r.git("push", "-q", "origin", "HEAD:refs/heads/main")
	own, prev := r.lines("own-push-stdin"), r.lines("prev-push-stdin")
	if len(own) == 0 || !strings.Contains(strings.Join(own, " "), "refs/heads/main") {
		t.Errorf("the clone pre-push read %v from stdin, want the pushed ref", own)
	}
	if strings.Join(own, " ") != strings.Join(prev, " ") {
		t.Errorf("the previous pre-push read %v from stdin, the clone one %v, want the same", prev, own)
	}

	res := r.jig("githook", "--global", "--uninstall")
	if res.Code != 0 || !strings.Contains(res.Stdout, "result: removed") {
		t.Fatalf("jig githook --global --uninstall = exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	if got := r.globalHooksPath(); got != filepath.ToSlash(previous) {
		t.Errorf("core.hooksPath after the uninstall = %q, want the previous %q back", got, filepath.ToSlash(previous))
	}
	if _, err := os.Stat(home.HooksDir(r.env.home)); !os.IsNotExist(err) {
		t.Errorf("the wrappers directory is still there after the uninstall: %v", err)
	}
	before := len(r.gitCommits())
	r.commit("two")
	if got := r.lines("prev-pre-commit"); len(got) != 2 {
		t.Errorf("the previous pre-commit ran %d times in all, want 2 (the setting is theirs again)", len(got))
	}
	if got := len(r.gitCommits()); got != before {
		t.Errorf("a commit after the uninstall was recorded")
	}
}

// TestGitHookBlockInAClonesOwnHooks pins the per-clone form: without the
// global setting, jig githook --here has the clone record its commits from its
// own post-commit, and installing the global wrappers over it afterwards does
// not record a commit twice.
func TestGitHookBlockInAClonesOwnHooks(t *testing.T) {
	t.Parallel()
	r := newHookRepo(t)
	r.hook(r.ownHooks(), "post-commit", "echo ran >> \""+r.path("marker")+"\"\n")

	res := r.jig("githook", "--here")
	if res.Code != 0 || !strings.Contains(res.Stdout, "result: installed") {
		t.Fatalf("jig githook --here = exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	if got := r.globalHooksPath(); got != "" {
		t.Errorf("--here set the global core.hooksPath to %q", got)
	}
	sha := r.commit("one")
	if got := r.lines("marker"); len(got) != 1 {
		t.Errorf("the clone own post-commit ran %d times, want 1", len(got))
	}
	if commits := r.gitCommits(); len(commits) != 1 || commits[0].Commit != sha {
		t.Errorf("git-commit lines = %+v, want one for %s", commits, sha)
	}

	// The wrappers over a clone that already records: still one line a commit.
	if res := r.jig("githook", "--global"); res.Code != 0 {
		t.Fatalf("jig githook --global = exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	sha = r.commit("two")
	if got := r.lines("marker"); len(got) != 2 {
		t.Errorf("the clone own post-commit ran %d times in all, want 2", len(got))
	}
	if commits := r.gitCommits(); len(commits) != 2 || commits[1].Commit != sha {
		t.Errorf("git-commit lines = %+v, want exactly one more, for %s", commits, sha)
	}

	// The uninstall takes the block out and leaves the person's hook.
	if res := r.jig("githook", "--here", "--uninstall"); res.Code != 0 || !strings.Contains(res.Stdout, "result: removed") {
		t.Fatalf("jig githook --here --uninstall = exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	}
	data, err := os.ReadFile(filepath.Join(r.ownHooks(), "post-commit"))
	if err != nil || string(data) != "#!/bin/sh\necho ran >> \""+r.path("marker")+"\"\n" {
		t.Errorf("post-commit after the uninstall = %q, %v, want the person's own", data, err)
	}
}
