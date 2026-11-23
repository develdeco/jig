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
// One function (clocks) computes it, and a step's row, the Steps panel's total, a
// sub-step's "took" and the open sub-step's run time all read its answer, so a
// number shown in two places is the same number.

// workRef names where work was counted: a sub-step by its step and key. A step
// with no sub-step to count in (the key "") is the one place a gap can land
// without one.
type workRef struct {
	step int
	key  string
}

// workTimes is the work of a ticket, by where it was counted.
type workTimes map[workRef]time.Duration

// of is the work of the sub-step key of step; "" for the step's own.
func (w workTimes) of(step int, key string) time.Duration { return w[workRef{step, key}] }

// ofStep is the work of step: its sub-steps' added up.
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
// neither starts nor ends a gap. A gap is the work of the sub-step whose line
// closes it, so a step's time is the sum of its sub-steps'. What of a gap a wait
// that ended covers is the wait's (wait.go), not work.
//
// The step under way also counts the time since its latest line, while that
// line is the latest of the log and the step has a sub-step at work: a slice
// being built, a brief being drafted. A step whose only open sub-steps wait for
// a person counts none: nobody is working, and the wait is not work.
func (m *Model) clocks(t *ticket, d phase.Ticket) clocks {
	placed, ws := m.placedFrom(t, d, t.workLines())
	closed, open := t.answered()
	waits := append(append(closed, phaseWaits(placed, d, open)...), asksWaits(placed, d)...)
	w := workTimes{}
	var prev time.Time
	prevStep, prevKey := -1, ""
	lines := withStarts(placed, ws)
	for i, p := range lines {
		at, ok := p.l.At()
		if p.step < 0 || !ok || p.l.Src == srcStatus {
			// A line the screen wrote when it noticed a change says when it
			// looked, not when the work was done: no launch reads the same.
			continue
		}
		if p.step == prevStep && at.After(prev) {
			// A gap belongs to the sub-step whose line closes it: a number
			// shown for a step is the sum of its sub-steps'.
			key := workKey(lines, i)
			gap := at.Sub(prev)
			for _, x := range waits {
				switch {
				case x.step != p.step:
				case !x.standing():
					gap -= overlap(prev, at, x.start, x.end)
				case x.key == key:
					// A wait that still stands, and a line of its own sub-step
					// closes the gap: the decision of one of several asks.
					gap -= overlap(prev, at, x.start, m.now())
				}
			}
			if gap > liveWindow {
				// A silence this long is idle, unless the line that ends it
				// says how long its run was (an oracle line's wall time): that
				// is work, a command a call or a line closed.
				gap = min(gap, time.Duration(p.l.Seconds)*time.Second)
			}
			if gap > 0 {
				w[workRef{p.step, key}] += gap
			}
		}
		prev, prevStep, prevKey = at, p.step, p.key
	}
	// The final run is what jig recorded of it, not the gaps between lines.
	if secs := finalSeconds(t.finalRuns()); secs > 0 {
		w[workRef{stepBuild, subFinal}] = secs
	} else {
		delete(w, workRef{stepBuild, subFinal})
	}
	idle := map[int]time.Time{}
	if cur := currentOf(d); prevStep == cur && d.Phases[cur].State != phase.StateDone && m.now().After(prev) {
		if key, ok := tailOwner(d.Phases[cur], prevKey); ok {
			// Only a sub-step whose holder is known is at work: the time since
			// the latest line is the holder's, until it is idle.
			if held := windowOf(ws, cur, key); held != nil {
				if last, gone := m.idleOf(t, *held, prev); gone {
					idle[cur] = last
				} else {
					w[workRef{cur, key}] += m.now().Sub(prev)
				}
			}
		}
	}
	return clocks{w, waits, idle}
}

// workKey is the sub-step the work that ends at lines[i] is counted in: the one
// the line belongs to, and for a line placed in its step alone the sub-step of
// the next line of the step that names one (a call that led to it), else of the
// latest before it: no time is the step's own.
func workKey(lines []placedLine, i int) string {
	if lines[i].key != "" || lines[i].step < 0 {
		return lines[i].key
	}
	for _, x := range lines[i+1:] {
		if x.step == lines[i].step && x.key != "" {
			return x.key
		}
		if x.step >= 0 && x.step != lines[i].step {
			break
		}
	}
	// No sub-step follows it: the work closes into the one before.
	for j := i - 1; j >= 0; j-- {
		if lines[j].step == lines[i].step && lines[j].key != "" {
			return lines[j].key
		}
	}
	return ""
}

// windowOf is the window of the sub-step key of step that is under way, nil
// when its holder is not known.
func windowOf(ws []moveWindow, step int, key string) *moveWindow {
	for i := range ws {
		if ws[i].step == step && ws[i].key == key && ws[i].open {
			return &ws[i]
		}
	}
	return nil
}

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
// fix) that the line is of, else the first one at work. False when none is at
// work: nobody holds the step, whether it waits for a person, between its
// slices or parked, and the time passing is no work.
func tailOwner(p phase.Phase, lineKey string) (string, bool) {
	first, atWork := "", false
	for _, s := range p.Subs {
		if s.State != phase.SubActive && s.State != phase.SubFailed {
			continue
		}
		if s.Key == lineKey {
			return lineKey, true
		}
		if !atWork {
			first, atWork = s.Key, true
		}
	}
	return first, atWork
}
