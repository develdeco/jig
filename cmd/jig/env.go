package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// env is what one run of the CLI takes from the process around it, beyond its
// arguments and streams, and the seams a test replaces: the environment
// variables cmd/jig itself reads (JIG_HOME, and the operator's home directory),
// the working directory it resolves the store, the repo and every relative path
// flag against, and three seams (the terminal check, the solve gate source,
// the flag-set hook). run hands it to every command, and cmd/jig reads those only
// through it, never through os.Getenv, os.Getwd, os.UserHomeDir or a
// package-level variable. The binary runs on processEnv; a test builds an env
// of its own, so two runs in one process share none of them and their tests
// can run in parallel.
//
// env does not reach what a run starts or calls into: the child processes it
// spawns (git, the session CLI, the oracles) inherit the process's environment
// and working directory, and internal/session reads JIG_HEADLESS_TIMEOUT and
// JIG_WSL_DISTRO from the process itself. A test must not use env to set
// those.
type env struct {
	// getenv looks a variable up as os.Getenv does.
	getenv func(key string) string
	// getwd returns the directory a command resolves its repo, its store and
	// every relative path flag against, as os.Getwd does.
	getwd func() (string, error)
	// stdinIsTerminal reports whether stdin is an interactive terminal, which
	// decides whether the triage prompts run or the defaults apply.
	stdinIsTerminal func(r io.Reader) bool
	// solveGateSource picks the GateSource `jig solve` runs its own gate
	// rounds on.
	solveGateSource func(scenario string, backend session.Backend) verifydeliver.GateSource
	// newFlagSetHook, when set, sees every FlagSet newFlagSet builds; a test
	// uses it to compare real flag registration with commandTable.
	newFlagSetHook func(name string, fs *flag.FlagSet)
}

// processEnv is the env of the binary: the process's own environment and
// working directory, and every seam at its production default.
func processEnv() env {
	return env{
		getenv:          os.Getenv,
		getwd:           os.Getwd,
		stdinIsTerminal: stdinIsTerminal,
		solveGateSource: gateSourceForSolve,
	}
}

// jigHome resolves the jig home root from e's environment: $JIG_HOME, else
// <user home>/.config/jig, made absolute against e's working directory if it
// is relative.
func (e env) jigHome() (string, error) {
	root, err := home.RootFrom(e.getenv)
	if err != nil {
		return "", err
	}
	return e.abs(root)
}

// userHomeDir resolves the operator's own home directory from e's
// environment, by os.UserHomeDir's rule.
func (e env) userHomeDir() (string, error) {
	return home.UserDirFrom(e.getenv)
}

// abs makes path absolute against e's working directory, where filepath.Abs
// would use the process's: every relative path a command is given (--store,
// --scenario, --doc, --dest, --clone) goes through it before it reaches the
// filesystem or an internal package, so a test's working directory is per
// call. An empty path stays empty.
func (e env) abs(path string) (string, error) {
	if !needsDir(path) {
		return path, nil
	}
	wd, err := e.getwd()
	if err != nil {
		return "", err
	}
	return absFrom(wd, path), nil
}

// absFrom is path joined onto dir when path is relative, else path as it is.
func absFrom(dir, path string) string {
	if !needsDir(path) {
		return path
	}
	return filepath.Join(dir, path)
}

// needsDir reports whether path is relative to a working directory: not empty,
// not absolute, and (on Windows) not rooted on the current drive or relative to
// a drive's own directory, which only the OS can place.
func needsDir(path string) bool {
	return path != "" && !filepath.IsAbs(path) && filepath.VolumeName(path) == "" && !os.IsPathSeparator(path[0])
}
