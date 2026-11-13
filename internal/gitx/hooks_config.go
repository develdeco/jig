package gitx

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/fileshare"
	format "github.com/go-git/go-git/v5/plumbing/format/config"
)

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
