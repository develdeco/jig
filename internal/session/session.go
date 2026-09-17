// Package session dispatches one slice attempt to a coding-agent backend
// and lets the caller read the result back off disk. Three backends
// implement the same narrow interface: fake (a deterministic scenario
// player used in CI and tests), headless (a local `claude -p` subprocess),
// and herdr (a remote agent driven through a WSL-hosted herdr instance).
package session

import "fmt"

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
