package observe

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/spawn"
	"github.com/develdeco/jig/internal/store"
)

// Deps is what the core works with. cmd/jig builds it from its env, so a
// test gives the core a clock, a jig home and a process starter of its own.
type Deps struct {
	// JigHome is the jig home root: the ledger, the machine map and the
	// verification logs live under it.
	JigHome string
	// Cwd is the directory an event that names none happened in.
	Cwd string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Getenv reads the environment (JIG_SESSION, JIG_SLICE); nil reads an
	// empty one.
	Getenv func(string) string
	// Executable is the jig binary a verification is started with, and Start
	// starts it detached: spawn.Detached in production.
	Executable string
	Start      func(spawn.Spec) (int, error)
	// OpenStore opens the store a located directory names, checking its
	// schema; a nil store and a nil error when the store is not on this
	// machine. The store layout is cmd/jig to know, so the core asks.
	OpenStore func(locate.Where) (*store.Store, project.Config, error)
	// Here renders what `jig here` says about a located directory: the text a
	// session is given when it starts. The sections of that text are cmd/jig
	// to know.
	Here func(locate.Where, *store.Store) (string, error)
	// Sources are the note sources to call; nil means the registered ones.
	Sources []NamedSource
}

func (d Deps) withDefaults() Deps {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Getenv == nil {
		d.Getenv = func(string) string { return "" }
	}
	if d.Sources == nil {
		d.Sources = NoteSources()
	}
	return d
}

// Handle acts on one event and says what the harness should be told. Its
// answer may be partial: the error is what went wrong on the way (a ledger
// line that could not be written, a note source that failed), and the Result
// still holds what could be done. It recovers from a panic into an error, so
// a hook that calls it cannot fail the harness.
func Handle(d Deps, ev Event) (res Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			res, err = Result{}, fmt.Errorf("observe: %s event: panic: %v", ev.Kind, r)
		}
	}()
	if err := ev.Validate(); err != nil {
		return Result{}, err
	}
	h := &handler{d: d.withDefaults(), ev: ev}
	switch ev.Kind {
	case KindSessionStart:
		res = h.sessionStart()
	case KindPrompt:
		res = h.prompt()
	case KindTool:
		res = h.tool()
	case KindStop:
		res = h.stop()
	case KindSessionEnd:
		res = h.sessionEnd()
	case KindPostCommit:
		res = h.postCommit()
	}
	return res, errors.Join(h.errs...)
}

// Run parses payload with the adapter, handles the event and returns what
// the harness reads, in the adapter shape (nil for nothing). An event the
// adapter does not map is ignored. Like Handle it never panics.
func Run(d Deps, a Adapter, data []byte) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("observe: %s: panic: %v", a.Name(), r)
		}
	}()
	ev, err := a.Parse(data)
	if err != nil {
		return nil, err
	}
	return RunEvent(d, a, ev)
}

// RunEvent is Run for an event already parsed.
func RunEvent(d Deps, a Adapter, ev Event) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("observe: %s: panic: %v", a.Name(), r)
		}
	}()
	if ev.Kind == "" {
		return nil, nil
	}
	res, err := Handle(d, ev)
	if len(res.Env) > 0 {
		said, envErr := handOffEnv(d.withDefaults(), a, res.Env)
		res.Context = joinText(res.Context, said)
		err = errors.Join(err, envErr)
	}
	return a.Render(ev, res), err
}

// handOffEnv gives vars to the shell commands the agent runs later: through
// the harness when the adapter can set them, else as a line of context
// asking the agent to set them itself, which it returns. A harness that
// failed to set them gets that line too, with the error.
func handOffEnv(d Deps, a Adapter, vars map[string]string) (string, error) {
	if s, ok := a.(EnvSetter); ok {
		set, err := s.SetEnv(d.Getenv, vars)
		if set && err == nil {
			return "", nil
		}
		if err != nil {
			return envContext(vars), err
		}
	}
	return envContext(vars), nil
}

// envContext asks the agent to run its jig commands with vars set, for a
// harness that could not set them.
func envContext(vars map[string]string) string {
	names := slices.Sorted(maps.Keys(vars))
	pairs := make([]string, len(names))
	for i, n := range names {
		pairs[i] = n + "=" + vars[n]
	}
	return "Run the jig commands of this session with " + strings.Join(pairs, " ") +
		" in their environment: the harness could not set it for them."
}

// The outcomes a note-delivered line records: the note was given as context
// in a tool call or prompt, or it was in a refusal to stop.
const (
	outcomeTool      = "tool"
	outcomePrompt    = "prompt"
	outcomeStopBlock = "stop-block"
)

// handler is one event being handled.
type handler struct {
	d  Deps
	ev Event

	w   locate.Where
	st  *store.Store
	cfg project.Config

	mem     sessionMemory
	memRead bool

	jlines []journal.Line
	jread  bool
	jerr   error

	unattendedKnown, unattendedVal bool

	errs []error
}

func (h *handler) fail(err error) {
	if err != nil {
		h.errs = append(h.errs, err)
	}
}

// locate places the event directory. It reports false when the directory is
// in no jig project, or could not be placed (the error is kept): then
// nothing else is done, and nothing is written, since a hook installed for
// every session of the machine hears of sessions that are none of jig.
func (h *handler) locate() bool {
	cwd := h.ev.Cwd
	if cwd == "" {
		cwd = h.d.Cwd
	}
	if cwd == "" {
		h.fail(errors.New("observe: the event names no directory"))
		return false
	}
	w, err := locate.Locate(h.d.JigHome, cwd, locate.Opts{Session: h.ev.Session, Getenv: h.d.Getenv})
	if err != nil {
		h.fail(err)
		return false
	}
	if w.How == locate.HowNone {
		return false
	}
	h.w = w
	if h.d.OpenStore != nil && w.StorePath != "" {
		st, cfg, err := h.d.OpenStore(w)
		h.fail(err)
		h.st, h.cfg = st, cfg
	}
	return true
}

// session is the id the event is for: the harness's, else the one Locate
// found in the environment.
func (h *handler) session() string {
	if h.ev.Session != "" {
		return h.ev.Session
	}
	return h.w.Session
}

// memory is what the ledger says of the session, read once per event.
func (h *handler) memory() sessionMemory {
	if !h.memRead {
		h.memRead = true
		var err error
		h.mem, err = readMemory(h.d.JigHome, h.ev.Session)
		h.fail(err)
	}
	return h.mem
}

// journal is the located ticket journal, read once per event.
func (h *handler) journal() ([]journal.Line, error) {
	if !h.jread {
		h.jread = true
		if h.st != nil && h.w.Ticket != "" {
			h.jlines, h.jerr = journal.Read(h.st, h.w.Ticket)
		}
	}
	return h.jlines, h.jerr
}

// head is the commit HEAD names in the directory of the event, empty when it
// names none (not a work tree, a repository with no commit yet).
func (h *handler) head() string {
	sha, err := gitx.RevParse(h.w.Cwd, "HEAD")
	if err != nil {
		return ""
	}
	return sha
}

// base is a ledger line of the event session at the located place.
func (h *handler) base(event string) journal.Line {
	return journal.Line{
		TS:      ledger.Stamp(h.d.Now()),
		Event:   event,
		Session: h.ev.Session,
		Agent:   h.ev.Agent,
		Harness: h.ev.Harness,
		Ticket:  h.w.Ticket,
		Slice:   h.w.Slice,
		Cwd:     h.w.Cwd,
	}
}

// hook records that a hook of the harness fired. Every event of a harness
// leaves a hook line or a tool line carrying its name, which is what a doctor
// reads to say the hooks are seen: a session start, prompt, stop and session
// end write this one, a tool call its own.
func (h *handler) hook(summary string) {
	l := h.base(journal.EventHook)
	l.Outcome = h.ev.Kind
	l.Summary = oneLine(summary, 600)
	h.fail(ledger.Append(h.d.JigHome, l))
}
