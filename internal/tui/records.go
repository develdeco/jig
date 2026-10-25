package tui

import (
	"strings"

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
	// State is how the commit stands: StateVerifying until a run ends,
	// StateGreen once verified, StateStalled when the latest run failed or the
	// verification could not run.
	State string
	// At is when the verification was asked for, and Verified when it passed.
	At, Verified events.Envelope
	// Runs are the oracle runs against the commit, oldest first.
	Runs []events.Envelope
	// Error is the verify-error line that ended the commit's verification, if
	// one did.
	Error *events.Envelope
}

// commits are the commits slice's verifications were asked about, oldest first,
// from the journal's verify-pending, oracle, verified and verify-error lines. An
// oracle line for a commit that was never asked about (the session ran the
// oracle itself) is a commit of its own, with no subject.
func (t *ticket) commits(slice string) []commitRecord {
	var out []commitRecord
	at := map[string]int{}
	get := func(sha string) *commitRecord {
		if i, ok := at[sha]; ok {
			return &out[i]
		}
		at[sha] = len(out)
		out = append(out, commitRecord{SHA: sha, State: StateVerifying})
		return &out[len(out)-1]
	}
	for _, l := range t.lines {
		if l.Slice != slice || l.Commit == "" {
			continue
		}
		switch l.Event {
		case journal.EventVerifyPending:
			c := get(l.Commit)
			c.At, c.Subject = l, firstOf(c.Subject, l.Summary)
		case journal.EventOracle:
			c := get(l.Commit)
			c.Runs = append(c.Runs, l)
			if l.Outcome == "pass" {
				c.State = StateVerifying
			} else {
				c.State = StateStalled
			}
		case journal.EventVerified:
			c := get(l.Commit)
			c.State, c.Verified = StateGreen, l
		case journal.EventVerifyError:
			c := get(l.Commit)
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
	if i < 0 || t.lines[i].Outcome == "pass" {
		return events.Envelope{}, false
	}
	if v := t.latest(slice, journal.EventVerified); v > i {
		return events.Envelope{}, false
	}
	return t.lines[i], true
}

// oracleWords is an oracle run as a line says it: "`go test ./...` passed in
// 15s", and who ran it when the line says.
func oracleWords(l events.Envelope) string {
	cmd := "its oracle"
	if l.Command != "" {
		cmd = "`" + l.Command + "`"
	}
	word := l.Outcome
	switch l.Outcome {
	case "pass":
		word = "passed"
	case "fail":
		word = "failed"
	case "":
		word = "ran"
	}
	text := cmd + " " + word + took(l.Seconds)
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
	var lines []string
	cur := ""
	cut := false
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
	if len(lines) > maxLines {
		lines, cut = lines[:maxLines], true
	}
	if cut {
		last := lines[len(lines)-1]
		if width(last)+1 > w {
			last = fit(last, w-1)
			last = strings.TrimSuffix(last, "…")
		}
		lines[len(lines)-1] = last + "…"
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
