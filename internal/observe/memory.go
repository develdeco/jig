package observe

import (
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
)

// What the core remembers of a session, it reads from the machine ledger: the
// lines it wrote for the session earlier. Nothing is kept anywhere else, so
// the next hook is a new process that knows only what the ledger says.

// memoryWindow bounds how far back in the ledger a session is looked for.
// The ledger grows with every tool call of the machine; a session that
// began further back than this is treated as having begun where the window
// ends, which at worst shows it a note a second time.
const memoryWindow = 16 << 20

// sessionMemory is what the ledger says a session has done lately.
type sessionMemory struct {
	// started is the stamp of the session-start line, or of the earliest line
	// of the session in the window when it has none; zero when the ledger has
	// nothing of the session.
	started time.Time
	// head is the HEAD the newest line that recorded one saw, and headCwd
	// the directory that line was written from.
	head, headCwd string
	// ticket and slice are what the newest line that named a ticket says the
	// session works on.
	ticket, slice string
	// harness and model are what the session's start (or a later touch)
	// line said it runs.
	harness, model string
	// delivered holds the Refs of the notes already given to the session (as
	// context or in a refusal to stop), blocked the Refs it was refused a
	// Stop for.
	delivered, blocked map[string]bool
}

// readMemory reads what the ledger says of session. An empty session id has
// no memory: nothing could say which lines are its own.
func readMemory(root, session string) (sessionMemory, error) {
	m := sessionMemory{delivered: map[string]bool{}, blocked: map[string]bool{}}
	if session == "" {
		return m, nil
	}
	err := ledger.ReadBackward(root, memoryWindow, func(l journal.Line) bool {
		if l.Session != session {
			return true
		}
		if at, err := time.Parse(time.RFC3339Nano, l.TS); err == nil {
			m.started = at // the walk goes back in time: the last one set is the earliest
		}
		switch l.Event {
		case journal.EventNoteDelivered:
			m.delivered[l.Ref] = true
			if l.Outcome == outcomeStopBlock {
				m.blocked[l.Ref] = true
			}
		case journal.EventTool, journal.EventHook, journal.EventSessionStart, journal.EventSessionTouch:
			if m.head == "" && l.Commit != "" {
				m.head, m.headCwd = l.Commit, l.Cwd
			}
			if m.ticket == "" && l.Ticket != "" {
				m.ticket, m.slice = l.Ticket, l.Slice
			}
			if m.model == "" && l.Model != "" {
				m.model = l.Model
			}
			if m.harness == "" && l.Harness != "" {
				m.harness = l.Harness
			}
		}
		// A session-start line ends the walk: what came before it belongs to
		// an earlier life of the session, or to none.
		return l.Event != journal.EventSessionStart
	})
	return m, err
}
