package project

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/store"
)

// ignoredFile is the wire shape of the ignored-directories list under the jig
// home root.
type ignoredFile struct {
	Paths []string `yaml:"paths"`
}

// LoadIgnored reads the directories this machine does not track (`jig
// ignore`), absolute and cleaned, in the order they were added. An absent
// list reads as none.
func LoadIgnored(jigHome string) ([]string, error) {
	if jigHome == "" {
		return nil, fmt.Errorf("project: no jig home given")
	}
	data, err := os.ReadFile(home.IgnoredPath(jigHome))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("project: read ignored list: %w", err)
	}
	var f ignoredFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("project: parse ignored list: %w", err)
	}
	return f.Paths, nil
}

// Ignore adds dir to the directories this machine does not track, and
// reports whether it was added (false: it, or a directory above it, was
// already there). dir is made absolute first. The write is atomic and under
// the list's lock, so two jig processes that both ignore a directory both
// land.
func Ignore(jigHome, dir string) (added bool, err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false, fmt.Errorf("project: resolve %s: %w", dir, err)
	}
	err = updateIgnored(jigHome, func(paths []string) ([]string, error) {
		for _, p := range paths {
			if pathContains(p, abs) {
				return paths, nil
			}
		}
		added = true
		return append(paths, abs), nil
	})
	return added, err
}

// Unignore removes dir from the directories this machine does not track, and
// reports whether it was there. Only the entry for dir itself goes: a
// directory ignored because a parent of it is, stays ignored.
func Unignore(jigHome, dir string) (removed bool, err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false, fmt.Errorf("project: resolve %s: %w", dir, err)
	}
	err = updateIgnored(jigHome, func(paths []string) ([]string, error) {
		kept := paths[:0:0]
		for _, p := range paths {
			if pathContains(p, abs) && pathContains(abs, p) {
				removed = true
				continue
			}
			kept = append(kept, p)
		}
		return kept, nil
	})
	return removed, err
}

// updateIgnored is a locked read-modify-write of the ignored list; nothing is
// written when fn leaves the list as it was.
func updateIgnored(jigHome string, fn func([]string) ([]string, error)) error {
	if jigHome == "" {
		return fmt.Errorf("project: no jig home given")
	}
	path := home.IgnoredPath(jigHome)
	release, _, err := store.Lock(path, machineLockTimeout)
	if err != nil {
		return fmt.Errorf("project: lock ignored list: %w", err)
	}
	defer release()
	cur, err := LoadIgnored(jigHome)
	if err != nil {
		return err
	}
	next, err := fn(append([]string(nil), cur...))
	if err != nil {
		return err
	}
	if len(next) == len(cur) && (len(cur) == 0 || equalStrings(next, cur)) {
		return nil
	}
	data, err := yaml.Marshal(ignoredFile{Paths: next})
	if err != nil {
		return fmt.Errorf("project: marshal ignored list: %w", err)
	}
	if err := store.AtomicWrite(path, data); err != nil {
		return fmt.Errorf("project: write ignored list: %w", err)
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// IsIgnored reports whether dir is one of the ignored directories or inside
// one, by path and then by the paths with their symlinks resolved, so a
// directory reached through a link (on macOS /var is /private/var) is
// ignored all the same.
func IsIgnored(ignored []string, dir string) (string, bool) {
	for _, p := range ignored {
		if pathContains(p, dir) {
			return p, true
		}
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	for _, p := range ignored {
		if realP, err := filepath.EvalSymlinks(p); err == nil && pathContains(realP, realDir) {
			return p, true
		}
	}
	return "", false
}
