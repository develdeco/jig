package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// The records the detail screens show under their rows: what a slice's commits
// were and how each verification went, written down by the journal's lines, and
// the words of the status's tables. Nothing here reads a store.

// commitRecord is one commit a slice's verification was asked to check, and
// every run of the oracle against it.
type commitRecord struct {
	SHA, Subject string
	// State is how the commit stands: StateQueued while it has no recorded
	// result (an old pending line is no result), StateBuilding once a run
	// passed and no verified line follows, StateGreen once verified,
	// StateStalled when the latest run failed or the verification could not
	// run.
	State string
	// At is when the verification was asked for, and Verified when it passed.
	At, Verified events.Envelope
	// Began is the instant of the first line about the commit, which is where
	// a list of commits puts it: later words add to the commit and never move it.
	Began time.Time
	// Runs are the oracle runs against the commit, oldest first.
	Runs []events.Envelope
	// Error is the verify-error line that ended the commit's verification, if
	// one did.
	Error *events.Envelope
}

// commits are the commits slice's verifications were asked about, oldest first by
// their first line,
// from the journal's verify-pending, oracle, verified and verify-error lines. An
// oracle line for a commit that was never asked about (the session ran the
// oracle itself) is a commit of its own, with no subject.
func (t *ticket) commits(slice string) []commitRecord {
	var out []commitRecord
	at := map[string]int{}
	get := func(l events.Envelope) *commitRecord {
		if i, ok := at[l.Commit]; ok {
			return &out[i]
		}
		at[l.Commit] = len(out)
		began, _ := l.At()
		out = append(out, commitRecord{SHA: l.Commit, State: StateQueued, Began: began})
		return &out[len(out)-1]
	}
	for _, l := range t.lines {
		if l.Slice != slice || l.Commit == "" {
			continue
		}
		switch l.Event {
		case journal.EventVerifyPending:
			c := get(l)
			c.At, c.Subject = l, firstOf(c.Subject, l.Summary)
		case journal.EventOracle:
			c := get(l)
			c.Subject = firstOf(c.Subject, l.Subject)
			c.Runs = append(c.Runs, l)
			// A run that covers nothing is listed, and leaves the state alone.
			switch {
			case l.Uncovered != "":
			case l.Outcome == "pass":
				c.State = StateBuilding
			default:
				c.State = StateStalled
			}
		case journal.EventVerified:
			c := get(l)
			c.State, c.Verified = StateGreen, l
		case journal.EventVerifyError:
			c := get(l)
			l := l
			c.State, c.Error = StateStalled, &l
		}
	}
	return out
}

// lastRunFailed is the failed oracle run that is the latest word on the slice's
// verification, or false when the latest run passed or there is none.
func (t *ticket) lastRunFailed(slice string) (events.Envelope, bool) {
	i := t.latest(slice, journal.EventOracle)
	for i >= 0 && t.lines[i].Uncovered != "" {
		// A run that covers nothing is not the slice's latest word.
		j := -1
		for k := i - 1; k >= 0; k-- {
			if t.lines[k].Slice == slice && t.lines[k].Event == journal.EventOracle {
				j = k
				break
			}
		}
		i = j
	}
	if i < 0 || t.lines[i].Outcome == "pass" {
		return events.Envelope{}, false
	}
	if v := t.latest(slice, journal.EventVerified); v > i {
		return events.Envelope{}, false
	}
	return t.lines[i], true
}

// oracleWords is an oracle run as a line says it: "passed in 15s", and who ran it
// when the line says. The command stands in place of "oracle" only when it is not
// the one the screen already says: "`go test ./pkg` passed in 15s".
func oracleWords(l events.Envelope, oracle string) string {
	cmd := "oracle "
	if l.Command != "" && l.Command != oracle {
		cmd = "`" + l.Command + "` "
	}
	word := l.Outcome
	switch l.Outcome {
	case "pass":
		word = "passed"
	case "fail":
		word = "failed"
		switch l.Cause {
		case journal.CauseStopped:
			word = "stopped, not counted"
		case journal.CauseTimeout:
			word = "timed out, not counted"
		}
	case "":
		word = "ran"
	}
	text := cmd + word + took(l.Seconds)
	switch l.Source {
	case "jig":
		text += " · jig ran it"
	case "session":
		text += " · the session ran it"
	}
	return text
}

// wrap breaks text into lines of at most w cells, on spaces, keeping at most
// maxLines of them; when it cut, the last line ends in an ellipsis. A word wider
// than a line is cut. Text with no room gives no lines.
func wrap(text string, w, maxLines int) []string {
	if w < 1 || maxLines < 1 {
		return nil
	}
	lines := wordLines(text, w)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		last := lines[len(lines)-1]
		if width(last)+1 > w {
			last = fit(last, w-1)
			last = strings.TrimSuffix(last, "…")
		}
		lines[len(lines)-1] = last + "…"
	}
	return lines
}

// wrapWhole breaks text into lines of at most w cells and cuts none of it: it
// breaks on spaces, keeps the line breaks the text has (a blank line stays a
// blank line), cuts a word wider than a line into lines, and never ends in an
// ellipsis. It is for text a person acts on, which a cut would leave unread.
func wrapWhole(text string, w int) []string {
	if w < 1 {
		return nil
	}
	var lines []string
	for _, para := range strings.Split(strings.TrimRight(text, "\n\r "), "\n") {
		if got := wordLines(para, w); len(got) > 0 {
			lines = append(lines, got...)
		} else {
			lines = append(lines, "")
		}
	}
	return lines
}

// wordLines is text broken into lines of at most w cells on spaces, its line
// breaks read as spaces.
func wordLines(text string, w int) []string {
	var lines []string
	cur := ""
	for _, word := range strings.Fields(text) {
		for width(word) > w {
			head := ansi.Truncate(word, w, "")
			if head == "" {
				word = ""
				break
			}
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			lines = append(lines, head)
			word = word[len(head):]
		}
		if word == "" {
			continue
		}
		switch {
		case cur == "":
			cur = word
		case width(cur)+1+width(word) <= w:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// shortRange is a reviewed range as the review screen says it: "full 1a2b3c4..5d6e7f8".
func shortRange(scope, base, head string) string {
	if head == "" {
		return ""
	}
	b := "?"
	if base != "" {
		b = shortRef(base)
	}
	text := b + ".." + shortRef(head)
	if scope != "" {
		text = scope + " " + text
	}
	return text
}

// shortRef is a sha, or a repo:sha pair of a head that names several repos, cut
// to the length a commit is shown at.
func shortRef(ref string) string {
	parts := strings.Split(ref, "+")
	for i, p := range parts {
		if name, sha, ok := strings.Cut(p, ":"); ok {
			parts[i] = name + ":" + short(sha)
		} else {
			parts[i] = short(p)
		}
	}
	return strings.Join(parts, "+")
}
