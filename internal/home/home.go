// Package home resolves jig's per-machine root. Root reads it once, from the
// JIG_HOME environment variable or the real home directory; every
// home-anchored path (the machine mapping, the worktree pool) is derived
// from a root its caller passes in, so the binary resolves the root once and
// a test hands each package its own root instead of touching the
// environment or the real home directory.
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

// PoolDir returns the worktree pool root under the jig home root.
func PoolDir(root string) string {
	return filepath.Join(root, "pool")
}

// MachinePath returns the per-machine project mapping file path under the
// jig home root.
func MachinePath(root string) string {
	return filepath.Join(root, "projects.yaml")
}

// IntentExcerptDir returns the directory for gate intent-inference
// excerpts under the jig home root: text jig extracted from a local agent
// transcript to summarize. It lives under the jig home, never the store,
// because a transcript can hold secrets a store commit must never carry.
func IntentExcerptDir(root string) string {
	return filepath.Join(root, "intent-excerpts")
}
