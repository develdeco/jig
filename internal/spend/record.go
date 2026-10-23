package spend

import (
	"math"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// A spend line is the ticket journal's record of what one session has spent:
//
//	{"event":"spend","session":"s-1","harness":"claude","slice":"a",
//	 "model":"claude-sonnet-4-5","provenance":"jig",
//	 "spend":{"input":..,"output":..,"cache_read":..,"cache_write":..,"usd":..}}
//
// It is cumulative: it holds the session's whole spend so far, so the latest
// line of a session is the session's spend and the lines before it are its
// history. Slice is the slice the session was on when the line was written
// (the difference between two lines is what the session spent between them).
// Model lists the models the usage came from, comma separated, and Summary
// says "unpriced: <models>" for those no price is known for.

const unpricedPrefix = "unpriced: "

// Line makes the spend line of session at ts. harness and slice may be empty.
func Line(ts, session, harness, slice string, t Totals) journal.Line {
	sp := t.Spend()
	l := journal.Line{
		TS:         ts,
		Event:      journal.EventSpend,
		Slice:      slice,
		Session:    session,
		Harness:    harness,
		Model:      strings.Join(t.Models, ","),
		Provenance: "jig",
		Spend:      &sp,
	}
	if len(t.Unpriced) > 0 {
		l.Summary = unpricedPrefix + strings.Join(t.Unpriced, ",")
	}
	return l
}

// Same reports whether two spend lines say the same about a session: the same
// counts, dollars and models. The stamp and the slice do not matter, so a
// session that has spent nothing new is not written down again.
func Same(a, b journal.Line) bool {
	if a.Spend == nil || b.Spend == nil {
		return a.Spend == b.Spend
	}
	return *a.Spend == *b.Spend && a.Model == b.Model && a.Summary == b.Summary
}

// Latest returns the latest spend line of each session in lines, by session
// id. Lines of no session are not a session's spend and are left out. Two
// lines are ordered by their stamps; when a stamp does not parse, the one that
// comes later in the journal is the later.
func Latest(lines []journal.Line) map[string]journal.Line {
	out := map[string]journal.Line{}
	for _, l := range lines {
		if l.Event != journal.EventSpend || l.Session == "" || l.Spend == nil {
			continue
		}
		if prev, ok := out[l.Session]; ok && before(l.TS, prev.TS) {
			continue
		}
		out[l.Session] = l
	}
	return out
}

// before reports whether stamp a is strictly earlier than stamp b.
func before(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339Nano, a)
	tb, errB := time.Parse(time.RFC3339Nano, b)
	return errA == nil && errB == nil && ta.Before(tb)
}

// FromLines adds up the latest spend line of every session in lines: the
// ticket's spend.
func FromLines(lines []journal.Line) Totals {
	var t Totals
	models, unpriced := map[string]bool{}, map[string]bool{}
	for _, l := range Latest(lines) {
		t.Sessions++
		t.Input += l.Spend.Input
		t.Output += l.Spend.Output
		t.CacheRead += l.Spend.CacheRead
		t.CacheWrite += l.Spend.CacheWrite
		t.USD += l.Spend.USD
		for _, m := range strings.Split(l.Model, ",") {
			models[m] = true
		}
		if rest, ok := strings.CutPrefix(l.Summary, unpricedPrefix); ok {
			for _, m := range strings.Split(rest, ",") {
				unpriced[m] = true
			}
		}
	}
	t.USD = math.Round(t.USD*1e6) / 1e6
	t.Models, t.Unpriced = sortedKeys(models), sortedKeys(unpriced)
	return t
}
