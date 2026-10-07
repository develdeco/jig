package main

import (
	"flag"
	"io"
	"os"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// env is everything one run of the CLI reads from the process around it,
// beyond its arguments and streams: the environment, the working directory,
// and the three seams a test replaces (the terminal check, the solve gate
// source, the flag-set hook). run hands it to every command, and a command
// reaches for the process only through it, never through os.Getenv, os.Getwd,
// os.UserHomeDir or a package-level variable. The binary runs on processEnv;
// a test builds an env of its own, so two runs in one process share nothing
// and their tests can run in parallel.
type env struct {
	// getenv looks a variable up as os.Getenv does.
	getenv func(key string) string
	// getwd returns the directory a command resolves its repo and store from,
	// as os.Getwd does.
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
// <user home>/.config/jig.
func (e env) jigHome() (string, error) {
	return home.RootFrom(e.getenv)
}

// userHomeDir resolves the operator's own home directory from e's
// environment, by os.UserHomeDir's rule.
func (e env) userHomeDir() (string, error) {
	return home.UserDirFrom(e.getenv)
}
