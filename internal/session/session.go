// Package session dispatches one session to a coding-agent backend and lets
// the caller read the result back off disk. Two backends implement the same
// narrow interface: fake (a deterministic scenario player used in tests) and
// headless (a local `claude -p` subprocess). The jig binary links none of it
// (lint/deps_test.go): jig starts no session, and what is left here serves
// the reviewer evaluation of internal/revieweval, which launches reviewers
// to measure them.
package session

import "fmt"

// Dispatch describes one session for a backend to run. Prompt carries
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

	// ExtraReadFile, when set, is one absolute file outside the worktree that
	// the prompt names for the session to read: a gate round's recordings.json
	// under the jig home. It grants nothing, since a session's read tools are
	// not scoped to the worktree (ADR 0008), and no backend opens it. It is
	// in the dispatch so that a backend that spells a path for its session
	// differently from jig does so for this one's mention in the prompt as
	// well as for the others (headless: the long spelling of a Windows 8.3
	// name).
	ExtraReadFile string

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
	// to it. The fake backend runs no session, so it accepts the field and
	// ignores it. A caller sets it for a dispatch whose input must
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
	return []string{d.Worktree, d.SliceJSON, d.ResultJSON, d.ExtraReadFile}
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

	// ScreenBinary is the program a screened headless dispatch's PreToolUse
	// hook runs, as `<ScreenBinary> _screen`: one that prints the decision
	// of internal/screen's Hook for the tool call on its stdin (the program
	// of testdata/fixture/screenhook, or a jig binary from before the
	// verb went). A screened dispatch refuses when it is empty: there is no
	// default, since the process running the dispatch is rarely such a
	// program and would otherwise register itself as the hook.
	ScreenBinary string // headless

	// ClaudeBinary is the `claude` program a headless dispatch runs. Empty,
	// the default, means the one on this process's PATH, looked up for each
	// dispatch. A caller that must run another - a test driving a stub -
	// names it here rather than editing PATH for the whole process. It must
	// be an absolute path: a relative one is resolved against the
	// dispatch's worktree (the child's working directory), and a bare name
	// is searched for on PATH by os/exec, not here, so one that is not
	// found fails the dispatch as "claude ran no session", not as
	// CLAUDE_NOT_FOUND.
	ClaudeBinary string // headless

	// Env, when non-nil, is the exact environment a headless dispatch's
	// `claude` child process gets: this list, with any PWD, OLDPWD or
	// JIG_RECORD_DIR entry dropped and PWD then set to the dispatch's own
	// worktree (cmd.Dir) - never a mix with this process's own environment.
	// Nil, the default, means the child inherits this process's full
	// environment (and the PWD os/exec sets for the worktree), except
	// JIG_RECORD_DIR (envrun.RecordDirEnv), which only jig's own oracle run
	// sets and no session inherits. A caller that dispatches against a
	// corpus or other content it does not fully trust - internal/revieweval's
	// live path above all - should build this from a filtered copy of its
	// own environment, never pass its own os.Environ() through untouched: a
	// nil Env hands a live child everything this process happens to be
	// running with, including anything naming what is being measured.
	Env []string // headless
}

// New constructs a Backend by name: "fake" or "headless". Only "headless"
// ever applies Options.Env to a dispatched child (childEnv); a non-nil Env
// given to a backend that cannot apply it fails closed here, rather than
// silently dispatching that backend's child under whatever environment it
// would otherwise inherit. A caller that filters its own environment before
// handing it to session.New (internal/revieweval's live path above all) can
// trust that its filtering either reaches the child or the construction
// fails, never that it was quietly ignored.
func New(name string, opts Options) (Backend, error) {
	switch name {
	case "fake":
		if opts.Env != nil {
			return nil, envUnsupportedError(name)
		}
		return newFakeBackend(opts), nil
	case "headless":
		return newHeadlessBackend(opts), nil
	default:
		return nil, fmt.Errorf("session: unknown backend %q", name)
	}
}

// envUnsupportedError names both the option and the backend that cannot
// apply it, for New's own fail-closed check above.
func envUnsupportedError(backend string) error {
	return fmt.Errorf("session: Options.Env is set, but backend %q cannot apply it; leave Env nil or dispatch through a backend that supports it (headless)", backend)
}

// PublishPicksSlice is the Dispatch.Slice of a publish picks session: the
// short dispatch that chooses which of the build's recordings a pull request
// shows and composes them into flows (ADR 0029). A backend that plays
// scenarios back tells it from a slice attempt by this name.
const PublishPicksSlice = "publish-picks"
