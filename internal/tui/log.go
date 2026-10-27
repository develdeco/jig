package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/phase"
)

// A ticket's activity is one log: its journal lines, what a status showed of
// the brief, and the calls its sessions made (the machine ledger's tool lines,
// in the words each session gave them), newest first. Each line is tagged with
// the step and sub-step it belongs to ("build·clamp", "review·r2"): a journal
// line by what it records, a call by the sub-step its session held when it made
// it, else by the step it led to (the step of the next line, or the ticket's
// current step for a call after the latest).

// logLines are the ticket's lines and moves, oldest first.
func (t *ticket) logLines() []events.Envelope {
	out := append([]events.Envelope(nil), t.lines...)
	for _, s := range t.seen {
		out = insertSorted(out, s)
	}
	for _, l := range t.ledger {
		if isMove(l) {
			out = insertSorted(out, l)
		}
	}
	return out
}

// moveWindow is the time a sub-step was held, and by which session: a call
// that session made in it belongs to the sub-step.
type moveWindow struct {
	step       int
	key        string
	holder     string
	start, end time.Time
	// borrowed is set for a window whose holder its sub-step's lines do not
	// name (subTrack.borrowed): a window whose lines name its holder wins.
	borrowed bool
}

// callAfter is how long after a sub-step's last line a call of its holder
// still belongs to it: the harness records a call when it ends, after the line
// the call wrote ("Close the review round" after the round's verdict).
const callAfter = 2 * time.Second

// moveWindows are the windows of the ticket's sub-steps that are done or under
// way and have a holder.
func (m *Model) moveWindows(t *ticket) []moveWindow {
	now := m.now()
	var ws []moveWindow
	for step, p := range m.phaseOf(t).Phases {
		for _, s := range p.Subs {
			var tr subTrack
			end := now
			switch {
			case s.State == phase.SubDone:
				tr = t.doneTrack(step, s.Key)
				end = tr.end.Add(callAfter)
			case subWeight(s) < 2:
				tr = t.openTrack(step, s.Key)
			default:
				continue
			}
			if tr.holder != "" && !tr.start.IsZero() {
				ws = append(ws, moveWindow{step, s.Key, tr.holder, tr.start, end, tr.borrowed})
			}
		}
	}
	return ws
}

// doneTrack is trackOf for a sub-step that is done. One whose lines name no
// session (a brief drafted, a body written: lines the screens or jig wrote) is
// held by the session of the latest line before it that names one, else by the
// session the machine ledger ties to the ticket, from the line before it. A
// line written within a second of its own is not before it: one call writes
// them together (the pull request, the changelog and the publish's end).
func (t *ticket) doneTrack(step int, key string) subTrack {
	tr := t.trackOf(step, key)
	if tr.holder != "" || tr.start.IsZero() {
		return tr
	}
	var prev time.Time
	for _, l := range append(append([]events.Envelope(nil), t.lines...), t.seen...) {
		at, ok := l.At()
		if !ok || !at.Before(tr.start.Add(-time.Second)) {
			continue
		}
		if at.After(prev) {
			prev = at
		}
		if l.Session != "" && l.Src != srcStatus {
			tr.holder = l.Session
		}
	}
	if tr.holder == "" {
		for _, l := range t.ledger {
			if l.Session != "" {
				tr.holder = l.Session
				if at, ok := l.At(); ok && prev.IsZero() {
					prev = at
				}
				break
			}
		}
	}
	if !prev.IsZero() {
		tr.start, tr.borrowed = prev, true
	}
	return tr
}

// placeOf is the step and sub-step key line l belongs to (key "" for the step
// alone; step -1 for the ticket as a whole). all are the log's lines, oldest
// first, for a call made between sub-steps: it belongs to the step of the next
// line, or to current (the ticket's current step) when none came after it.
func (t *ticket) placeOf(l events.Envelope, ws []moveWindow, all []events.Envelope, current int) (int, string) {
	if !isMove(l) {
		return stepOfLine(l), t.subKey(l)
	}
	at, _ := l.At()
	best := -1
	for i, w := range ws {
		if w.holder != l.Session || !at.After(w.start) || at.After(w.end) {
			continue
		}
		if best < 0 || (ws[best].borrowed && !w.borrowed) || (ws[best].borrowed == w.borrowed && w.start.After(ws[best].start)) {
			best = i
		}
	}
	if best >= 0 {
		return ws[best].step, ws[best].key
	}
	for _, x := range all {
		if isMove(x) {
			continue
		}
		if xt, ok := x.At(); ok && xt.After(at) {
			if s := stepOfLine(x); s >= 0 {
				return s, ""
			}
		}
	}
	return current, ""
}

// tagOf is a place as a log line's tag: the step's name, and the sub-step's
// short name after a dot.
func tagOf(step int, key string) string {
	if step < 0 {
		return "ticket"
	}
	name := strings.ToLower(stepNames[step])
	if key == "" {
		return name
	}
	kind, id, _ := strings.Cut(key, ":")
	switch kind {
	case "slice":
		return name + "·" + id
	case "round":
		return name + "·r" + id
	case "pr":
		return strings.TrimSpace(name + "·pr " + id)
	}
	return name + "·" + kind
}

// stepTagState is the state whose color tags a step's lines.
var stepTagState = [stepCount]string{StateNeedsInput, StateBuilding, StateReviewing, StatePublishing}

// tagMaxW is the most cells a tag takes.
const tagMaxW = 18

// activityPanel adds the ticket's activity log to p, newest first: as many
// lines as the rows left below what p holds allow, activityCap at most and
// three at least, so the panel's edge stays on the screen.
func (m *Model) activityPanel(p *page, t *ticket) {
	all := t.logLines()
	keep := min(activityCap(m.bodyRows()), max(3, m.bodyRows()-len(p.lines)-2))
	right := ""
	if n := len(all); n > keep {
		right = fmt.Sprintf("latest %d of %d", keep, n)
	}
	p.boxTop("Activity", right)
	if len(all) == 0 {
		p.boxAdd(m.st.dim("nothing yet"))
	}
	ws := m.moveWindows(t)
	current := currentStep(m.steps(t))
	type entry struct {
		l    events.Envelope
		step int
		tag  string
	}
	var shown []entry
	tagW := 0
	for i := len(all) - 1; i >= 0 && len(shown) < keep; i-- {
		step, key := t.placeOf(all[i], ws, all, current)
		tag := tagOf(step, key)
		tagW = max(tagW, min(tagMaxW, width(tag)))
		shown = append(shown, entry{all[i], step, tag})
	}
	for _, e := range shown {
		tag := pad(e.tag, tagW)
		if e.step >= 0 {
			tag = m.st.state(stepTagState[e.step], tag)
		} else {
			tag = m.st.dim(tag)
		}
		p.boxAdd(m.st.dim(clock(e.l, m.zone)) + " " + tag + " " + m.moveText(t, e.l))
	}
	p.boxEnd()
}
