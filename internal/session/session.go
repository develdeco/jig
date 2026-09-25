// Package session dispatches one slice attempt to a coding-agent backend
// and lets the caller read the result back off disk. Three backends
// implement the same narrow interface: fake (a deterministic scenario
// player used in CI and tests), headless (a local `claude -p` subprocess),
// and herdr (a remote agent driven through herdr, natively off Windows and
// via WSL on Windows).
package session

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/develdeco/jig/internal/axi"
)

// Dispatch describes one slice attempt for a backend to run. Prompt carries
// paths, not file contents; the backend (or the agent it drives) reads
// SliceJSON itself.
type Dispatch struct {
	Ticket, Slice string
	Attempt       int    // 1-based
	Worktree      string // lease dir
	SliceJSON     string // path jig writes before Run
	ResultJSON    string // path expected after Run
	Model         string
	Prompt        string // rendered dispatch prompt (paths, not contents)

	// Screen attaches the command/secret screens as the session's
	// PreToolUse hook, where the backend has one (headless only). Every
	// dispatch jig makes is screened; an unscreened headless session gets
	// its shell and read tools through plain allow rules instead.
	Screen bool
}

// Backend runs one dispatch. A returned error means infrastructure failure
// (the backend itself could not run); the caller reads ResultJSON
// afterward regardless, and a missing file there is the caller's job to
// turn into a failed outcome.
type Backend interface {
	Run(d Dispatch) error
}

// Options configures backend construction. ScenarioDir is used by "fake"
// only, ScreenBinary and Env by "headless" only.
type Options struct {
	ScenarioDir string // fake

	// ScreenBinary is the jig binary whose `_screen` verb a screened
	// headless dispatch's PreToolUse hook runs. Empty means this process's
	// own executable, which a screened dispatch refuses unless this process
	// is the jig binary itself: a test binary or another program running
	// screened dispatches must pass a built jig.
	ScreenBinary string // headless

	// Env, when non-nil, is the exact environment a headless dispatch's
	// `claude` child process gets: this list, with any PWD or OLDPWD entry
	// dropped and PWD then set to the dispatch's own worktree (cmd.Dir) -
	// never a mix with this process's own environment. Nil, the default,
	// means the child inherits this process's full environment unchanged. A caller that dispatches against a corpus or
	// other content it does not fully trust - internal/revieweval's live
	// path above all - should build this from a filtered copy of its own
	// environment, never pass its own os.Environ() through untouched: a nil
	// Env hands a live child everything this process happens to be running
	// with, including anything naming what is being measured.
	Env []string // headless
}

// New constructs a Backend by name: "fake", "headless", or "herdr".
func New(name string, opts Options) (Backend, error) {
	switch name {
	case "fake":
		return newFakeBackend(opts), nil
	case "headless":
		return newHeadlessBackend(opts), nil
	case "herdr":
		return newHerdrBackend(opts), nil
	default:
		return nil, fmt.Errorf("session: unknown backend %q", name)
	}
}

// Available reports whether the program the named backend runs is on PATH,
// so a command can stop before it spends attempts on a missing tool. The
// fake backend needs nothing.
func Available(name string) error {
	return available(runtime.GOOS, name)
}

func available(goos, name string) error {
	var prog, install string
	switch name {
	case "headless":
		prog, install = "claude", "Install the Claude Code CLI so `claude` is on PATH"
	case "herdr":
		if goos == "windows" {
			prog, install = "wsl", "Install WSL with herdr in its default distro (JIG_WSL_DISTRO picks another)"
		} else {
			prog, install = "herdr", "Install herdr so it is on PATH"
		}
	default:
		return nil
	}
	if _, err := exec.LookPath(prog); err == nil {
		return nil
	}
	other := "headless"
	if name == "headless" {
		other = "herdr"
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the %s backend needs %s, which is not on PATH", name, prog),
		Code: "BACKEND_UNAVAILABLE",
		Help: []string{install, fmt.Sprintf("Or run with `--backend %s`", other)},
	}
}
