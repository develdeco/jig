package ledger

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
)

// This file is the session records of the ledger: how a harness session is
// registered (session-start), kept alive (session-touch) and closed
// (session-end), how the tool lines it writes show what it is doing, and how
// Sessions folds all of that into one Session per id.
//
// A session id is the harness's own (Claude Code's session_id) or one jig
// minted (MintID) for a session that has none. Nothing is stored per session:
// the ledger lines are the record, and Sessions is a read of them, so a
// session started by one process is listed by another with no registry to
// agree on.
//
// Line shapes, all journal.Line values in the ledger (ledger.go):
//
//	session-start  session, agent?, harness?, cwd?, clone?, ticket?, slice?, model?, effort?
//	session-touch  the same keys; every one given replaces what the session had
//	session-end    session
//	tool           session, agent?, tool?, files? (the observe hook writes these)
//
// The one key journal.Line has no field for is clone, the repository clone the
// session works in; session lines carry it beside the Line's own keys.

// Session is one harness session as the ledger's session lines fold it.
type Session struct {
	ID      string `json:"id"`
	Harness string `json:"harness,omitempty"`
	// Agents are the ids of the agents seen in the session, in the order
	// they first appeared; the session's own agent has no id and is not here.
	Agents []string `json:"agents,omitempty"`
	// Cwd is the directory the session was started in, Clone the repository
	// clone it works in, Ticket and Slice what it is working on.
	Cwd    string `json:"cwd,omitempty"`
	Clone  string `json:"clone,omitempty"`
	Ticket string `json:"ticket,omitempty"`
	Slice  string `json:"slice,omitempty"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Started is the stamp of the session's earliest line, LastSeen the
	// latest of its lines, Ended the stamp of the session-end that closed it
	// ("" for a session still open). All are RFC 3339 with nanoseconds, as
	// Stamp writes them.
	Started  string `json:"started"`
	LastSeen string `json:"last_seen"`
	Ended    string `json:"ended,omitempty"`
	// LastTool is the tool of the session's latest tool line that named one;
	// Files the files its tool lines touched, each once, in the order they
	// first appeared.
	LastTool string   `json:"last_tool,omitempty"`
	Files    []string `json:"files,omitempty"`
}

// Live reports whether the session has not ended.
func (s Session) Live() bool { return s.Ended == "" }

// Registration is what a session-start or session-touch line says about a
// session: its id, the agent the line is from, and whatever of the rest the
// writer knows. A field left empty is not written, and so changes nothing
// when the line is folded.
type Registration struct {
	ID      string
	Harness string
	Agent   string
	Cwd     string
	Clone   string
	Ticket  string
	Slice   string
	Model   string
	Effort  string
}

// MintID returns a fresh session id, "s-" and twelve hex digits, for a
// session that has none of its own.
func MintID() string {
	var b [6]byte
	rand.Read(b[:]) // never fails, and fills b entirely
	return "s-" + hex.EncodeToString(b[:])
}

// StartSession appends a session-start line for r.ID, stamped at (now when
// at is zero). A session may be started again, as a harness resumes it: the
// new line replaces what it names and reopens a session that had ended.
func StartSession(root string, at time.Time, r Registration) error {
	return appendSession(root, journal.EventSessionStart, at, r)
}

// TouchSession appends a session-touch line for r.ID: the session is still
// working, and what r names it now works on. at is as in StartSession.
func TouchSession(root string, at time.Time, r Registration) error {
	return appendSession(root, journal.EventSessionTouch, at, r)
}

// EndSession appends a session-end line for id. at is as in StartSession.
func EndSession(root string, at time.Time, id string) error {
	return appendSession(root, journal.EventSessionEnd, at, Registration{ID: id})
}

// sessionLine is a session line as the ledger stores it: the journal line and
// the clone key (see the comment at the top of the file).
type sessionLine struct {
	journal.Line
	Clone string `json:"clone,omitempty"`
}

func appendSession(root, event string, at time.Time, r Registration) error {
	if r.ID == "" {
		return fmt.Errorf("ledger: a %s line needs a session id", event)
	}
	if at.IsZero() {
		at = time.Now()
	}
	return appendValue(root, sessionLine{
		Line: journal.Line{
			TS:      Stamp(at),
			Event:   event,
			Session: r.ID,
			Agent:   r.Agent,
			Harness: r.Harness,
			Cwd:     r.Cwd,
			Ticket:  r.Ticket,
			Slice:   r.Slice,
			Model:   r.Model,
			Effort:  r.Effort,
		},
		Clone: r.Clone,
	})
}

// Graph appends a graph line: the code-graph starting points a session was
// given for a slice. l names the ticket (required), the slice, the session and
// agent, the clone directory in Cwd, the starting points in Files and anything
// worth keeping in Summary; Event is set here and TS, when empty, is now.
func Graph(root string, l journal.Line) error {
	if l.Ticket == "" {
		return errors.New("ledger: a graph line needs a ticket")
	}
	l.Event = journal.EventGraph
	return Append(root, l)
}

// Sessions folds the ledger under the jig home root into the sessions it
// records, by id. Four events make a session: session-start, session-touch,
// session-end and tool; any other line, and any line without a session id, is
// not about one. The lines fold in file order, so they say what happened in
// the order it was written:
//
//   - Started is the earliest stamp among a session's lines and LastSeen the
//     latest, whatever order the file has them in;
//   - a start or touch replaces Harness, Cwd, Clone, Model and Effort with
//     whichever it names, and Ticket with Slice (a line that names a ticket
//     replaces the slice with its own, empty or not; one that names only a
//     slice replaces only that);
//   - a start also clears Ended: the harness resumed the session;
//   - an end sets Ended, and a touch or tool line after it does not clear it;
//   - a tool line gives LastTool and Files and says nothing else about the
//     session's identity;
//   - every line's Agent is added to Agents once.
//
// A session whose first line is not a start (the hooks were installed after
// it began) is still a session. An absent ledger folds to no sessions; a
// malformed line is skipped, as in Read. Readers never lock.
func Sessions(root string) (map[string]Session, error) {
	data, err := os.ReadFile(home.LedgerPath(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]Session{}, nil
		}
		return nil, fmt.Errorf("ledger: read: %w", err)
	}
	sessions := map[string]Session{}
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		var l sessionLine
		if decodeLine(raw, &l) {
			foldSession(sessions, l)
		}
	}
	return sessions, nil
}

func foldSession(sessions map[string]Session, l sessionLine) {
	switch l.Event {
	case journal.EventSessionStart, journal.EventSessionTouch, journal.EventSessionEnd, journal.EventTool:
	default:
		return
	}
	if l.Session == "" {
		return
	}
	s, known := sessions[l.Session]
	if !known {
		s.ID = l.Session
	}

	if l.TS != "" {
		if s.Started == "" || stampBefore(l.TS, s.Started) {
			s.Started = l.TS
		}
		if s.LastSeen == "" || !stampBefore(l.TS, s.LastSeen) {
			s.LastSeen = l.TS
		}
	}
	if l.Agent != "" && !slices.Contains(s.Agents, l.Agent) {
		s.Agents = append(s.Agents, l.Agent)
	}

	switch l.Event {
	case journal.EventSessionStart, journal.EventSessionTouch:
		replaceIfSet(&s.Harness, l.Harness)
		replaceIfSet(&s.Cwd, l.Cwd)
		replaceIfSet(&s.Clone, l.Clone)
		replaceIfSet(&s.Model, l.Model)
		replaceIfSet(&s.Effort, l.Effort)
		if l.Ticket != "" {
			s.Ticket, s.Slice = l.Ticket, l.Slice
		} else {
			replaceIfSet(&s.Slice, l.Slice)
		}
		if l.Event == journal.EventSessionStart {
			s.Ended = ""
		}
	case journal.EventSessionEnd:
		s.Ended = l.TS
	case journal.EventTool:
		replaceIfSet(&s.LastTool, l.Tool)
		for _, f := range l.Files {
			if !slices.Contains(s.Files, f) {
				s.Files = append(s.Files, f)
			}
		}
	}
	sessions[l.Session] = s
}

// Recent lists sessions most recently seen first, then by id. Stamps are
// compared as instants, not as strings: RFC 3339 with trimmed nanoseconds does
// not sort as text.
func Recent(sessions map[string]Session) []Session {
	out := make([]Session, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Session) int {
		switch {
		case stampBefore(a.LastSeen, b.LastSeen):
			return 1
		case stampBefore(b.LastSeen, a.LastSeen):
			return -1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// replaceIfSet sets *dst to v unless v is empty.
func replaceIfSet(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

// stampBefore reports whether stamp a is strictly earlier than b. A stamp
// that does not parse is before nothing, so a line with a bad one is taken
// in file order.
func stampBefore(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339Nano, a)
	tb, errB := time.Parse(time.RFC3339Nano, b)
	return errA == nil && errB == nil && ta.Before(tb)
}
