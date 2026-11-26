package tui

import (
	"sort"
	"strings"
)

// A cut the TUI makes leaves its "…" as a mark that belongs to a holder: the row
// whose cell was cut. `space`, or a click on the mark, lays the holder's cut
// cells out whole where it stands, and `space` again folds it back. What is
// expanded is kept by the holder's identity, not by its lines, so it survives a
// line arriving above it, a resize and the zoom, and it ends with its screen.

// expKey names an expanded holder: the pane it is in ("" for the project's list)
// and the identity of its row.
type expKey struct {
	pane string
	id   rowIdentity
}

// expansionKey names what the screen has expanded, for a cache of lines laid
// out under it: the folded layout and each change of the expansions are told
// apart.
func (e *navEntry) expansionKey() string {
	if len(e.expanded) == 0 {
		return ""
	}
	if e.suppress {
		return "folded"
	}
	// What is expanded, not a count of changes: a count restarts with each visit
	// to a screen and would name another visit's lines.
	keys := make([]string, 0, len(e.expanded))
	for k := range e.expanded {
		keys = append(keys, k.pane+"\x00"+k.id.id+"\x00"+k.id.container+"\x00"+k.id.kind)
	}
	sort.Strings(keys)
	return strings.Join(keys, "\x01")
}

// isExpanded says whether the row ref in the pane titled pane is expanded.
func (e *navEntry) isExpanded(pane string, ref *rowRef) bool {
	if ref == nil || len(e.expanded) == 0 || e.suppress {
		return false
	}
	return e.expanded[expKey{pane, ref.identity()}]
}

// wholeOf are the rows that are laid out whole, for rows of refs: nil when none
// is, so a screen with nothing expanded lays out as it always did. first is the
// number of rows in the list before refs (a names row).
func (e *navEntry) wholeOf(pane string, first int, refs []*rowRef) []bool {
	if len(e.expanded) == 0 || e.suppress {
		return nil
	}
	var whole []bool
	for i, r := range refs {
		if e.isExpanded(pane, r) {
			if whole == nil {
				whole = make([]bool, first+len(refs))
			}
			whole[first+i] = true
		}
	}
	return whole
}

// paneTitle is the title of the panel the page is adding to: the last top edge.
func (p page) paneTitle() string {
	for i := len(p.lines) - 1; i >= 0; i-- {
		if p.lines[i].box == boxTop {
			return p.lines[i].text
		}
	}
	return ""
}

// holderNow is the holder the next line added to the panel p stands for: the
// row ref when the cursor can stand on it, else the holder of the line above in
// the panel, else the panel itself. A line outside every panel has none.
func (p *page) holderNow(ref *rowRef) (expKey, bool) {
	pane := p.paneTitle()
	if ref != nil {
		return expKey{pane: pane, id: ref.identity()}, true
	}
	for i := len(p.lines) - 1; i >= 0; i-- {
		l := p.lines[i]
		if l.box == boxTop {
			return expKey{pane: l.text}, true
		}
		if l.box == boxNone || l.box == boxBottom {
			return expKey{}, false
		}
		if l.held {
			return l.holder, true
		}
		if l.ref != nil {
			return expKey{pane: pane, id: l.ref.identity()}, true
		}
	}
	return expKey{}, false
}

// holderIn is the holder the line stands for, in the pane titled pane: the stamp
// it carries, else its row's, else the pane's own for a line inside it.
func (l pline) holderIn(pane string) (expKey, bool) {
	switch {
	case l.held:
		return l.holder, true
	case l.ref != nil:
		return expKey{pane: pane, id: l.ref.identity()}, true
	case l.box == boxMid && !l.names:
		return expKey{pane: pane}, true
	}
	return expKey{}, false
}

// holdsCut says whether a line the holder k stands for carries a cut mark. A
// holder is found by its stamp: the place among the rows moves when a list counts
// the ones it leaves out.
func (p page) holdsCut(k expKey) bool {
	pane := ""
	for _, l := range p.lines {
		if l.box == boxTop {
			pane = l.text
		}
		if len(l.cuts) == 0 {
			continue
		}
		if h, ok := l.holderIn(pane); ok && h == k {
			return true
		}
	}
	return false
}

// holderOfSel is the holder of the sel-th row of the page, whether a line carries
// it or it stands in a count of the rows left out.
func (p page) holderOfSel(sel int) (expKey, bool) {
	pane, n := "", 0
	for _, l := range p.lines {
		if l.box == boxTop {
			pane = l.text
		}
		if sel >= n && sel < n+len(l.hiddenRefs) {
			return expKey{pane: pane, id: l.hiddenRefs[sel-n].identity()}, true
		}
		n += len(l.hiddenRefs)
		if l.ref != nil {
			if n == sel {
				return l.holderIn(pane)
			}
			n++
		}
	}
	return expKey{}, false
}

// paneOpen says whether the pane titled title, which has no cursor rows, is
// expanded: every cut line it has is laid out whole.
func (e *navEntry) paneOpen(title string) bool {
	return len(e.expanded) > 0 && !e.suppress && e.expanded[expKey{pane: title}]
}

// paneExpanded says whether the pane titled title on the screen on top is
// expanded.
func (m *Model) paneExpanded(title string) bool { return m.top().paneOpen(title) }

// holder is what `space` acts on: the row under the cursor, or the focused
// pane when it has no cursor rows.
func (s subject) holder() bool { return s.hasRow || s.textPane }

// holds says the focus has a holder that holds a cut or is expanded: `space`
// acts on it before it reaches the last command line.
func (s subject) holds() bool { return s.holder() && (s.cut || s.expanded) }

// expandHolder expands the holder under the cursor, or folds it when it is
// expanded and fold is set; it says whether anything changed.
func (m *Model) expandHolder(fold bool) bool {
	sub := m.subjectNow()
	if !sub.holds() {
		// A row's own cut comes first; with none, `space` reaches the screen's last
		// command. A click that found no holder does nothing.
		return fold && m.toggleLast(fold)
	}
	e := m.top()
	if e.expanded == nil {
		e.expanded = map[expKey]bool{}
	}
	k := expKey{pane: e.paneFocus}
	if sub.hasRow {
		k.pane = sub.pane
	}
	if sub.hasRow {
		k.id = sub.row.identity()
	}
	switch {
	case e.expanded[k] && fold:
		delete(e.expanded, k)
	case !e.expanded[k]:
		e.expanded[k] = true
	default:
		return false
	}
	// The row is read where it stands, from its first line.
	e.lead, e.rowScroll = 0, 0
	m.expandMoved()
	return true
}

// expandMoved says that what is expanded moved: the pages built from it are laid
// out again, from the rows already built.
func (m *Model) expandMoved() {
	m.top().expGen++
	m.shown = nil
	// A page that changed because the person folded or opened a holder has no new
	// lines in it: the reader's place is not held against the last frame.
	m.seen = nil
}

// underWrap adds text to the panel p as lines under the row of owner (nil for a
// pane with no cursor rows, whose own they are), the first led by first and the
// rest by pre, wrapped to most rows of w cells and drawn by style. A holder the
// person has expanded is given every row it needs; a cut registers its mark with
// the whole text.
func (m *Model) underWrap(p *page, owner *rowRef, first, pre, text string, w, most int, style func(string) string) {
	e, pane := m.top(), p.paneTitle()
	if owner != nil && e.isExpanded(pane, owner) || owner == nil && e.paneOpen(pane) {
		most = wholeRows
	}
	lines, cut := wrapCut(text, w, most)
	holder, held := p.holderNow(owner)
	for i, l := range lines {
		line := pre + style(l)
		if i == 0 {
			line = first + style(l)
		}
		p.lines = append(p.lines, pline{text: line, box: boxMid, under: true, holder: holder, held: held})
		if cut && i == len(lines)-1 {
			p.lines[len(p.lines)-1].cuts = []cutMark{{x: width(line) - 1, value: text}}
		}
	}
}
