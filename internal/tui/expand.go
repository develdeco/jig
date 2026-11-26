package tui

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

// isExpanded says whether the row ref in the pane titled pane is expanded.
func (e *navEntry) isExpanded(pane string, ref *rowRef) bool {
	if ref == nil || len(e.expanded) == 0 {
		return false
	}
	return e.expanded[expKey{pane, ref.identity()}]
}

// wholeOf are the rows that are laid out whole, for rows of refs: nil when none
// is, so a screen with nothing expanded lays out as it always did. first is the
// number of rows in the list before refs (a names row).
func (e *navEntry) wholeOf(pane string, first int, refs []*rowRef) []bool {
	if len(e.expanded) == 0 {
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

// holdsCut says whether the sel-th row of the page, with the continuation rows
// and the lines under it that belong to it, carries a cut mark.
func (p page) holdsCut(sel int) bool {
	n, in := -1, false
	for _, l := range p.lines {
		switch {
		case l.ref != nil:
			n++
			in = n == sel
			if in && len(l.cuts) > 0 {
				return true
			}
		case l.cont || l.under:
			if in && len(l.cuts) > 0 {
				return true
			}
		default:
			// Any other line ends the row.
			in = false
		}
	}
	return false
}

// paneOpen says whether the pane titled title, which has no cursor rows, is
// expanded: every cut line it has is laid out whole.
func (e *navEntry) paneOpen(title string) bool {
	return len(e.expanded) > 0 && e.expanded[expKey{pane: title}]
}

// paneExpanded says whether the pane titled title on the screen on top is
// expanded.
func (m *Model) paneExpanded(title string) bool { return m.top().paneOpen(title) }

// holdsCutIn says whether a line of the pane titled title carries a cut mark.
func (p page) holdsCutIn(title string) bool {
	for _, pn := range p.panes() {
		if pn.title != title {
			continue
		}
		for _, l := range p.lines[pn.top : pn.bottom+1] {
			if len(l.cuts) > 0 {
				return true
			}
		}
	}
	return false
}

// holder is what `space` acts on: the row under the cursor, or the focused
// pane when it has no cursor rows.
func (s subject) holder() bool { return s.hasRow || s.textPane }

// expandHolder expands the holder under the cursor, or folds it when it is
// expanded and fold is set; it says whether anything changed.
func (m *Model) expandHolder(fold bool) bool {
	sub := m.subjectNow()
	if !sub.holder() || !(sub.cut || sub.expanded) {
		return false
	}
	e := m.top()
	if e.expanded == nil {
		e.expanded = map[expKey]bool{}
	}
	k := expKey{pane: e.paneFocus}
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
	for i, l := range lines {
		line := pre + style(l)
		if i == 0 {
			line = first + style(l)
		}
		p.lines = append(p.lines, pline{text: line, box: boxMid, under: true})
		if cut && i == len(lines)-1 {
			p.lines[len(p.lines)-1].cuts = []cutMark{{x: width(line) - 1, value: text}}
		}
	}
}
