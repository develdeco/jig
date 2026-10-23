package observe

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/store"
)

// A Note is one thing jig has to tell the session: a verification that
// failed, a question that was answered, a correction. Notes come from note
// sources; the core delivers each to a session once, as context at the next
// tool call or prompt, and keeps the ones that matter from letting an
// unattended session stop.

// Note is one thing to tell a session.
type Note struct {
	// Ref names the fact the note is about, the same every time the source
	// reports it: the core delivers a Ref to a session once. It is a
	// fingerprint, not a counter (verified:3f2a1b9c0d12:a:1).
	Ref string
	// Text is what the session reads, one line.
	Text string
	// Enforce makes the note hold an unattended session back: when the
	// session is under an unattended claim (or the project defaults to
	// unattended), a Stop with an enforced note it has not been refused for
	// yet is refused, once. An interactive session is only told, and a note
	// that is not Enforce is only ever told: spend is the example.
	Enforce bool
	// Source is the name of the source that returned the note; the core
	// sets it.
	Source string
}

// NoteCtx is what a note source is given: the event, where it happened, and
// the means to read the ticket the session works on.
type NoteCtx struct {
	// Event is the event being handled; a source acts on the kinds it wants
	// (KindTool, KindPrompt, KindStop, KindSessionEnd) and returns nothing
	// for the rest.
	Event Event
	// Where is where the event happened. A source is called only when the
	// directory is in a project, and when it works on a ticket in a store
	// that is here, so Where.Ticket and Store are set.
	Where locate.Where
	// Store is the ticket store.
	Store *store.Store
	// JigHome is the jig home root, where the ledger is.
	JigHome string
	// Now is the clock.
	Now time.Time
	// Since is when the session began, as the ledger knows it: a fact older
	// than that was not made in this session. It is zero when the ledger has
	// no line of the session yet.
	Since time.Time
	// Commit is the commit HEAD moved to during this event (a tool call that
	// made one), empty otherwise.
	Commit string
	// Unattended is true when the session is under an unattended claim, or
	// the project defaults to unattended: the notes it enforces are enforced.
	Unattended bool
	// Journal reads the located ticket journal, once per event.
	Journal func() ([]journal.Line, error)
}

// NoteSource turns what is true of a session ticket now into notes. It is
// called at every tool, prompt, stop and session-end event of a session in a
// project, so it must be quick and must return the same Ref for the same
// fact. It may write its own records (a correction line, a spend line): that
// is how a fact it reports once is not reported twice. The notes it returns
// at a session-end are not delivered; the call is its chance to write what it
// keeps.
type NoteSource func(NoteCtx) ([]Note, error)

// NamedSource is a registered NoteSource.
type NamedSource struct {
	Name string
	Fn   NoteSource
}

var (
	sourcesMu sync.Mutex
	sources   []NamedSource
)

// RegisterNoteSource adds a note source, from an init() of the file that
// owns it. It panics on an empty name, a nil function or a name already
// taken: a registration is source code, so each is a bug found the first
// time the binary starts.
func RegisterNoteSource(name string, fn NoteSource) {
	if name == "" || fn == nil {
		panic("observe: RegisterNoteSource needs a name and a function")
	}
	sourcesMu.Lock()
	defer sourcesMu.Unlock()
	for _, s := range sources {
		if s.Name == name {
			panic(fmt.Sprintf("observe: note source %q is already registered", name))
		}
	}
	sources = append(sources, NamedSource{name, fn})
}

// NoteSources returns the registered note sources by name.
func NoteSources() []NamedSource {
	sourcesMu.Lock()
	defer sourcesMu.Unlock()
	out := slices.Clone(sources)
	slices.SortFunc(out, func(a, b NamedSource) int { return strings.Compare(a.Name, b.Name) })
	return out
}
