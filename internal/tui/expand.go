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
// that belong to it, carries a cut mark.
func (p page) holdsCut(sel int) bool {
	n := -1
	for _, l := range p.lines {
		switch {
		case l.ref != nil:
			n++
			if n == sel && len(l.cuts) > 0 {
				return true
			}
		case l.cont && n == sel && len(l.cuts) > 0:
			return true
		}
	}
	return false
}

// expandHolder expands the holder under the cursor, or folds it when it is
// expanded and fold is set; it says whether anything changed.
func (m *Model) expandHolder(fold bool) bool {
	sub := m.subjectNow()
	if !sub.hasRow || !(sub.cut || sub.expanded) {
		return false
	}
	e := m.top()
	if e.expanded == nil {
		e.expanded = map[expKey]bool{}
	}
	k := expKey{e.paneFocus, sub.row.identity()}
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
