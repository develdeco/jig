// Package gitxtest is the part of gitx only tests use: rewriting the history of
// a repository a test built, committing in process with a pinned environment,
// running the git program with an environment of exactly the caller's, asking
// whether git would run a hook, and reading how many questions gitx answered in
// process. It lives under internal/gitx so the lint that only gitx runs git and
// only gitx links go-git reads it as gitx; the jig binary links none of it, and
// only tests and internal/fixture import it.
package gitxtest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	format "github.com/go-git/go-git/v5/plumbing/format/config"

	"github.com/develdeco/jig/internal/fileshare"
	"github.com/develdeco/jig/internal/gitx"
)

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// RunReplaceEnv is gitx.Run with env as the call's whole process environment,
// instead of appended to this process's own inherited one: a caller resolving
// some other directory's identity, or lack of one, uses it so an identity this
// process has pinned for its own commits (gittest.PinIdentity, say) cannot
// shadow it.
func RunReplaceEnv(dir string, env []string, args ...string) (string, error) {
	return gitx.RunWith(dir, env, true, args...)
}

// InProcessReads is the number of in-process read answers gitx has given so
// far. Its consumers are the tests that bound what a session-start call costs.
func InProcessReads() int64 { return gitx.InProcessReadTally.Load() }

// envEntries answers lookups from KEY=VALUE entries (the last of a key
// winning), then the process environment.
func envEntries(env []string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		for i := len(env) - 1; i >= 0; i-- {
			if k, v, ok := strings.Cut(env[i], "="); ok && k == key {
				return v, true
			}
		}
		return os.LookupEnv(key)
	}
}

// CommitAllEnv is the commit Repo.CommitAllAs makes with its author, committer
// and both dates read from env (KEY=VALUE entries, the last of a key winning,
// as gitx.RunEnv takes them) before the process environment, so a test pins
// them without editing the process's own. It writes what the git program
// writes under the same environment, hash included, and returns gitx.ErrUseCLI
// for the reasons CommitAllAs does, and when neither env nor the process
// environment names the author and committer, which the git program would read
// from configuration.
func CommitAllEnv(r *gitx.Repo, msg string, env []string) (bool, error) {
	lookup := envEntries(env)
	return r.CommitAllWith(msg, func() (gitx.Identity, error) {
		for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
			if v, _ := lookup(k); v == "" {
				return gitx.Identity{}, gitx.ErrUseCLI
			}
		}
		return gitx.Identity{}, nil
	}, lookup)
}

// HooksConfigured reports whether the git program, run in dir with
// childEnv (its whole environment, KEY=VALUE entries, the last of a key
// winning) as the environment, could run a hook the repository's own hooks
// directory does not show: core.hooksPath set in the repository's config, in
// the global config the environment names (GIT_CONFIG_GLOBAL, else
// $XDG_CONFIG_HOME/git/config and ~/.gitconfig) or in the environment's own
// GIT_CONFIG_* variables. A config file that cannot be read or parsed, one
// that includes another, and a repository whose git directory is not
// dir/.git count as configuring hooks: the answer is for a caller deciding
// whether an in-process move is the same as the git program's, and when in
// doubt it is not. The system config is not read.
func HooksConfigured(dir string, childEnv []string) bool {
	get := func(key string) string {
		v, _ := envEntries(childEnv)(key)
		return v
	}
	if get("GIT_CONFIG_PARAMETERS") != "" && strings.Contains(strings.ToLower(get("GIT_CONFIG_PARAMETERS")), "hookspath") {
		return true
	}
	if n, err := strconv.Atoi(get("GIT_CONFIG_COUNT")); err == nil {
		for i := 0; i < n; i++ {
			if strings.EqualFold(get("GIT_CONFIG_KEY_"+strconv.Itoa(i)), "core.hookspath") {
				return true
			}
		}
	}
	files := []string{filepath.Join(dir, ".git", "config")}
	if g := get("GIT_CONFIG_GLOBAL"); g != "" {
		files = append(files, g)
	} else {
		home := get("HOME")
		if home == "" {
			home = get("USERPROFILE")
		}
		xdg := get("XDG_CONFIG_HOME")
		if xdg == "" && home != "" {
			xdg = filepath.Join(home, ".config")
		}
		if xdg != "" {
			files = append(files, filepath.Join(xdg, "git", "config"))
		}
		if home != "" {
			files = append(files, filepath.Join(home, ".gitconfig"))
		}
	}
	for _, f := range files {
		if configSetsHooks(f) {
			return true
		}
	}
	return false
}

// configSetsHooks reports whether the config file at path sets
// core.hooksPath, includes another file, or cannot be read; a missing file
// sets nothing.
func configSetsHooks(path string) bool {
	f, err := fileshare.Open(path)
	if err != nil {
		return !isAbsent(err)
	}
	defer f.Close()
	raw := format.New()
	if err := format.NewDecoder(f).Decode(raw); err != nil {
		return true
	}
	for _, s := range raw.Sections {
		if s.IsName("include") || s.IsName("includeIf") {
			return true
		}
	}
	return raw.Section("core").HasOption("hooksPath")
}

// isAbsent reports whether err says a file or directory is not there.
func isAbsent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}
