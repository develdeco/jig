package tui

import "strings"

// The illustration is a part of a brief that has one: enter on its row serves it
// in the browser (Config.Viewer) and sends its address to the opener (Config.Open).

// illustrationRow is the Parts row of t's illustration.
func (m *Model) illustrationRow(t *ticket) listRow {
	stands := m.st.dim("not served yet")
	if servedAddress(m.served[t.id]) {
		stands = m.st.state(StateOK, "served")
	}
	if s := t.status.Brief.Illustration; s == "changed" || s == "added" {
		stands += " · " + m.st.state(StateFindings, s)
	}
	return listRow{ref: &rowRef{kind: rowIllustration, id: "illustration", ticket: t.id}, cells: []string{"illustration", "the brief's illustration", stands}}
}

// servedAddress says addr is an address a browser can reach, not the path the
// viewer names in a session whose browser cannot.
func servedAddress(addr string) bool {
	return strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://")
}

// illustrationDetail is the detail of the illustration: where it stands, whether
// it changed since the approval, and what enter does.
func (m *Model) illustrationDetail(t *ticket) detailSpec {
	d := detailSpec{title: "illustration", summary: "not served yet"}
	add := func(r detailRow) { d.rows = append(d.rows, r) }
	if addr := m.served[t.id]; servedAddress(addr) {
		d.summary = "served"
		add(detailRow{label: "served", text: addr})
	} else if addr != "" {
		// A path is where the file is: nothing listens for a browser.
		add(detailRow{label: "served", text: "not served: the file is at " + addr, dim: true})
	} else {
		add(detailRow{label: "served", text: "not yet: enter serves it in the browser", dim: true})
	}
	b := t.status.Brief
	if b.Approval == "changed" {
		switch b.Illustration {
		case "same":
			add(detailRow{label: "approval", text: "the illustration is as it was approved"})
		case "changed":
			add(detailRow{label: "approval", text: "the illustration changed since the approval", state: StateFindings})
		case "added":
			add(detailRow{label: "approval", text: "the illustration was added since the approval", state: StateFindings})
		case "":
			add(detailRow{label: "approval", text: "whether the illustration changed was not recorded for this approval", dim: true})
		}
	}
	return d
}

// openIllustration is enter on the illustration row: the viewer serves the
// ticket's illustration (once; the address is kept) and the opener gets the
// address. With no opener the notice carries the address, and a refused open says
// why and carries it too; an address that is a path (a session whose browser
// cannot reach the viewer) is only named.
func (m *Model) openIllustration(ticket string) {
	addr := m.served[ticket]
	if addr == "" {
		if m.cfg.Viewer == nil {
			m.notice = "this program serves no illustration: `jig brief show " + ticket + "` does"
			return
		}
		var err error
		if addr, err = m.cfg.Viewer(ticket); err != nil {
			m.notice = "could not serve the illustration: " + err.Error()
			return
		}
		if m.served == nil {
			m.served = map[string]string{}
		}
		m.served[ticket] = addr
	}
	switch {
	case !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://"):
		m.notice = "the illustration is at " + addr
	case m.cfg.Open == nil:
		m.notice = "the illustration: " + addr
	default:
		if err := m.cfg.Open(addr); err != nil {
			m.notice = "could not open the illustration: " + err.Error() + ": " + addr
			return
		}
		m.notice = "opened the illustration: " + addr
	}
}
