package gitx

import (
	"os"
	"strings"
	"testing"
)

// CommitAll is Repo.CommitAllAs for a caller that names its author and
// committer: name and email, unless GIT_AUTHOR_* or GIT_COMMITTER_* set them.
func (r *Repo) CommitAll(msg, name, email string) (bool, error) {
	return r.CommitAllAs(msg, func() (Identity, error) {
		return Identity{Name: name, Email: email}, nil
	})
}

// CommitAllEnv is the commit CommitAll makes with its author, committer and
// both dates read from env (KEY=VALUE entries, the last of a key winning, as
// RunEnv takes them) before the process environment, so a test pins them
// without editing the process's own. It returns ErrUseCLI when neither env nor
// the process environment names the author and committer, which the git
// program would read from configuration.
func (r *Repo) CommitAllEnv(msg string, env []string) (bool, error) {
	lookup := envEntries(env)
	return r.CommitAllWith(msg, func() (Identity, error) {
		for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
			if v, _ := lookup(k); v == "" {
				return Identity{}, ErrUseCLI
			}
		}
		return Identity{}, nil
	}, lookup)
}

// envEntries answers lookups from KEY=VALUE entries, then the process
// environment.
func envEntries(env []string) envLookup {
	return func(key string) (string, bool) {
		for i := len(env) - 1; i >= 0; i-- {
			if k, v, ok := strings.Cut(env[i], "="); ok && k == key {
				return v, true
			}
		}
		return os.LookupEnv(key)
	}
}

// RunReplaceEnv is Run with env as the call's whole process environment,
// instead of appended to this process's own inherited one.
func RunReplaceEnv(dir string, env []string, args ...string) (string, error) {
	return RunWith(dir, env, true, args...)
}

// rewritePinned is the identity and date every commit of the tests that build a
// repository with the program is made with, so two repositories given the same
// files and message make the same commit.
var rewritePinned = []string{
	"GIT_AUTHOR_NAME=pinned", "GIT_AUTHOR_EMAIL=pinned@example.invalid", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_NAME=pinned", "GIT_COMMITTER_EMAIL=pinned@example.invalid", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

// rewriteEnv is the hermetic environment the program runs under besides
// rewritePinned: no host config, so a developer's own signing or line-ending
// settings cannot reach a commit.
var rewriteEnv = []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}

// pinnedGit runs the program in dir with the pinned identity and returns its
// output.
func pinnedGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := RunEnv(dir, append(append([]string{}, rewriteEnv...), rewritePinned...), args...)
	if err != nil {
		t.Fatalf("%s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(out)
}

// newRewriteRepo makes a repository in a fresh directory with one commit of
// files (name to body) on main, and returns its directory.
func newRewriteRepo(t *testing.T, files map[string]string, msg string) string {
	t.Helper()
	dir := t.TempDir()
	pinnedGit(t, dir, "init", "-q", "-b", "main")
	pinnedGit(t, dir, "config", "core.autocrlf", "false")
	for name, body := range files {
		writeFileT(t, dir, name, body)
	}
	pinnedGit(t, dir, "add", "-A")
	pinnedGit(t, dir, "commit", "-q", "-m", msg)
	return dir
}

// fsck fails the test when the repository at dir is not consistent.
func fsck(t *testing.T, dir string) {
	t.Helper()
	if _, err := RunEnv(dir, rewriteEnv, "fsck", "--strict", "--no-dangling"); err != nil {
		t.Fatalf("fsck %s: %v", dir, err)
	}
}
