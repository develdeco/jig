package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/verify"
)

// The records the detail screens show under their rows: what a slice's commits
// were and how each verification went, written down by the journal's lines, and
// the words of the status's tables. Nothing here reads a store.

// commitRecord is one commit a slice's verification was asked to check, and
// every run of the oracle against it.
type commitRecord struct {
	SHA, Subject string
	// State is how the commit stands: StateQueued while it has no recorded
	// result, StateBuilding once a run
	// passed and no verified line follows, StateGreen once verified,
	// StateStalled when the latest run failed or the verification could not
	// run.
	State string
	// Verified is the line that says the commit passed.
	Verified events.Envelope
	// Began is the instant of the first line about the commit, which is where
	// a list of commits puts it: later words add to the commit and never move it.
	Began time.Time
	// Runs are the oracle runs against the commit, oldest first.
	Runs []events.Envelope
	// Error is the verify-error line that ended the commit's verification, if
	// one did.
	Error *events.Envelope
	// Recordings are the files the build's scenarios wrote at the commit, the
	// ones its recorded lines name.
	Recordings []journal.Recording
}

// commits are the commits slice's verifications have a word about, oldest first
// by their first line, from the journal's oracle, verified, verify-error and
// recorded lines. A verify-pending line (an old worker's history every reader
// skips) gives a commit no time, no state and no subject, and a commit with
// only such a line is none.
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
		case journal.EventRecorded:
			c := get(l)
			c.Recordings = append(c.Recordings, l.Recordings...)
		case journal.EventVerifyError:
			c := get(l)
			l := l
			c.State, c.Error = StateStalled, &l
		}
	}
	return out
}

// sliceCommits are the slice's commits as the screens list them: the ones the
// journal has a word about, and a commit only the status's verification table
// names (a commit nothing was run against reads the state the table gives it,
// "no recorded result"), none left out.
func (t *ticket) sliceCommits(slice string) []commitRecord {
	out := t.commits(slice)
	if t.status == nil {
		return out
	}
	for _, v := range t.status.Verifications {
		if v.Slice != slice || v.Commit == "" {
			continue
		}
		known := false
		for _, c := range out {
			if strings.HasPrefix(c.SHA, v.Commit) {
				known = true
				break
			}
		}
		if !known {
			out = append(out, commitRecord{SHA: v.Commit, State: StateQueued})
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

// runWords is an oracle run as the screens say it: its result, how long it took,
// what its process tree used when the line measured it, and who ran it, "run by
// builder, witnessed by jig". The session is named in the role the line gives
// it (an oracle line is a builder's), never by the first role it played, so a
// session that drafted the brief and then ran the oracle is the builder. The
// command stands in place of "oracle" only when it is not the slice's own, and a
// run that covers no commit says why.
func (m *Model) runWords(t *ticket, l events.Envelope, oracle string) string {
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
	parts := []string{cmd + word + took(l.Seconds)}
	if cpu := verify.CPUWords(l.CPUMs); cpu != "" {
		parts = append(parts, "cpu "+cpu+"s")
	}
	if peak := verify.MemoryWords(l.PeakBytes); peak != "" {
		if basis := verify.BasisWords(l.PeakBasis); basis != "" {
			peak += " (" + basis + ")"
		}
		parts = append(parts, "peak "+peak)
	}
	if l.Uncovered != "" {
		parts = append(parts, "covers nothing: "+l.Uncovered)
	}
	ran := "witnessed by jig"
	if who := m.whoAs(t, l.Session, roleOf(l.Event)); who != "" {
		ran = "run by " + who + ", " + ran
	}
	return strings.Join(append(parts, ran), " · ")
}

// commitWhen is when a commit's latest word came: its latest run, its
// verification or the error that ended it. A commit none of them is about has
// no time.
func (m *Model) commitWhen(c commitRecord) string {
	var last events.Envelope
	var lastAt time.Time
	for _, l := range c.words() {
		if at, ok := l.At(); ok && !at.Before(lastAt) {
			last, lastAt = l, at
		}
	}
	if last.TS == "" {
		return ""
	}
	return clock(last, m.zone)
}

// words are the lines that speak of the commit's verification: its runs, its
// verification and the error that ended it.
func (c commitRecord) words() []events.Envelope {
	out := append([]events.Envelope(nil), c.Runs...)
	if c.Verified.TS != "" {
		out = append(out, c.Verified)
	}
	if c.Error != nil {
		out = append(out, *c.Error)
	}
	return out
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
