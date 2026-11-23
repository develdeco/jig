package tui

import (
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// liveWindow is how long a holder may go without a line or a call before the
// step it holds reads idle: measured on a machine's ledger, 95 % of the gaps
// between one agent's calls are under 3 minutes and 97 % under 8, and a longer
// silence is mostly a person away or a command still running that a call will
// close. A session is live in the same window: whatever asks whether a session
// is live reads this constant, so the screens agree.
const liveWindow = 5 * time.Minute

// Idle is a step under way whose holder is gone (its subagent stopped, its
// session ended) or quiet (nothing from it for liveWindow). It counts no time
// from its holder's last line or call, says since when, and counts again from
// the holder's next line or call; the time between is in no counter. A step
// that waits for a person is never idle: nobody is meant to be working.

// ledgerStop is the outcome of a ledger hook line that records a stop.
const ledgerStop = "stop"

// isAgentStop is whether a machine ledger line says a subagent stopped: the
// hook that fired when it was about to end its turn, and did not hold it back.
func isAgentStop(l events.Envelope) bool {
	return l.Src == events.SrcLedger && l.Event == journal.EventHook && l.Outcome == ledgerStop &&
		l.Agent != "" && !strings.HasPrefix(l.Summary, "blocked:")
}

// idleOf is whether the holder of w has stopped working at now, and when it
// last did: its latest line or call, which is no earlier than floor (the step's
// latest line). A holder is gone when its subagent has a stop line, or its
// session an end line, after that; quiet when that is older than liveWindow.
func (m *Model) idleOf(t *ticket, w moveWindow, floor time.Time) (last time.Time, idle bool) {
	last = floor
	mine := func(l events.Envelope) bool {
		return l.Session == w.holder && (w.agent == "" || l.Agent == w.agent)
	}
	for _, l := range t.ledger {
		if at, ok := l.At(); ok && isMove(l) && mine(l) && at.After(last) {
			last = at
		}
	}
	for _, l := range t.lines {
		if at, ok := l.At(); ok && mine(l) && at.After(last) {
			last = at
		}
	}
	if m.now().Sub(last) > liveWindow {
		return last, true
	}
	if w.agent != "" {
		for _, l := range t.ledger {
			if at, ok := l.At(); ok && isAgentStop(l) && l.Agent == w.agent && l.Session == w.holder && !at.Before(last) {
				return last, true
			}
		}
	}
	if end, ok := m.w.sessionEnd[w.holder]; ok && !end.Before(last) {
		return last, true
	}
	return last, false
}

// idleWords is what a step that is idle says: since when, in the screen's zone.
func (m *Model) idleWords(since time.Time) string {
	return "idle since " + since.In(m.zone).Format("15:04")
}
