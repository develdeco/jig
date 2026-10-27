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
// it, else by the step the ticket was at.

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
}

// moveWindows are the windows of the ticket's sub-steps that are done or under
// way and name a holder.
func (m *Model) moveWindows(t *ticket) []moveWindow {
	now := m.now()
	var ws []moveWindow
	for step, p := range m.phaseOf(t).Phases {
		for _, s := range p.Subs {
			var tr subTrack
			end := now
			switch {
			case s.State == phase.SubDone:
				tr = t.trackOf(step, s.Key)
				end = tr.end
			case subWeight(s) < 2:
				tr = t.openTrack(step, s.Key)
			default:
				continue
			}
			if tr.holder != "" && !tr.start.IsZero() {
				ws = append(ws, moveWindow{step, s.Key, tr.holder, tr.start, end})
			}
		}
	}
	return ws
}

// placeOf is the step and sub-step key line l belongs to (key "" for the step
// alone; step -1 for the ticket as a whole). all are the log's lines, oldest
// first, for a call made between sub-steps: it belongs to the step of the
// latest line before it.
func (t *ticket) placeOf(l events.Envelope, ws []moveWindow, all []events.Envelope) (int, string) {
	if !isMove(l) {
		return stepOfLine(l), t.subKey(l)
	}
	at, _ := l.At()
	best := -1
	for i, w := range ws {
		if w.holder == l.Session && at.After(w.start) && !at.After(w.end) && (best < 0 || w.start.After(ws[best].start)) {
			best = i
		}
	}
	if best >= 0 {
		return ws[best].step, ws[best].key
	}
	step := stepBrief
	for _, x := range all {
		if isMove(x) {
			continue
		}
		if xt, ok := x.At(); ok && !xt.After(at) {
			if s := stepOfLine(x); s >= 0 {
				step = s
			}
		}
	}
	return step, ""
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
const tagMaxW = 16

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
	type entry struct {
		l    events.Envelope
		step int
		tag  string
	}
	var shown []entry
	tagW := 0
	for i := len(all) - 1; i >= 0 && len(shown) < keep; i-- {
		step, key := t.placeOf(all[i], ws, all)
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
