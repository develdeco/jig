package tui

import (
	"slices"
	"sort"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// What a counter on a screen counts is work: the time a holder had a sub-step.
// One function (work) computes it, and a step's row, the Steps panel's total, a
// sub-step's "took" and the open sub-step's run time all read its answer, so a
// number shown in two places is the same number.

// workRef names where work was counted: a sub-step by its step and key, or a
// step's own time by its step and the key "" (the time between its sub-steps,
// and the calls no sub-step held).
type workRef struct {
	step int
	key  string
}

// workTimes is the work of a ticket, by where it was counted.
type workTimes map[workRef]time.Duration

// of is the work of the sub-step key of step; "" for the step's own.
func (w workTimes) of(step int, key string) time.Duration { return w[workRef{step, key}] }

// ofStep is the work of step: its own and its sub-steps' added up.
func (w workTimes) ofStep(step int) time.Duration {
	var d time.Duration
	for r, v := range w {
		if r.step == step {
			d += v
		}
	}
	return d
}

// clocks are the work of the ticket and its waits, standing as d says. Work is
// how long the ticket's holders worked: the gaps between consecutive lines of
// the log that both belong to one step (placed), so a build that a review sent a
// fix back to counts the fix and not the review between, and the time from one
// step to the next (a person reading the outcome before prompting the next
// phase) counts for neither. A line of no step (a spend, a call nothing held)
// neither starts nor ends a gap. A gap between two lines of one sub-step is that
// sub-step's work; one between lines of two sub-steps is the step's own. What of
// a gap a wait that ended covers is the wait's (wait.go), not work.
//
// The step under way also counts the time since its latest line, while that
// line is the latest of the log and the step has a sub-step at work: a slice
// being built, a brief being drafted. A step whose only open sub-steps wait for
// a person counts none: nobody is working, and the wait is not work.
func (m *Model) clocks(t *ticket, d phase.Ticket) clocks {
	placed, ws := m.placedIn(t, d)
	closed, open := t.answered()
	waits := append(closed, phaseWaits(placed, d, open)...)
	w := workTimes{}
	var prev time.Time
	prevStep, prevKey := -1, ""
	for _, p := range withStarts(placed, ws) {
		at, ok := p.l.At()
		if p.step < 0 || !ok {
			continue
		}
		if p.step == prevStep && at.After(prev) {
			key := ""
			if p.key == prevKey {
				key = p.key
			}
			gap := at.Sub(prev)
			for _, x := range waits {
				if x.step == p.step && !x.standing() {
					gap -= overlap(prev, at, x.start, x.end)
				}
			}
			if gap > 0 {
				w[workRef{p.step, key}] += gap
			}
		}
		prev, prevStep, prevKey = at, p.step, p.key
	}
	if cur := currentOf(d); prevStep == cur && d.Phases[cur].State != phase.StateDone && m.now().After(prev) {
		if key, ok := tailOwner(d.Phases[cur], prevKey); ok {
			w[workRef{cur, key}] += m.now().Sub(prev)
		}
	}
	return clocks{w, waits}
}

// work is the work of the ticket (clocks).
func (m *Model) work(t *ticket, d phase.Ticket) workTimes { return m.clocks(t, d).work }

// withStarts is the lines with, for each sub-step under way that no line of its
// own opened (a brief being drafted by the session the ledger ties to it), a
// line at the time its holder took it: the work of a sub-step begins when the
// holder does, not at the first call that happens to name the ticket.
func withStarts(lines []placedLine, ws []moveWindow) []placedLine {
	out := lines
	for _, w := range ws {
		if !w.open || !w.borrowed {
			continue
		}
		start := placedLine{l: events.Envelope{Line: journal.Line{TS: w.start.UTC().Format(time.RFC3339Nano)}}, step: w.step, key: w.key}
		i := sort.Search(len(out), func(i int) bool {
			at, ok := out[i].l.At()
			return ok && at.After(w.start)
		})
		out = slices.Insert(slices.Clone(out), i, start)
	}
	return out
}

// tailOwner is the sub-step of p that the time since the step's latest line
// belongs to: the sub-step at work (under way, or failed and the builder's to
// fix) that the line is of, else the first one at work, else the step's own
// time. False when none is at work and one waits for a person.
func tailOwner(p phase.Phase, lineKey string) (string, bool) {
	first, atWork, waits := "", false, false
	for _, s := range p.Subs {
		switch s.State {
		case phase.SubActive, phase.SubFailed:
			if s.Key == lineKey {
				return lineKey, true
			}
			if !atWork {
				first, atWork = s.Key, true
			}
		case phase.SubNeedsYou:
			waits = true
		}
	}
	return first, atWork || !waits
}
