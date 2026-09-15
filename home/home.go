// Package home resolves jig's per-machine root. Every home-anchored path (the
// machine mapping, the worktree pool) goes through Root, overridable via the
// JIG_HOME environment variable so tests never touch the real home directory.
package home

import (
	"os"
	"path/filepath"
)

// Root returns the jig home directory: $JIG_HOME if set, else <user home>/.config/jig.
func Root() (string, error) {
	if v := os.Getenv("JIG_HOME"); v != "" {
		return v, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".config", "jig"), nil
}

// PoolDir returns the worktree pool root under the jig home.
func PoolDir() (string, error) {
	r, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "pool"), nil
}

// MachinePath returns the per-machine project mapping file path.
func MachinePath() (string, error) {
	r, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(r, "projects.yaml"), nil
}
