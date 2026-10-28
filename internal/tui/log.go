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
// line by what it records; a call of a subagent by the sub-step its lines name;
// any other call by the sub-step its session held when it made it, else by the
// line it wrote, else by the step it led to (the step of the next line, or the
// ticket's current step for a call after the latest).

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
	step   int
	key    string
	holder string
	// agent is the subagent the sub-step's own lines name (the reviewer that
	// closed a round), "" for none: a subagent is started for one sub-step,
	// so every call it makes is that sub-step's, however long after the
	// sub-step's last line the harness records it.
	agent      string
	start, end time.Time
	// borrowed is set for a window whose holder its sub-step's lines do not
	// name (subTrack.borrowed): a window whose lines name its holder wins.
	borrowed bool
}

// callAfter is how long after a sub-step's last line a call of its holder
// still belongs to it: the harness records a call when it ends, after the line
// the call wrote ("Close the review round" after the round's verdict).
const callAfter = 2 * time.Second

// moveWindows are the windows of the sub-steps of a ticket that stands as d
// says that are done or under way and have a holder.
func (m *Model) moveWindows(t *ticket, d phase.Ticket) []moveWindow {
	now := m.now()
	var ws []moveWindow
	for step, p := range d.Phases {
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
				ws = append(ws, moveWindow{step, s.Key, tr.holder, agentOf(tr.own), tr.start, end, tr.borrowed})
			}
		}
	}
	return ws
}

// agentOf is the subagent the latest of lines that names one names, "" for
// none.
func agentOf(lines []events.Envelope) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Agent != "" {
			return lines[i].Agent
		}
	}
	return ""
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
// first, for a call made outside every sub-step's window: it belongs to the
// line it wrote (wroteLine), else to the step of the next line, or to the step
// under way in d when none came after it.
func (t *ticket) placeOf(l events.Envelope, ws []moveWindow, all []events.Envelope, d phase.Ticket) (int, string) {
	if !isMove(l) {
		return stepOfLine(l), t.subKey(l)
	}
	at, _ := l.At()
	best := -1
	if l.Agent != "" {
		for i, w := range ws {
			if w.agent == l.Agent && at.After(w.start) && (best < 0 || w.start.After(ws[best].start)) {
				best = i
			}
		}
		if best >= 0 {
			return ws[best].step, ws[best].key
		}
	}
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
	if step, key, ok := t.wroteLine(l, at, all, d); ok {
		return step, key
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
	return currentOf(d), ""
}

// placedLine is a line of the log and the place it belongs to (placeOf).
type placedLine struct {
	l    events.Envelope
	step int
	key  string
}

// placed are the log's lines of a ticket that stands as d says, oldest first,
// each with its place.
func (m *Model) placed(t *ticket, d phase.Ticket) []placedLine {
	all := t.logLines()
	ws := m.moveWindows(t, d)
	out := make([]placedLine, len(all))
	for i, l := range all {
		step, key := t.placeOf(l, ws, all, d)
		out[i] = placedLine{l, step, key}
	}
	return out
}

// currentOf is the step under way in d: the first not done, the last when all
// are.
func currentOf(d phase.Ticket) int {
	for i, p := range d.Phases {
		if p.State != phase.StateDone {
			return i
		}
	}
	return len(d.Phases) - 1
}

// wroteLine is the place of the line call l wrote, when it wrote one: the
// harness records a call when it ends, so the first journal line written since
// the call its session (and subagent) made before it, that names that session
// or none, was written while it ran (a command that records a line, then
// pushes the store and syncs its mirror, ends seconds after it). Lines it wrote
// within a second of that one are one write, in no order that means anything
// (the pull request's, the changelog's and the publish's end), and the call
// takes the place of the one that comes first in its step (subOrder). A line
// the person wrote at a terminal is never a session's, and a session's first
// call has no call before it to bound the lines it could have written.
func (t *ticket) wroteLine(l events.Envelope, at time.Time, all []events.Envelope, d phase.Ticket) (int, string, bool) {
	var since time.Time
	for _, x := range all {
		xt, ok := x.At()
		if !ok || !xt.Before(at) {
			break
		}
		if isMove(x) && x.Session == l.Session && x.Agent == l.Agent {
			since = xt
		}
	}
	if since.IsZero() {
		return 0, "", false
	}
	var first time.Time
	step, key, rank := 0, "", -1
	for _, x := range all {
		xt, ok := x.At()
		switch {
		case !ok || !xt.After(since):
			continue
		case xt.After(at) || (!first.IsZero() && xt.Sub(first) > time.Second):
			return step, key, rank >= 0
		case isMove(x) || x.Src == srcStatus || x.Provenance == "person" || stepOfLine(x) < 0:
			continue
		case x.Session != "" && (x.Session != l.Session || (x.Agent != "" && x.Agent != l.Agent)):
			continue
		}
		if first.IsZero() {
			first = xt
		}
		s, k := stepOfLine(x), t.subKey(x)
		if r := s*1000 + subOrder(d, s, k); rank < 0 || r < rank {
			step, key, rank = s, k, r
		}
	}
	return step, key, rank >= 0
}

// subOrder is where sub-step key comes in step's sub-steps in d, after all of
// them when it is not one.
func subOrder(d phase.Ticket, step int, key string) int {
	subs := d.Phases[step].Subs
	for i, s := range subs {
		if s.Key == key {
			return i
		}
	}
	return len(subs)
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
// lines as the rows left below what p holds allow, three at least, so the
// panel's edge stays on the screen and a tall one is filled to its foot.
func (m *Model) activityPanel(p *page, t *ticket) {
	all := m.placed(t, m.phaseOf(t))
	keep := max(3, m.bodyRows()-len(p.lines)-2)
	right := ""
	if n := len(all); n > keep {
		right = fmt.Sprintf("latest %d of %d", keep, n)
	}
	p.boxTop("Activity", right)
	if len(all) == 0 {
		p.boxAdd(m.st.dim("nothing yet"))
	}
	var shown []placedLine
	tagW := 0
	for i := len(all) - 1; i >= 0 && len(shown) < keep; i-- {
		tagW = max(tagW, min(tagMaxW, width(tagOf(all[i].step, all[i].key))))
		shown = append(shown, all[i])
	}
	for _, e := range shown {
		tag := pad(tagOf(e.step, e.key), tagW)
		if e.step >= 0 {
			tag = m.st.state(stepTagState[e.step], tag)
		} else {
			tag = m.st.dim(tag)
		}
		p.boxAdd(m.st.dim(clock(e.l, m.zone)) + " " + tag + " " + m.moveText(t, e.l))
	}
	p.boxEnd()
}
