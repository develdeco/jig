package correct

import (
	"fmt"
	"slices"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// StallAfter is how many oracle failures with one signature make a stall: the
// repeat of a failure is the sign that the next attempt is more of the same
// (outcome.StallCounter's threshold, the frontier's own).
const StallAfter = 2

// A Stall is a slice whose oracle failed the same way again: StallAfter or
// more oracle lines of outcome fail with one signature, counted from the
// slice's last verification or requeue, since either starts it over.
type Stall struct {
	Slice string
	// Signature is the failure signature the lines share.
	Signature string
	// Count is how many failures carry it.
	Count int
	// Commit, Attempt, TS and Summary are the latest of them.
	Commit  string
	Attempt int
	TS      string
	Summary string
}

// Ref fingerprints the stall: the slice, the failure and the attempt that
// repeated it. The next repeat is a new stall with a Ref of its own, so a
// session that went on failing the same way is told again.
func (s Stall) Ref() string {
	return fmt.Sprintf("stall:%s:%s:%d", s.Slice, fingerprint(s.Signature), s.Attempt)
}

// Stalls reads the stalls out of a ticket's journal, in the order their
// latest failures were written. Only oracle failures that carry a signature
// count (the digester's verify workers write it; the frontier's lines have
// none), and a slice's count starts over at its next verified line (it
// passed) or requeue line (a person or an agent decided it starts over).
func Stalls(lines []journal.Line) []Stall {
	type tally struct {
		Stall
		at int // index of the latest failure, to order the result
	}
	seen := map[string]map[string]*tally{} // slice -> signature -> tally
	for i, l := range lines {
		switch l.Event {
		case journal.EventVerified, journal.EventRequeue:
			delete(seen, l.Slice)
		case journal.EventOracle:
			if l.Outcome != "fail" || l.Signature == "" {
				continue
			}
			bySig := seen[l.Slice]
			if bySig == nil {
				bySig = map[string]*tally{}
				seen[l.Slice] = bySig
			}
			t := bySig[l.Signature]
			if t == nil {
				t = &tally{Stall: Stall{Slice: l.Slice, Signature: l.Signature}}
				bySig[l.Signature] = t
			}
			t.Count++
			t.Commit, t.Attempt, t.TS, t.Summary, t.at = l.Commit, l.Attempt, l.TS, l.Summary, i
		}
	}
	var found []*tally
	for _, bySig := range seen {
		for _, t := range bySig {
			if t.Count >= StallAfter {
				found = append(found, t)
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	slices.SortFunc(found, func(a, b *tally) int { return a.at - b.at })
	out := make([]Stall, len(found))
	for i, t := range found {
		out[i] = t.Stall
	}
	return out
}

// Stalled is ss after a stall was found: stalled, with the reason, signature
// and summary the frontier's own stall leaves, and the attempts counted up to
// the stall's. It reports whether that changes the file. A slice that is
// already stalled, or waiting on a question, an environment or nothing at all
// (green), keeps the state it has: those say what the slice waits for, and a
// stall is no reason to take that away.
func Stalled(ss store.SliceState, s Stall) (store.SliceState, bool) {
	switch ss.State {
	case "queued", "building", "":
	default:
		return ss, false
	}
	ss.State = "stalled"
	ss.Reason = "stall"
	ss.Signature = s.Signature
	ss.StallSummary = s.Summary
	ss.Attempts = max(ss.Attempts, s.Attempt)
	return ss, true
}

// stallText is what the session is told of a stall. Held back from stopping
// (unattended), the slice is also marked stalled, which is what the requeue
// remedy clears.
func stallText(ticket string, s Stall, unattended bool) string {
	text := fmt.Sprintf("slice %s failed its oracle the same way %d times (attempt %d", s.Slice, s.Count, s.Attempt)
	if s.Commit != "" {
		text += ", latest at " + short12(s.Commit)
	}
	text += ")"
	if s.Summary != "" {
		text += ": " + oneLine(s.Summary, 200)
	}
	text += ". A repeat is likely a misconception: change the approach before another attempt."
	if unattended {
		text += fmt.Sprintf(" The slice is marked stalled; run `jig requeue %s --slice %s` once the approach has changed.", ticket, s.Slice)
	}
	return text
}
