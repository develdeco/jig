package tui

import (
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// A wait is time a sub-step stands that waits for a person (the brief's
// approval, an answer, a decision, the push approval) or for the host (a merge).
// Nobody holds such a sub-step, and the time it stands is not work: it has a
// clock of its own, from the line that opened it to the line that closed it.

// waitSpan is one wait: the sub-step that waited, for whom, the sub-step's own
// words for it, and when it began and ended (end is zero while it stands).
type waitSpan struct {
	step       int
	key        string
	forWhom    string
	words      string
	start, end time.Time
}

// standing says the wait has not ended.
func (w waitSpan) standing() bool { return w.end.IsZero() }

// clocks are the times a ticket's screens show: the work, and the waits.
type clocks struct {
	work  workTimes
	waits []waitSpan
}

// waitOf is the wait of the sub-step key of step, done or standing: the latest
// when it waited more than once.
func (c clocks) waitOf(step int, key string) (waitSpan, bool) {
	for i := len(c.waits) - 1; i >= 0; i-- {
		if w := c.waits[i]; w.step == step && w.key == key {
			return w, true
		}
	}
	return waitSpan{}, false
}

// standingOf is the wait that has stood longest in step, if one stands.
func (c clocks) standingOf(step int) (waitSpan, bool) {
	var best waitSpan
	found := false
	for _, w := range c.waits {
		if w.step == step && w.standing() && (!found || w.start.Before(best.start)) {
			best, found = w, true
		}
	}
	return best, found
}

// answered are the questions of the ticket: the waits that an answer closed, and
// when each slice's open question was asked, by the sub-step key. An answer
// closes the question it names.
func (t *ticket) answered() (closed []waitSpan, open map[string]time.Time) {
	asked := map[string]events.Envelope{}
	for _, l := range t.lines {
		switch l.Event {
		case journal.EventQuestion:
			asked[l.Ref] = l
		case journal.EventAnswer:
			q, ok := asked[l.Ref]
			if !ok {
				continue
			}
			delete(asked, l.Ref)
			qa, ok1 := q.At()
			aa, ok2 := l.At()
			if ok1 && ok2 {
				closed = append(closed, waitSpan{stepBuild, sliceKey(q.Slice), phase.WaitsForPerson, "waits for your answer", qa, aa})
			}
		}
	}
	open = map[string]time.Time{}
	for _, q := range asked {
		if at, ok := q.At(); ok {
			open[sliceKey(q.Slice)] = at
		}
	}
	return closed, open
}

// sliceKey is the sub-step key of a slice, "" for a question of no slice.
func sliceKey(slice string) string {
	if slice == "" {
		return ""
	}
	return "slice:" + slice
}

// phaseWaits are the waits of the sub-steps that d says wait for someone, in
// every phase d derives (the Land phase too), read from the log's placed lines.
// A sub-step that waits stands from the line that opened it: the latest line of
// its step that is not its own (the draft's last line for the brief's approval,
// the round's close for its asks), the publish's last line for a landing, the
// question's line for an answer. A wait that was given its answer or approval
// ran to the sub-step's first line, or for a merge to the landing.
func phaseWaits(placed []placedLine, d phase.Ticket, openQuestions map[string]time.Time) []waitSpan {
	var out []waitSpan
	for step, p := range d.Phases {
		for _, s := range p.Subs {
			if s.WaitsFor == "" {
				continue
			}
			switch s.State {
			case phase.SubNeedsYou, phase.SubWaiting:
				if start, ok := waitStart(placed, step, s.Key, time.Time{}, openQuestions); ok {
					out = append(out, waitSpan{step, s.Key, s.WaitsFor, s.Detail, start, time.Time{}})
				}
			case phase.SubDone:
				end, ok := waitEnd(placed, step, s.Key)
				if !ok {
					continue
				}
				if start, ok := waitStart(placed, step, s.Key, end, nil); ok && end.After(start) {
					out = append(out, waitSpan{step, s.Key, s.WaitsFor, s.Detail, start, end})
				}
			}
		}
	}
	return out
}

// waitEnd is the line that closed the wait of the sub-step key of step: its
// first line, or for a landing the person's word to merge (the session merging
// is at work), else its record.
func waitEnd(placed []placedLine, step int, key string) (time.Time, bool) {
	for _, p := range placed {
		at, ok := p.l.At()
		if !ok || p.step != step {
			continue
		}
		if (step == stepLand && (p.l.Event == journal.EventLanded || p.l.Event == journal.EventLandNamed)) || (step != stepLand && p.key == key) {
			return at, true
		}
	}
	return time.Time{}, false
}

// waitStart is where a wait began: the line that opened it. before bounds the
// search for a wait that has ended (zero for one that stands).
func waitStart(placed []placedLine, step int, key string, before time.Time, openQuestions map[string]time.Time) (time.Time, bool) {
	if at, ok := openQuestions[key]; ok && step == stepBuild && before.IsZero() {
		return at, true
	}
	from := step
	if step == stepLand {
		// A landing waits from the publish's last line; once the merge is
		// named the session merging it is at work, and nobody waits.
		if before.IsZero() {
			for _, p := range placed {
				if p.step == stepLand && p.l.Event == journal.EventLandNamed {
					return time.Time{}, false
				}
			}
		}
		from = stepPublish
	}
	latest := func(step int, own string) (time.Time, bool) {
		var found time.Time
		for _, p := range placed {
			at, ok := p.l.At()
			if !ok || p.step != step || p.key == own || (!before.IsZero() && !at.Before(before)) {
				continue
			}
			if at.After(found) {
				found = at
			}
		}
		return found, !found.IsZero()
	}
	if at, ok := latest(from, key); ok {
		return at, true
	}
	if from > 0 {
		return latest(from-1, "")
	}
	return time.Time{}, false
}

// overlap is how long [a0, a1] and [b0, b1] have in common.
func overlap(a0, a1, b0, b1 time.Time) time.Duration {
	lo, hi := a0, a1
	if b0.After(lo) {
		lo = b0
	}
	if b1.Before(hi) {
		hi = b1
	}
	return max(0, hi.Sub(lo))
}

// waitClock is how long a wait stood, or has stood so far.
func (m *Model) waitClock(w waitSpan) string {
	end := w.end
	if end.IsZero() {
		end = m.now()
	}
	return ago(end.Sub(w.start))
}

// waitHeader is the header's word for a step whose open sub-step waits: the
// step's name and the sub-step's words ("brief waits for your approval"), when
// those words say what is awaited.
func waitHeader(step int, w waitSpan) string {
	if !strings.HasPrefix(w.words, "waits for ") {
		return ""
	}
	return strings.ToLower(stepNames[step]) + " " + w.words
}
