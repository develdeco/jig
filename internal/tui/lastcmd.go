package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The last command is a holder of its own, held by the screen whatever pane has
// the focus: its line ends in a cut mark when the screen is too narrow for it,
// and `space` (when the focus holds nothing cut) or a click on the mark wraps it
// whole under its own column, in the rows the screen can spare while the body
// keeps its minimum. It folds with `space` and goes with the line itself.

// lastView is the last command's rows as the panel draws them: the rows, which
// of them ends in a cut mark (-1 for none), and the whole text the cut stands
// for.
type lastView struct {
	lines []string
	cut   int
	value string
}

// lastViewOf is the panel's rows for the command o in w cells: the one line,
// cut when it is wider than the screen, and for a verb that leaves the next move
// to someone else the help line it printed beneath the command, wrapped. Whole,
// the rows are as many as the words need, the said text wrapped under its own
// column.
func (m *Model) lastViewOf(o *outcome, w int, whole bool) lastView {
	s := o.shown()
	cmd := m.shownLine(s.Args)
	if o.leavesTheMove() && s.Code == 0 {
		if help, ok := firstHelp(s.Out); ok {
			v := lastView{cut: -1}
			first := "  " + m.st.icon(StateOK) + " " + m.st.bold(cmd)
			v.lines = []string{fit(first, w)}
			if width(first) > w {
				v.cut, v.value = 0, cmd+" "+help
			}
			most := helpWrapRows
			if whole {
				most = wholeRows
			}
			rows, cut := wrapCut(help, w-4, most)
			for i, l := range rows {
				v.lines = append(v.lines, "    "+m.st.dim(l))
				if cut && i == len(rows)-1 {
					v.cut, v.value = len(v.lines)-1, cmd+" "+help
				}
			}
			return v
		}
	}
	state, said := StateOK, firstSentence(s.Out)
	if s.Code != 0 {
		state, said = StateFail, errorSentence(s.Out)
	}
	head := "  " + m.st.icon(state) + " " + m.st.bold(cmd)
	if said != "" {
		if state == StateFail {
			said = m.st.state(StateFail, said)
		} else {
			said = m.st.dim(said)
		}
	}
	line := head
	if said != "" {
		line += "  " + said
	}
	value := ansi.Strip(line)
	if width(line) <= w {
		return lastView{lines: []string{line}, cut: -1}
	}
	if !whole {
		return lastView{lines: []string{fit(line, w)}, cut: 0, value: strings.TrimSpace(value)}
	}
	// The said text wraps under its own column, the one after the command; a
	// command so long that the column has no room puts it under an indent.
	col := width(head) + 2
	if said == "" || col+10 > w {
		col = 4
	}
	if said == "" {
		said = head
		head, col = "", 2
	}
	rows := wrapCell(said, max(1, w-col), wholeRows)
	v := lastView{cut: -1}
	switch {
	case head == "":
		for _, l := range rows {
			v.lines = append(v.lines, spaces(col)+l)
		}
	case col == 4:
		v.lines = append(v.lines, fit(head, w))
		for _, l := range rows {
			v.lines = append(v.lines, spaces(col)+l)
		}
	default:
		for i, l := range rows {
			if i == 0 {
				v.lines = append(v.lines, head+"  "+l)
				continue
			}
			v.lines = append(v.lines, spaces(col)+l)
		}
	}
	return v
}

// lastRoom is the rows the screen can spare for the last command: what the
// header, the keys and the notice leave once the body has its minimum.
func (m *Model) lastRoom(w int) int {
	notice := 0
	if m.notice != "" {
		notice = len(wrapWhole(m.notice, w-2))
	}
	return m.height - len(m.header(w)) - len(m.footer(w)) - minBodyRows - notice
}

// lastShown is the last command as the panel draws it now, and whether it is
// whole: whole when the person expanded it and the screen has the rows, else the
// line as the screen's width allows.
func (m *Model) lastShown(w int) (lastView, bool) {
	if m.lastOpen != nil && m.lastOpen == m.last {
		if v := m.lastViewOf(m.last, w, true); len(v.lines) <= m.lastRoom(w) {
			return v, true
		}
	}
	return m.lastViewOf(m.last, w, false), false
}

// lastState says whether the last command line is cut and whether it is
// expanded, as the keys read them.
func (m *Model) lastState() (cut, expanded bool) {
	if m.last == nil || m.width < 1 {
		return false, false
	}
	// What is cut follows the width alone, and what is open is the person's: the
	// keys never read the rows the screen has to spare.
	v := m.lastViewOf(m.last, m.width, false)
	return v.cut >= 0, m.lastOpen == m.last
}

// toggleLast expands the last command, or folds it when it is expanded and fold
// is set. A line that needs more rows than the screen can spare stays as it is,
// and the notice says how many it needs and how many the screen has.
func (m *Model) toggleLast(fold bool) bool {
	if m.last == nil {
		return false
	}
	w := m.width
	if _, whole := m.lastShown(w); whole {
		if !fold {
			return false
		}
		m.lastOpen = nil
		return true
	}
	if m.lastViewOf(m.last, w, false).cut < 0 {
		return false
	}
	need, room := len(m.lastViewOf(m.last, w, true).lines), m.lastRoom(w)
	if need > room {
		m.notice = fmt.Sprintf("the whole line needs %d rows, this screen has %d", need, max(0, room))
		return false
	}
	m.lastOpen = m.last
	return true
}

// lastMarkAt is the cell the last frame drew the last command's cut mark on.
type lastMarkAt struct{ y, x int }
