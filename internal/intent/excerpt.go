package intent

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxExcerptBytes caps how much transcript text an excerpt carries: enough
// to convey intent, not a full transcript replay.
const MaxExcerptBytes = 32 * 1024

// RenderExcerpt renders s's user and assistant text as plain "role: text"
// lines, oldest first by Timestamp - tool calls and tool results already
// dropped by the reader - capped at MaxExcerptBytes by keeping the
// earliest line (almost always the user's own original ask, the line most
// likely to state intent directly) plus as many of the most recent lines
// as fit, since intent also tends to sharpen as a session goes on. A
// folded-in subagent's own messages are merged into this order by their
// own Timestamp, not appended en masse after the parent's: a long-running
// implementer subagent's messages otherwise crowd out both the parent's
// own opening ask and its own most recent turns. A caller writes the
// result under the jig home, never the store: a transcript can hold
// secrets.
func RenderExcerpt(s *Session) string {
	if s == nil {
		return ""
	}
	msgs := append([]Message(nil), s.Messages...)
	sort.SliceStable(msgs, func(i, j int) bool {
		return msgs[i].Timestamp.Before(msgs[j].Timestamp)
	})
	var lines []string
	for _, m := range msgs {
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}
		role := "user"
		switch {
		case m.Role == RoleAssistant:
			role = "assistant"
		case m.FromSubagent:
			// A RoleUser message folded in from a subagent's own
			// transcript is the parent agent's own prompt to that
			// subagent, not the developer's own words, even though the
			// transcript format gives both the same "user" role - labeled
			// distinctly so the summarizer is never told to read it as
			// the developer's own ask.
			role = "subagent-prompt"
		}
		lines = append(lines, fmt.Sprintf("%s: %s", role, text))
	}
	return capExcerptLines(lines, MaxExcerptBytes)
}

// excerptLineTruncMarker notes that one line's own text - not the excerpt's
// own line selection - was cut to stay within budget: a pasted log inside
// a single message, distinct from excerptOmissionMarker below, which marks
// a gap between kept lines, never a cut inside one.
const excerptLineTruncMarker = "...(truncated)"

// excerptOmissionMarker stands between the kept opening line and the kept
// recent lines when something in the middle was dropped.
const excerptOmissionMarker = "(earlier messages omitted)"

// excerptSep separates two lines of an excerpt.
const excerptSep = "\n\n"

// capExcerptLines joins lines (oldest first) with a blank line between
// each, keeping the first line - the session's own opening turn - plus as
// many of the most recent ones as fit within maxBytes, with a single
// omission marker between them when something in the middle was actually
// dropped. When the full text already fits, nothing is dropped and no
// marker is added. The result never exceeds maxBytes, the omission marker
// and the separators included: a head or tail line that alone is over
// budget is itself cut - the head keeping its own start, the tail its own
// end, each carrying excerptLineTruncMarker - rather than kept whole, so
// one oversized message (a pasted log in the opening or the latest turn)
// cannot pass the cap uncut.
func capExcerptLines(lines []string, maxBytes int) string {
	full := strings.Join(lines, excerptSep)
	if len(full) <= maxBytes {
		return full
	}
	if maxBytes <= 0 || len(lines) == 0 {
		return ""
	}

	head := lines[0]
	if len(lines) == 1 || len(head)+len(excerptSep) >= maxBytes {
		// Either the only line there is, or the opening line alone
		// already fills the cap: keep only it, cut to fit, so at least
		// the start of the opening turn always survives.
		return truncateKeepStart(head, maxBytes)
	}

	// Fit the recent lines with no marker first. When that reaches all the
	// way back to right after the head, nothing was dropped in between and
	// no marker is due. Otherwise one is, so they are fitted again around
	// its own bytes.
	tail, from := keepRecent(lines, maxBytes-len(head))
	if from <= 1 {
		return head + excerptSep + strings.Join(tail, excerptSep)
	}
	tail, _ = keepRecent(lines, maxBytes-len(head)-len(excerptSep)-len(excerptOmissionMarker))
	if len(tail) == 0 {
		return head
	}
	return head + excerptSep + excerptOmissionMarker + excerptSep + strings.Join(tail, excerptSep)
}

// keepRecent returns the most recent of lines[1:] that fit in budget bytes,
// each counted with the separator that precedes it, oldest first, and the
// index of the first one kept (len(lines) when none is). When even the
// single most recent line does not fit whole, its own end is kept, cut to
// fit, rather than dropping it and so the entire tail for want of room.
func keepRecent(lines []string, budget int) (tail []string, from int) {
	from = len(lines)
	total := 0
	for i := len(lines) - 1; i >= 1; i-- {
		n := len(lines[i]) + len(excerptSep)
		if total+n > budget {
			if len(tail) == 0 {
				if cut := truncateKeepEnd(lines[i], budget-len(excerptSep)); cut != "" {
					tail = []string{cut}
					from = i
				}
			}
			break
		}
		tail = append([]string{lines[i]}, tail...)
		total += n
		from = i
	}
	return tail, from
}

// truncateKeepStart cuts s to at most maxBytes, keeping its own start and
// appending excerptLineTruncMarker, snapped to a UTF-8 rune boundary so a
// multi-byte rune is never split. A cut is always marked: a maxBytes too
// small to hold the marker and any of s yields "".
func truncateKeepStart(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	n := maxBytes - len(excerptLineTruncMarker)
	if n <= 0 {
		return ""
	}
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + excerptLineTruncMarker
}

// truncateKeepEnd cuts s to at most maxBytes, keeping its own end and
// prepending excerptLineTruncMarker, snapped to a UTF-8 rune boundary so a
// multi-byte rune is never split. A cut is always marked: a maxBytes too
// small to hold the marker and any of s yields "".
func truncateKeepEnd(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	n := maxBytes - len(excerptLineTruncMarker)
	if n <= 0 {
		return ""
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return excerptLineTruncMarker + s[start:]
}
