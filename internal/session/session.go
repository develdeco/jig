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
	Worktree      string // the session's working directory: the lease dir, or a scratch dir for an intent summarizer
	SliceJSON     string // path jig writes before Run
	ResultJSON    string // path expected after Run
	Model         string
	// Effort is the session's reasoning effort, one of Claude Code's
	// --effort levels, or "" to pass none. Only headless passes it.
	Effort string
	Prompt string // rendered dispatch prompt (paths, not contents)

	// ExtraWriteDir, when set, is one absolute directory outside the
	// worktree the session may also write files in: a gate demo's media
	// directory. Every other path outside the worktree stays closed. Where a
	// screen attaches (headless) it governs the shell either way, so this
	// widens only the backend's own edit tools (headless: one more
	// path-scoped rule); herdr sessions are not screened, and scope no edits
	// of their own, so the field changes nothing there.
	ExtraWriteDir string

	// Screen attaches the command/secret screens as the session's
	// PreToolUse hook, where the backend has one (headless only). Every
	// dispatch jig makes is screened; an unscreened headless session gets
	// its shell and read tools through plain allow rules instead.
	Screen bool

	// NoSessionPersistence asks the backend not to leave a transcript of
	// the session on disk where it would otherwise keep one. Only the
	// headless backend has anything to turn off: `claude -p` saves every
	// session under ~/.claude/projects, in a directory named after the
	// session's working directory, and this passes --no-session-persistence
	// to it. The fake backend runs no session, and herdr's agent is an
	// interactive claude session started without flags, so both accept the
	// field and ignore it. A caller sets it for a dispatch whose input must
	// not be copied into the operator's own Claude Code data (the intent
	// summarizer's excerpt); every other dispatch leaves it unset.
	NoSessionPersistence bool

	// resume is the session id a Resumer continues instead of starting a new
	// session; only Resume sets it.
	resume string
}

// Resumer is a Backend that can continue a session it ran: RunResumable is
// Run that also returns the session's id ("" when the backend could not read
// one), and Resume hands that session d.Prompt as its next turn, with d's
// worktree, grants and result path. Only headless implements it; a caller
// that holds another backend starts a fresh dispatch instead.
type Resumer interface {
	RunResumable(d Dispatch) (sessionID string, err error)
	Resume(d Dispatch, sessionID string) error
}

// paths is every path d names: the ones a backend may have to spell for its
// session, in the prompt's own mentions of them.
func (d Dispatch) paths() []string {
	return []string{d.Worktree, d.SliceJSON, d.ResultJSON, d.ExtraWriteDir}
}

// Backend runs one dispatch. A returned error means infrastructure failure
// (the backend itself could not run); the caller reads ResultJSON
// afterward regardless, and a missing file there is the caller's job to
// turn into a failed outcome.
type Backend interface {
	Run(d Dispatch) error
}

// Options configures backend construction. ScenarioDir is used by "fake"
// only, ScreenBinary, ClaudeBinary and Env by "headless" only.
type Options struct {
	ScenarioDir string // fake

	// ScreenBinary is the jig binary whose `_screen` verb a screened
	// headless dispatch's PreToolUse hook runs. Empty means this process's
	// own executable, which a screened dispatch refuses unless this process
	// is the jig binary itself: a test binary or another program running
	// screened dispatches must pass a built jig.
	ScreenBinary string // headless

	// ClaudeBinary is the `claude` program a headless dispatch runs. Empty,
	// the default, means the one on this process's PATH, looked up for each
	// dispatch. A caller that must run another - a test driving a stub -
	// names it here rather than editing PATH for the whole process.
	ClaudeBinary string // headless

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

// New constructs a Backend by name: "fake", "headless", or "herdr". Only
// "headless" ever applies Options.Env to a dispatched child (childEnv); a
// non-nil Env given to a backend that cannot apply it fails closed here,
// rather than silently dispatching that backend's child under whatever
// environment it would otherwise inherit - the exact silent drop herdr's
// own Run demonstrated before this check existed (newHerdrBackend accepted
// Options but had no env field at all). A caller that filters its own
// environment before handing it to session.New (internal/revieweval's live
// path above all) can trust that its filtering either reaches the child or
// the construction fails, never that it was quietly ignored.
func New(name string, opts Options) (Backend, error) {
	switch name {
	case "fake":
		if opts.Env != nil {
			return nil, envUnsupportedError(name)
		}
		return newFakeBackend(opts), nil
	case "headless":
		return newHeadlessBackend(opts), nil
	case "herdr":
		if opts.Env != nil {
			return nil, envUnsupportedError(name)
		}
		return newHerdrBackend(opts), nil
	default:
		return nil, fmt.Errorf("session: unknown backend %q", name)
	}
}

// envUnsupportedError names both the option and the backend that cannot
// apply it, for New's own fail-closed check above.
func envUnsupportedError(backend string) error {
	return fmt.Errorf("session: Options.Env is set, but backend %q cannot apply it; leave Env nil or dispatch through a backend that supports it (headless)", backend)
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

// GateDemoSlice is the Dispatch.Slice of a gate demo session: the dispatch
// that follows a clean reviewer round and records what the change looks like
// working. A backend that plays scenarios back tells it from a slice attempt
// (any other Slice) and from a gate review (Slice "gate") by this name.
const GateDemoSlice = "gate-demo"
