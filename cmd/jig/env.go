package main

import (
	"context"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/spawn"
	"github.com/develdeco/jig/internal/store"
)

// env is what one run of the CLI takes from the process around it, beyond its
// arguments and streams, and the seams a test replaces: the environment
// variables cmd/jig itself reads (JIG_HOME, and the operator's home directory),
// the working directory it resolves the store, the repo and every relative path
// flag against, and the seams (the terminal checks,
// the flag-set hook, the mirror's GitHub client, the chart write, and the
// context, clock, process spawner, executable and PATH lookups below). run
// hands it to every command, and cmd/jig reads those only through it, never
// through os.Getenv, os.Getwd, os.UserHomeDir, time.Now or a package-level
// variable. The binary runs on processEnv; a test builds an env of its own,
// so two runs in one process share none of them and their tests can run in
// parallel.
//
// env does not reach what a run starts or calls into: the child processes it
// spawns (git, the oracles, the verification workers, and the `gh auth token`
// the mirror runs, which reads GH_TOKEN and GITHUB_TOKEN itself) inherit the
// process's environment and working directory. A test must not use env to
// set those.
type env struct {
	// getenv looks a variable up as os.Getenv does.
	getenv func(key string) string
	// getwd returns the directory a command resolves its repo, its store and
	// every relative path flag against, as os.Getwd does.
	getwd func() (string, error)
	// stdinIsTerminal reports whether stdin is an interactive terminal, which
	// decides whether the triage prompts run or the defaults apply.
	stdinIsTerminal func(r io.Reader) bool
	// newFlagSetHook, when set, sees every FlagSet newFlagSet builds; a test
	// uses it to compare real flag registration with the registered verbs'
	// help rows.
	newFlagSetHook func(name string, fs *flag.FlagSet)
	// mirrorClient, when set, is the GitHub client every checkpoint's sync
	// and `jig trackers sync` use, the same seam mirror.Deps.Client gives
	// internal/mirror's own tests; nil, as in production, builds the client
	// ghAuthToken and api.github.com give it. A test sets it to keep a
	// checkpoint sync hermetic: pointed at an httptest server or a stub
	// rather than the developer's own gh token and github.com.
	mirrorClient github.Client
	// writeChart, when set, replaces the st.WriteChart call `jig graduate`
	// makes after minting a ticket, so a test can force one particular call
	// to fail the same way on every OS, instead of racing an OS-specific
	// read-only chmod against store.AtomicWrite's own os.Rename, whose
	// failure mode differs by OS. nil, as in production, writes through st.
	writeChart func(st *store.Store, chart string, entries []store.ChartEntry) error
	// ctx is cancelled when the run should wind down: the first interrupt of
	// the binary (main wires signal.NotifyContext). A command that waits, or
	// streams until stopped, watches it; a test cancels its own to stop one.
	ctx context.Context
	// now is the clock: time.Now in production.
	now func() time.Time
	// spawn starts a detached process that outlives this one and returns its
	// pid: spawn.Detached in production. A test env refuses.
	spawn func(spawn.Spec) (int, error)
	// executable is the path of the running jig binary, as os.Executable
	// gives it, for a command that starts itself as a detached worker.
	executable func() (string, error)
	// lookPath finds a program on PATH as exec.LookPath does.
	lookPath func(file string) (string, error)
	// stdoutIsTerminal reports whether stdout is an interactive terminal,
	// which decides whether a command draws a screen or writes plain lines.
	stdoutIsTerminal func(w io.Writer) bool
	// stderr is where a command writes a warning that is not its output:
	// os.Stderr in production. The screens (`jig tui`) own the terminal, so
	// a command they run writes it into the output they show instead.
	stderr io.Writer
}

// processEnv is the env of the binary: the process's own environment and
// working directory, and every seam at its production default.
func processEnv() env {
	return env{
		getenv:          os.Getenv,
		getwd:           os.Getwd,
		stdinIsTerminal: stdinIsTerminal,

		ctx:              context.Background(),
		now:              time.Now,
		spawn:            spawn.Detached,
		executable:       os.Executable,
		lookPath:         exec.LookPath,
		stdoutIsTerminal: stdoutIsTerminal,
		stderr:           os.Stderr,
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
