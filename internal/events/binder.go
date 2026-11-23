package events

import (
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// A ledger line is on the ticket it names. The hook that wrote it bound it
// when it was written (internal/observe), so a reader guesses nothing from the
// session: a line of an older jig that names none is on none.
//
// The one exception is the first calls of a subagent. A subagent is started
// for one ticket, and its first calls (`jig here`, a `git status`, a read of
// the brief) come before its first claim has bound a line: they are that
// ticket's work, and join the ticket of the first line the same (session,
// agent) has bound.

const (
	// maxSeats bounds how many (session, agent) pairs a Binder follows, and
	// maxHeld how many early calls it holds for one of them; maxFold how many
	// recent calls it remembers to fold a double. A run of any length holds a
	// fixed amount.
	maxSeats = 1024
	maxHeld  = 64
	maxFold  = 4096
)

// seatKey names an agent of a session ("" is the session's own).
type seatKey struct{ session, agent string }

// seat is what a Binder knows of one agent.
type seat struct {
	// bound is true once a line of the agent named a ticket; from then on its
	// lines are on what they name.
	bound bool
	// held are the calls before that, in order.
	held []journal.Line
	// touched orders the seats for eviction.
	touched uint64
}

// callKey is what makes two tool lines one call: the session, the agent, the
// tool, the command, the words, the files and the exit status; the instants
// are compared apart (foldWindow).
type callKey struct{ session, agent, tool, command, description, files, exit string }

// stamp is the instant a call was last seen, and its text when it did not
// parse.
type stamp struct {
	ts string
	at time.Time
	ok bool
}

// foldWindow is how close two copies of a call are in time to be one: the
// hooks that hear a call stamp it at their own instants, and one agent does
// not repeat the same call in less.
const foldWindow = 500 * time.Millisecond

// Binder places ledger lines on tickets as they arrive in file order. It is
// for one goroutine.
type Binder struct {
	hold  bool
	seats map[seatKey]*seat
	tick  uint64

	seen map[callKey]stamp
	ring []callKey
	next int
}

// NewBinder returns a Binder. With hold, the early calls of a subagent are
// held until its first bound line; without it every line is placed as it
// arrives, for a reader that has no ticket to place a line on.
func NewBinder(hold bool) *Binder {
	return &Binder{hold: hold, seats: map[seatKey]*seat{}, seen: map[callKey]stamp{}}
}

// Bind takes one line and returns the lines that are placed now, in order: the
// line itself, or nothing when it is a double of a call already seen or an
// early call held back, or the held calls and then the line when it is the
// first one its agent has bound (each of them now on its ticket).
func (b *Binder) Bind(l journal.Line) []journal.Line {
	if l.Event == journal.EventTool && b.double(l) {
		return nil
	}
	if !b.hold || l.Agent == "" || l.Session == "" {
		return []journal.Line{l}
	}
	s := b.seat(seatKey{l.Session, l.Agent})
	if s.bound {
		return []journal.Line{l}
	}
	if l.Ticket == "" {
		if l.Event != journal.EventTool {
			return []journal.Line{l}
		}
		if len(s.held) == maxHeld {
			// An agent that never binds holds nothing for good: its oldest
			// early calls go, as lines on no ticket.
			s.held = s.held[1:]
		}
		s.held = append(s.held, l)
		return nil
	}
	s.bound = true
	out := make([]journal.Line, 0, len(s.held)+1)
	for _, h := range s.held {
		h.Ticket = l.Ticket
		out = append(out, h)
	}
	s.held = nil
	return append(out, l)
}

// double reports whether the tool line is a call the Binder has seen: the
// same session, agent, tool, command, words, files and exit status within
// foldWindow of its last copy (two hooks stamp a call at their own instants,
// a few milliseconds apart). It remembers the call when it is not.
func (b *Binder) double(l journal.Line) bool {
	k := callKey{l.Session, l.Agent, l.Tool, l.Command, l.Description, strings.Join(l.Files, "\x00"), exitOf(l)}
	at, err := time.Parse(time.RFC3339Nano, l.TS)
	now := stamp{ts: l.TS, at: at, ok: err == nil}
	if prev, seen := b.seen[k]; seen {
		if (now.ok && prev.ok && at.Sub(prev.at).Abs() <= foldWindow) || (!now.ok && !prev.ok && prev.ts == l.TS) {
			return true
		}
		b.seen[k] = now
		return false
	}
	if len(b.ring) < maxFold {
		b.ring = append(b.ring, k)
	} else {
		delete(b.seen, b.ring[b.next])
		b.ring[b.next] = k
		b.next = (b.next + 1) % maxFold
	}
	b.seen[k] = now
	return false
}

func exitOf(l journal.Line) string {
	if l.Exit == nil {
		return "-"
	}
	return strconv.Itoa(*l.Exit)
}

// seat is the seat of an agent, made when it has none. When there are too many,
// the one touched longest ago is let go.
func (b *Binder) seat(k seatKey) *seat {
	b.tick++
	if s, ok := b.seats[k]; ok {
		s.touched = b.tick
		return s
	}
	if len(b.seats) >= maxSeats {
		var oldest seatKey
		var at uint64
		first := true
		for key, s := range b.seats {
			if first || s.touched < at {
				oldest, at, first = key, s.touched, false
			}
		}
		delete(b.seats, oldest)
	}
	s := &seat{touched: b.tick}
	b.seats[k] = s
	return s
}
