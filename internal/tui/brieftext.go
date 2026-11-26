package tui

import (
	"fmt"
	"strings"

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
	// or two of its body and the body whole.
	Sections []briefSection
	// Err is why the text could not be read, "" when it was.
	Err string
	// md are the sections drawn, by what they were drawn for (rendered).
	md map[mdKey][]string
}

// briefSection is a section as the verb printed it: its heading, the first line
// or two of its body, and the body whole, as the markdown it was written in.
type briefSection struct {
	Heading, Summary, Body string
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
	bodies := sectionBodies(out)
	for i, r := range doc.Tables["sections"].Maps() {
		s := briefSection{Heading: r["heading"], Summary: r["summary"]}
		if i < len(bodies) {
			s.Body = bodies[i]
		}
		b.Sections = append(b.Sections, s)
	}
	return b
}

// sectionBodies are the bodies the verb prints after its tables, one for each
// "section: <heading>" block in order: the lines of the block with their two
// spaces of indent taken off, a blank line kept, so the markdown reads as it
// was written. A block ends at the next line that is neither indented nor
// blank, so a body can hold any text, a line that looks like a block of the
// verb's own among it.
func sectionBodies(out string) []string {
	var bodies []string
	var cur []string
	open := false
	flush := func() {
		if open {
			bodies = append(bodies, strings.TrimRight(strings.Join(cur, "\n"), "\n"))
		}
		cur, open = nil, false
	}
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		switch {
		case open && (raw == "" || strings.HasPrefix(raw, "  ")):
			cur = append(cur, strings.TrimPrefix(raw, "  "))
		case strings.HasPrefix(raw, "section: "):
			flush()
			open = true
		default:
			flush()
		}
	}
	flush()
	return bodies
}

// sectionAt is the place in the text of the brief's section at position i of
// the status's list: the section at the same position when it has the same
// heading, else the first section of that heading. ok is false when the text has
// none (it was not read, or the brief changed since the status was).
func (b *briefText) sectionAt(i int, heading string) (at int, ok bool) {
	if b == nil {
		return 0, false
	}
	if i < len(b.Sections) && b.Sections[i].Heading == heading {
		return i, true
	}
	for j, s := range b.Sections {
		if s.Heading == heading {
			return j, true
		}
	}
	return 0, false
}

// mdKey tells a section's rendering: the section, the cells it was laid out in
// and whether the frame draws no color.
type mdKey struct {
	section, width int
	plain          bool
}

// rendered is section at's body drawn in lines of at most w cells, laid out
// once for the text read: a frame draws from what is built and renders nothing.
func (b *briefText) rendered(at, w int, st styles) []string {
	k := mdKey{at, w, st.plainFrame()}
	if lines, ok := b.md[k]; ok {
		return lines
	}
	lines := st.briefMarkdown(b.Sections[at].Body, w)
	if b.md == nil {
		b.md = map[mdKey][]string{}
	}
	b.md[k] = lines
	return lines
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
		out, code := run(classOpened, about{ticket: id}, args)
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
