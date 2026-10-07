// Package home resolves jig's per-machine root. Root reads it once, from the
// JIG_HOME environment variable or the real home directory; every
// home-anchored path (the machine mapping, the worktree pool, gate demo
// media) is derived from a root its caller passes in, so the binary resolves
// the root once and a test hands each package its own root instead of
// touching the environment or the real home directory.
package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Root returns the jig home directory: $JIG_HOME if set, else <user home>/.config/jig.
// It reads the process's environment; a caller that holds an environment of its
// own resolves the root from that with RootFrom.
func Root() (string, error) {
	return RootFrom(os.Getenv)
}

// RootFrom is Root against the environment getenv reads, in place of the
// process's: $JIG_HOME if getenv gives one, else <user home>/.config/jig with
// the user home UserDirFrom finds. The binary passes os.Getenv; a command that
// runs in a process beside others (a test) passes the environment it was
// given, so what it resolves never depends on a variable another sets.
func RootFrom(getenv func(string) string) (string, error) {
	if v := getenv("JIG_HOME"); v != "" {
		return v, nil
	}
	h, err := UserDirFrom(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".config", "jig"), nil
}

// UserDirFrom returns the user's own home directory from the environment
// getenv reads, by the rule os.UserHomeDir applies to the process's: $HOME
// (%USERPROFILE% on Windows, $home on Plan 9), else the fixed directory Android
// and iOS use. It fails when the variable is empty elsewhere.
func UserDirFrom(getenv func(string) string) (string, error) {
	env, enverr := "HOME", "$HOME"
	switch runtime.GOOS {
	case "windows":
		env, enverr = "USERPROFILE", "%userprofile%"
	case "plan9":
		env, enverr = "home", "$home"
	}
	if v := getenv(env); v != "" {
		return v, nil
	}
	switch runtime.GOOS {
	case "android":
		return "/sdcard", nil
	case "ios":
		return "/", nil
	}
	return "", errors.New(enverr + " is not defined")
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

// IntentScratchDir returns the directory under the jig home root whose
// subdirectories are the working directories of gate intent-inference
// summarizer dispatches: one fresh, empty directory per dispatch, made and
// removed by the caller. It lives under the jig home, never the lease (the
// code under review) and never the store.
func IntentScratchDir(root string) string {
	return filepath.Join(root, "intent-scratch")
}

// EvidenceDir returns where one reviewed head's demo media live under the
// jig home root: <root>/evidence/<storeID>/<ticket>/<sha>. It is a fifth
// home beside the four ARCHITECTURE.md names, kept out of the store's git
// on purpose (media are large and the store is a long-lived repo) and out
// of any lease (a lease is rewound between rounds), so it lives with the
// machine, like the pool. storeID, ticket and sha must each name a single
// directory, so no caller-supplied spelling can climb out of the evidence
// tree.
func EvidenceDir(root, storeID, ticket, sha string) (string, error) {
	for _, part := range []struct{ what, name string }{
		{"store id", storeID},
		{"ticket", ticket},
		{"sha", sha},
	} {
		if !singleDirName(part.name) {
			return "", fmt.Errorf("home: evidence %s %q must name a single plain directory", part.what, part.name)
		}
	}
	return filepath.Join(root, "evidence", storeID, ticket, sha), nil
}

// singleDirName reports whether name is one plain, visible directory name:
// no separator or drive colon, not a dot name, and no trailing dot or space
// (Windows drops those, so two spellings would name one directory).
// filepath.IsLocal also refuses an empty name and a Windows reserved device
// name.
func singleDirName(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	return !strings.ContainsAny(name, `/\:`) && filepath.IsLocal(name)
}
