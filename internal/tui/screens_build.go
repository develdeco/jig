package tui

import "strconv"

// buildPage is a ticket's slices: each with its state, how many tries it has
// had, what blocks it and the latest line about it.
func (m *Model) buildPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	slices := t.slices(m.zone)
	if len(slices) == 0 {
		p.add(m.st.dim("no slices yet"))
		if t.status == nil {
			p.add(m.st.dim("reading the ticket's status"))
		}
		return p
	}

	idW, stateW, blockedW := 5, 5, 7
	for _, s := range slices {
		idW = max(idW, width(s.ID))
		stateW = max(stateW, width(s.State))
		blockedW = max(blockedW, width(blockedCell(s)))
	}
	p.add(m.st.dim("  " + pad("slice", idW) + "  " + pad("state", stateW) + "  try  " + pad("blocked", blockedW) + "  latest"))
	for _, s := range slices {
		latest := ""
		if s.Last != "" {
			latest = m.st.dim(s.LastAt) + " " + m.st.state(s.LastState, s.Last)
		}
		line := m.st.icon(s.State) + " " + pad(s.ID, idW) + "  " + m.st.state(s.State, pad(s.State, stateW)) +
			"  " + pad(strconv.Itoa(s.Attempts), 3) + "  " + pad(blockedCell(s), blockedW) + "  " + latest
		p.addRow(rowRef{kind: rowSlice, id: s.ID, ticket: id, slice: s.ID}, line)
	}
	return p
}

// blockedCell is what holds a slice up: the slices it waits for, else the
// question it is parked on.
func blockedCell(s sliceRow) string {
	switch {
	case s.BlockedBy != "":
		return s.BlockedBy
	case s.Question != "":
		return s.Question
	}
	return "-"
}
