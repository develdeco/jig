package tui

import (
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// liveWindow is how recent a ledger line of a session must be for the session to
// count as working on a ticket now. One minute is a guess, to be measured on a
// real run: this is the one place to change it.
const liveWindow = time.Minute

// live is whether a session is working on the ticket now, and how long ago its
// latest sign was: a ledger line of the machine bound to the ticket (a tool
// call, a session start or touch) within liveWindow of now, from a session that
// has not written a later session end. The store says nothing of a session at
// work, so this is read from the ledger's recency and from nothing else.
func (t *ticket) live(now time.Time) (time.Duration, bool) {
	ended := map[string]time.Time{}
	for _, l := range t.ledger {
		if l.Event != journal.EventSessionEnd {
			continue
		}
		if at, ok := l.At(); ok && at.After(ended[l.Session]) {
			ended[l.Session] = at
		}
	}
	best, found := time.Duration(0), false
	for _, l := range t.ledger {
		switch l.Event {
		case journal.EventTool, journal.EventSessionStart, journal.EventSessionTouch:
		default:
			continue
		}
		at, ok := l.At()
		if !ok {
			continue
		}
		if end, gone := ended[l.Session]; gone && l.Session != "" && !at.After(end) {
			continue
		}
		age := max(now.Sub(at), 0)
		if age > liveWindow {
			continue
		}
		if !found || age < best {
			best, found = age, true
		}
	}
	return best, found
}

// live is whether a session is working on any ticket of the chart now, and how
// long ago the latest sign of one was.
func (c *chart) live(w *world, now time.Time) (time.Duration, bool) {
	best, found := time.Duration(0), false
	for _, id := range c.ids() {
		t := w.tickets[id]
		if t == nil {
			continue
		}
		if age, ok := t.live(now); ok && (!found || age < best) {
			best, found = age, true
		}
	}
	return best, found
}

// ticketLive is whether a session is working on the ticket now, by the model's
// clock.
func (m *Model) ticketLive(t *ticket) bool {
	if t == nil {
		return false
	}
	_, ok := t.live(m.now())
	return ok
}
