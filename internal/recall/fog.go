package recall

import (
	"strings"
)

// Fog is what a chart's map.md says is not yet specified: the items of its fog
// section. More counts the items left out because the section is longer than
// MaxFogItems.
type Fog struct {
	Chart string   `json:"chart"`
	Items []string `json:"items"`
	More  int      `json:"more,omitempty"`
}

// The caps that keep a report short enough to read before a grilling round.
const (
	// MaxFogItems is the most fog items one chart contributes.
	MaxFogItems = 30
	// MaxItemRunes is the longest an item, a document line or an answer shows.
	MaxItemRunes = 300
)

// FogItems returns the items of the fog section of a chart's map.md: the
// first heading (at any level) that reads "Not yet specified ..." or names the
// fog, up to the next heading of the same or a higher level. A list item, or
// a paragraph that is not a list, is one item; a heading inside a code fence
// is not a heading. A map with no such section has no fog.
func FogItems(mapMD string) []string {
	lines := strings.Split(strings.ReplaceAll(mapMD, "\r\n", "\n"), "\n")
	start, level, end := -1, 0, len(lines)
	fence := false
	for i, line := range lines {
		if isFence(line) {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		lvl, text := headingOf(line)
		if lvl == 0 {
			continue
		}
		if start >= 0 {
			if lvl <= level {
				end = i
				break
			}
			continue
		}
		if isFogHeading(text) {
			start, level = i+1, lvl
		}
	}
	if start < 0 {
		return nil
	}
	return itemsOf(lines[start:end])
}

// isFence reports whether line opens or closes a fenced code block.
func isFence(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// headingOf reads an ATX heading: its level (the number of leading #, 1 to
// 6) and its text, or level 0 when line is not one.
func headingOf(line string) (int, string) {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 {
		return 0, ""
	}
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(t) || t[n] != ' ' {
		return 0, ""
	}
	return n, strings.TrimSpace(strings.TrimRight(strings.TrimSpace(t[n:]), "#"))
}

// isFogHeading reports whether a heading is the chart template's fog section,
// "Not yet specified (fog)", or one that names the fog.
func isFogHeading(text string) bool {
	t := strings.ToLower(text)
	return strings.HasPrefix(t, "not yet specified") || strings.Contains(t, "fog")
}

// itemsOf splits the lines of a section into items: a bullet or numbered line
// opens one, any other non-blank line continues the open one (or opens a
// paragraph), and a blank line closes it.
func itemsOf(lines []string) []string {
	var items []string
	var cur []string
	fence := false
	flush := func() {
		if len(cur) > 0 {
			items = append(items, strings.Join(cur, " "))
			cur = nil
		}
	}
	for _, line := range lines {
		if isFence(line) {
			flush()
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		if lvl, _ := headingOf(line); lvl > 0 {
			flush()
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" {
			flush()
			continue
		}
		if text, ok := bulletText(t); ok {
			flush()
			cur = append(cur, text)
			continue
		}
		cur = append(cur, t)
	}
	flush()
	return items
}

// bulletText returns the text of a list line ("- x", "* x", "+ x", "1. x")
// and whether line is one.
func bulletText(t string) (string, bool) {
	for _, p := range []string{"- ", "* ", "+ "} {
		if rest, ok := strings.CutPrefix(t, p); ok {
			return strings.TrimSpace(rest), true
		}
	}
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i > 0 && i < len(t)-1 && t[i] == '.' && t[i+1] == ' ' {
		return strings.TrimSpace(t[i+2:]), true
	}
	return "", false
}

// clip shortens s to at most max runes, ending in "..." when it cut.
func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max-3])) + "..."
}
