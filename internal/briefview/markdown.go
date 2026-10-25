package briefview

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

var (
	headingLine  = regexp.MustCompile(`^(#{1,6})[ \t]+(.+?)[ \t]*$`)
	listItemLine = regexp.MustCompile(`^([ \t]*)([-*+]|\d+[.)])[ \t]+(.*)$`)
	boldSpan     = regexp.MustCompile(`\*\*(.+?)\*\*`)
	linkSpan     = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\s)]+)\)`)
)

// markdownHTML renders the small part of markdown a brief uses as HTML:
// headings, paragraphs, nested lists, quotes, fenced code and pipe tables
// (shown as written), with code spans, bold and http(s) links inside text.
// Everything else is text. All of it is escaped, so the result holds no
// markup the brief did not get from these rules.
func markdownHTML(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var b strings.Builder
	var para []string
	var lists listStack

	flushPara := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + inline(strings.Join(para, " ")) + "</p>\n")
			para = nil
		}
	}
	closeAll := func() {
		flushPara()
		lists.closeAll(&b)
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		indented := line != strings.TrimLeft(line, " \t")
		switch {
		case trimmed == "":
			closeAll()
		case strings.HasPrefix(trimmed, "```"), strings.HasPrefix(trimmed, "~~~"):
			closeAll()
			fence := trimmed[:3]
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence); i++ {
				code = append(code, lines[i])
			}
			b.WriteString("<pre><code>" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>\n")
		case !indented && headingLine.MatchString(trimmed):
			closeAll()
			m := headingLine.FindStringSubmatch(trimmed)
			tag := "h" + strconv.Itoa(len(m[1]))
			b.WriteString("<" + tag + ">" + inline(m[2]) + "</" + tag + ">\n")
		case strings.HasPrefix(trimmed, "|"):
			closeAll()
			table := []string{line}
			for i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "|") {
				i++
				table = append(table, lines[i])
			}
			b.WriteString("<pre>" + html.EscapeString(strings.Join(table, "\n")) + "</pre>\n")
		case strings.HasPrefix(trimmed, ">"):
			closeAll()
			var quoted []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				quoted = append(quoted, strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">"), " "))
			}
			i--
			b.WriteString("<blockquote>\n" + markdownHTML(strings.Join(quoted, "\n")) + "</blockquote>\n")
		case listItemLine.MatchString(line):
			flushPara()
			m := listItemLine.FindStringSubmatch(line)
			lists.item(&b, indentWidth(m[1]), m[2][0] >= '0' && m[2][0] <= '9', inline(m[3]))
		case indented && lists.isOpen():
			// An indented line after an item goes on that item.
			b.WriteString(" " + inline(trimmed))
		default:
			lists.closeAll(&b)
			para = append(para, trimmed)
		}
	}
	closeAll()
	return b.String()
}

// listStack is the lists open while rendering, outermost first. Each has an
// <li> open until its next item or its end, so a deeper list can sit inside
// the item above it.
type listStack struct {
	open []openList
}

type openList struct {
	indent  int
	ordered bool
}

func (l *listStack) isOpen() bool { return len(l.open) > 0 }

// item writes an item at the given indent: it closes the lists that are
// deeper, ends the item before it at that indent, or opens a list inside the
// item above when the indent is deeper than any open.
func (l *listStack) item(b *strings.Builder, indent int, ordered bool, text string) {
	for n := len(l.open); n > 0 && l.open[n-1].indent > indent; n = len(l.open) {
		l.closeTop(b)
	}
	if n := len(l.open); n > 0 && l.open[n-1].indent == indent {
		b.WriteString("</li>\n")
	} else {
		tag := "ul"
		if ordered {
			tag = "ol"
		}
		b.WriteString("<" + tag + ">\n")
		l.open = append(l.open, openList{indent: indent, ordered: ordered})
	}
	b.WriteString("<li>" + text)
}

// closeTop ends the innermost list and the item it was written in.
func (l *listStack) closeTop(b *strings.Builder) {
	top := l.open[len(l.open)-1]
	l.open = l.open[:len(l.open)-1]
	tag := "ul"
	if top.ordered {
		tag = "ol"
	}
	b.WriteString("</li>\n</" + tag + ">\n")
}

// closeAll ends every open list.
func (l *listStack) closeAll(b *strings.Builder) {
	for len(l.open) > 0 {
		l.closeTop(b)
	}
}

// indentWidth is the width of leading white space, a tab counting as four.
func indentWidth(s string) int {
	return len(strings.ReplaceAll(s, "\t", "    "))
}

// inline renders a run of text: code spans as code, and in the rest bold and
// http(s) links, with everything escaped.
func inline(s string) string {
	parts := strings.Split(s, "`")
	if len(parts)%2 == 0 {
		// A backtick that never closes is text.
		parts = []string{s}
	}
	var b strings.Builder
	for i, part := range parts {
		if len(parts) > 1 && i%2 == 1 {
			b.WriteString("<code>" + html.EscapeString(part) + "</code>")
			continue
		}
		esc := html.EscapeString(part)
		esc = boldSpan.ReplaceAllString(esc, "<strong>$1</strong>")
		esc = linkSpan.ReplaceAllString(esc, `<a href="$2" rel="noopener noreferrer">$1</a>`)
		b.WriteString(esc)
	}
	return b.String()
}
