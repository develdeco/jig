package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// The brief's words are not in the event stream and not in `jig status`, which
// names its sections and no more. The brief screen reads them from `jig brief
// show --text`, once when it opens, and keeps what it read until a line about
// the brief arrives (an approval, a check of the slices) or the person
// refreshes: the brief is the person's file and only they and the planner edit
// it, so the screens read it as little as they can.

// briefText is the brief as `jig brief show --text` printed it.
type briefText struct {
	// Title is the brief's own title, "" when it has none.
	Title string
	// Illustration is whether the brief has an illustration beside it.
	Illustration bool
	// Sections are the brief's "## " sections in order, each with the first line
	// or two of its body.
	Sections []briefSection
	// Err is why the text could not be read, "" when it was.
	Err string
}

type briefSection struct {
	Heading, Summary string
}

// parseBriefText reads what `jig brief show --text` printed and the code it
// exited with. The section bodies the verb prints after its tables are indented
// blocks of their own, which the reader of the tables passes over.
func parseBriefText(out string, code int) briefText {
	doc := parseAxi(out)
	if doc.Err != nil {
		return briefText{Err: doc.Err.Msg}
	}
	if code != 0 {
		return briefText{Err: fmt.Sprintf("jig brief show exited %d with %s", code, firstWords(out))}
	}
	var b briefText
	if kv, ok := doc.KV["brief"]; ok {
		b.Title, b.Illustration = kv["title"], kv["illustration"] == "yes"
	}
	for _, r := range doc.Tables["sections"].Maps() {
		b.Sections = append(b.Sections, briefSection{Heading: r["heading"], Summary: r["summary"]})
	}
	return b
}

// summaryOf is the first lines of the brief's section at position i of the
// status's list: the section of the text at the same position when it has the
// same heading, else the first section of that heading. "" when the text has
// none (it was not read, or the brief changed since the status was).
func (b *briefText) summaryOf(i int, heading string) string {
	if b == nil {
		return ""
	}
	if i < len(b.Sections) && b.Sections[i].Heading == heading {
		return b.Sections[i].Summary
	}
	for _, s := range b.Sections {
		if s.Heading == heading {
			return s.Summary
		}
	}
	return ""
}

// briefMsg is a ticket's brief text, read.
type briefMsg struct {
	ticket string
	text   briefText
}

// loadBrief reads the ticket's brief text when the screens hold none (or it has
// gone stale), one read at a time: a brief line that arrives while a read is out
// reads again when it lands.
func (m *Model) loadBrief(id string) tea.Cmd {
	t := m.w.tickets[id]
	if t == nil || m.run == nil || t.awaiting {
		return nil
	}
	if t.brief != nil && !t.briefStale {
		return nil
	}
	if t.briefLoading {
		t.briefStale = true
		return nil
	}
	t.briefLoading, t.briefStale = true, false
	args := m.verbArgs("brief", "show", id, "--text")
	run := m.run
	return func() tea.Msg {
		out, code := run(args)
		return briefMsg{ticket: id, text: parseBriefText(out, code)}
	}
}

// onBrief takes a brief text in, and reads again when the brief changed while
// it was being read and the person is looking at it.
func (m *Model) onBrief(msg briefMsg) tea.Cmd {
	t := m.w.tickets[msg.ticket]
	if t == nil {
		return nil
	}
	t.briefLoading = false
	text := msg.text
	t.brief = &text
	if t.briefStale && m.top().screen == screenBrief && m.top().ticket == msg.ticket {
		return m.loadBrief(msg.ticket)
	}
	return nil
}
