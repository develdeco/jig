package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// The brief's words are markdown, and the browser page (internal/briefview)
// draws the small part of it a brief uses: headings, paragraphs, nested lists,
// quotes, fenced code, pipe tables as written, code spans, bold and http(s)
// links. This is the same part drawn for a terminal, as lines of at most a width
// of cells, so a section's text reads the same on both and nothing of it is cut.
// Anything else is text.

var (
	mdHeading = regexp.MustCompile(`^(#{1,6})[ \t]+(.+?)[ \t]*$`)
	mdItem    = regexp.MustCompile(`^([ \t]*)([-*+]|\d+[.)])[ \t]+(.*)$`)
	mdLink    = regexp.MustCompile(`^\[([^\]]+)\]\((https?://[^\s)]+)\)`)
)

// bullet is the glyph an unordered item starts with.
const bullet = "•"

// plainFrame is whether s draws no color: a pipe or NO_COLOR, where bold shows
// as its bare words and so a code span keeps its backticks to be told by.
func (s styles) plainFrame() bool { return s.r.ColorProfile() == termenv.Ascii }

// briefMarkdown is md drawn in lines of at most w cells. Bold is the bold style
// and a code span the key style; in a plain frame a code span keeps its
// backticks. A bullet is "•" with its depth, a numbered item keeps its number, a
// fence's lines stand indented with the fence lines dropped, a quote has a bar
// and a link reads "text (address)".
func (s styles) briefMarkdown(md string, w int) []string {
	w = max(1, w)
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out []string
	var para []string
	var items listDepths
	var item *mdItemText // the item the lines that follow it go on
	// gap puts one blank line before a block, none at the top and none between
	// the items of a list.
	gap := func() {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}
	flushItem := func() {
		if item != nil {
			out = append(out, s.hanging(item.prefix, item.text, w)...)
			item = nil
		}
	}
	flushPara := func() {
		if len(para) > 0 {
			gap()
			out = append(out, s.hanging("", strings.Join(para, " "), w)...)
			para = nil
		}
	}
	closeAll := func() {
		flushItem()
		flushPara()
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		indented := line != strings.TrimLeft(line, " \t")
		switch {
		case trimmed == "":
			// A blank line ends the lists, as it does on the browser page.
			closeAll()
			items.reset()
		case strings.HasPrefix(trimmed, "```"), strings.HasPrefix(trimmed, "~~~"):
			closeAll()
			items.reset()
			fence := trimmed[:3]
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence); i++ {
				code = append(code, lines[i])
			}
			gap()
			for _, c := range code {
				out = append(out, wrapCells("  ", c, w)...)
			}
		case !indented && mdHeading.MatchString(trimmed):
			closeAll()
			items.reset()
			gap()
			m := mdHeading.FindStringSubmatch(trimmed)
			out = append(out, s.hangingBold("", m[2], w)...)
		case strings.HasPrefix(trimmed, "|"):
			closeAll()
			items.reset()
			gap()
			out = append(out, wrapCells("", line, w)...)
			for i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "|") {
				i++
				out = append(out, wrapCells("", lines[i], w)...)
			}
		case strings.HasPrefix(trimmed, ">"):
			closeAll()
			items.reset()
			var quoted []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				quoted = append(quoted, strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">"), " "))
			}
			i--
			gap()
			bar := s.dim("│") + " "
			for _, q := range s.briefMarkdown(strings.Join(quoted, "\n"), max(1, w-2)) {
				if q == "" {
					out = append(out, s.dim("│"))
					continue
				}
				out = append(out, bar+q)
			}
		case mdItem.MatchString(line):
			flushItem()
			flushPara()
			m := mdItem.FindStringSubmatch(line)
			if !items.open() {
				gap()
			}
			depth := items.at(indentCells(m[1]))
			marker := bullet
			if m[2][0] >= '0' && m[2][0] <= '9' {
				marker = m[2]
			}
			item = &mdItemText{prefix: spaces(2*depth) + marker + " ", text: m[3]}
		case indented && item != nil:
			// An indented line after an item goes on that item.
			item.text += " " + trimmed
		default:
			flushItem()
			items.reset()
			para = append(para, trimmed)
		}
	}
	closeAll()
	return out
}

// mdItemText is a list item being read: the lines after it that are indented go
// on its text.
type mdItemText struct {
	prefix, text string
}

// listDepths tracks the indents of the lists open, as the browser page's lists
// are: an item deeper than any open starts a list inside the one above.
type listDepths struct {
	indents []int
}

func (l *listDepths) open() bool { return len(l.indents) > 0 }

func (l *listDepths) reset() { l.indents = nil }

// at is the depth of an item indented by n cells: lists deeper than n close, and
// one deeper than any open opens.
func (l *listDepths) at(n int) int {
	for len(l.indents) > 0 && l.indents[len(l.indents)-1] > n {
		l.indents = l.indents[:len(l.indents)-1]
	}
	if len(l.indents) == 0 || l.indents[len(l.indents)-1] < n {
		l.indents = append(l.indents, n)
	}
	return len(l.indents) - 1
}

// indentCells is the width of leading white space, a tab counting as four.
func indentCells(s string) int { return len(strings.ReplaceAll(s, "\t", "    ")) }

// hanging is text drawn in lines of at most w cells, the first behind prefix and
// the others under it.
func (s styles) hanging(prefix, text string, w int) []string {
	return hangLines(prefix, s.inline(text, false), w)
}

// hangingBold is the same, the whole text bold: a heading.
func (s styles) hangingBold(prefix, text string, w int) []string {
	return hangLines(prefix, s.inline(text, true), w)
}

// hangLines breaks words into lines behind prefix, the lines after the first
// standing under its text.
func hangLines(prefix, words string, w int) []string {
	room := max(1, w-width(prefix))
	var out []string
	for i, l := range wordLines(words, room) {
		if i == 0 {
			out = append(out, prefix+l)
		} else {
			out = append(out, spaces(width(prefix))+l)
		}
	}
	if len(out) == 0 {
		out = append(out, strings.TrimRight(prefix, " "))
	}
	return out
}

// wrapCells is line cut into lines of at most w cells behind prefix, as it was
// written: a table row or a line of code keeps its spaces.
func wrapCells(prefix, line string, w int) []string {
	room := max(1, w-width(prefix))
	line = expandTabs(strings.TrimRight(line, " \t"))
	if width(line) <= room {
		return []string{prefix + line}
	}
	var out []string
	for _, l := range strings.Split(ansi.Hardwrap(line, room, true), "\n") {
		out = append(out, prefix+l)
	}
	return out
}

// expandTabs is line with each tab as the spaces to the next stop four cells on:
// a tab counts no cell where it is measured and a terminal draws it as several,
// which would push the right edge of the box out.
func expandTabs(line string) string {
	if !strings.Contains(line, "\t") {
		return line
	}
	var b strings.Builder
	col := 0
	for _, r := range line {
		if r == '\t' {
			n := 4 - col%4
			b.WriteString(spaces(n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += width(string(r))
	}
	return b.String()
}

// inline is text with its bold, code spans and links drawn, a single space
// between its words. A code span in a plain frame keeps its backticks, since
// nothing else would mark it. bold draws every word bold.
func (s styles) inline(text string, bold bool) string {
	var words []string
	cur := ""
	flush := func() {
		if cur != "" {
			words = append(words, cur)
			cur = ""
		}
	}
	// put adds run to the word being built, the spaces in it ending words, each
	// word drawn by draw on its own so no style runs over a space.
	put := func(run string, draw func(string) string) {
		for _, part := range splitSpaces(run) {
			if part == " " {
				flush()
			} else {
				cur += draw(part)
			}
		}
	}
	plain := func(t string) string { return t }
	var walk func(t string, draw func(string) string)
	walk = func(t string, draw func(string) string) {
		for len(t) > 0 {
			switch {
			case t[0] == '`':
				if end := strings.IndexByte(t[1:], '`'); end >= 0 {
					code := t[1 : 1+end]
					if s.plainFrame() {
						code = "`" + code + "`"
					}
					put(code, s.key)
					t = t[end+2:]
					continue
				}
			case strings.HasPrefix(t, "**"):
				if end := strings.Index(t[2:], "**"); end > 0 {
					walk(t[2:2+end], s.bold)
					t = t[end+4:]
					continue
				}
			case t[0] == '[':
				if m := mdLink.FindStringSubmatch(t); m != nil {
					walk(m[1], draw)
					put(" ("+m[2]+")", draw)
					t = t[len(m[0]):]
					continue
				}
			}
			// Plain text runs to the next mark.
			n := 1
			for n < len(t) && t[n] != '`' && t[n] != '*' && t[n] != '[' {
				n++
			}
			put(t[:n], draw)
			t = t[n:]
		}
	}
	if bold {
		walk(text, s.bold)
	} else {
		walk(text, plain)
	}
	flush()
	return strings.Join(words, " ")
}

// splitSpaces is run in its words and the single " " of each run of white
// space between them (and at its ends), so a caller can end a word at a space.
func splitSpaces(run string) []string {
	var out []string
	cur := ""
	space := false
	for _, r := range run {
		if r == ' ' || r == '\t' || r == '\n' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			if !space {
				out = append(out, " ")
			}
			space = true
			continue
		}
		space = false
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
