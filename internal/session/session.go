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
	Screen        bool
}

// Backend runs one dispatch. A returned error means infrastructure failure
// (the backend itself could not run); the caller reads ResultJSON
// afterward regardless, and a missing file there is the caller's job to
// turn into a failed outcome.
type Backend interface {
	Run(d Dispatch) error
}

// Options configures backend construction. ScenarioDir is used by "fake"
// only.
type Options struct {
	ScenarioDir string // fake
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
