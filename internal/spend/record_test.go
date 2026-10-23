package spend

import (
	"slices"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

func spendLine(ts, session string, input, output int64, usd float64) journal.Line {
	return journal.Line{TS: ts, Event: journal.EventSpend, Session: session, Spend: &journal.Spend{Input: input, Output: output, USD: usd}}
}

// TestLatestIsTheNewestLineOfEachSession: spend lines are cumulative, so the
// newest line of a session is its spend. Stamps order them, and a pair that
// does not order falls back on the journal order.
func TestLatestIsTheNewestLineOfEachSession(t *testing.T) {
	t.Parallel()
	lines := []journal.Line{
		spendLine("2026-01-01T10:00:00.5Z", "a", 1, 1, 0),
		{TS: "2026-01-01T10:00:01Z", Event: journal.EventNote, Session: "a"},
		spendLine("2026-01-01T10:00:02Z", "a", 5, 5, 0),
		// A merge from another machine can put an older line later in the file.
		spendLine("2026-01-01T10:00:01Z", "a", 3, 3, 0),
		spendLine("2026-01-01T10:00:00Z", "b", 7, 7, 0),
		spendLine("not a time", "c", 1, 1, 0),
		spendLine("also not", "c", 2, 2, 0),
		spendLine("2026-01-01T10:00:00Z", "", 9, 9, 0), // no session: not a session spend
	}
	got := Latest(lines)
	if len(got) != 3 || got["a"].Spend.Input != 5 || got["b"].Spend.Input != 7 || got["c"].Spend.Input != 2 {
		t.Errorf("Latest = %+v, want a at 5, b at 7 and c at 2 (the later of two lines that do not order)", got)
	}
}

// TestFromLinesAddsUpTheSessions: the spend of a ticket is the latest line of
// every session added up, with the models and the unpriced ones named.
func TestFromLinesAddsUpTheSessions(t *testing.T) {
	t.Parallel()
	a := Line("2026-01-01T10:00:00Z", "a", "claude", "s1", Totals{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, USD: 1.5, Models: []string{"m1"}})
	b := Line("2026-01-01T10:00:00Z", "b", "claude", "", Totals{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, USD: 0.25, Models: []string{"m1", "m2"}, Unpriced: []string{"m2"}})
	older := Line("2026-01-01T09:00:00Z", "a", "claude", "s1", Totals{Input: 1, USD: 99, Models: []string{"old"}})
	got := FromLines([]journal.Line{a, b, older})
	if got.Sessions != 2 || got.Input != 11 || got.Output != 22 || got.CacheRead != 33 || got.CacheWrite != 44 || !near(got.USD, 1.75) {
		t.Errorf("totals = %+v, want two sessions: 11 in, 22 out, 33 read, 44 write, $1.75", got)
	}
	if !slices.Equal(got.Models, []string{"m1", "m2"}) || !slices.Equal(got.Unpriced, []string{"m2"}) {
		t.Errorf("models = %q, unpriced = %q, want m1 and m2, with m2 unpriced", got.Models, got.Unpriced)
	}
	if got := FromLines(nil); got.Sessions != 0 || got.Tokens() != 0 {
		t.Errorf("no lines: %+v, want nothing", got)
	}
}

// TestSameIgnoresWhenAndWhere: a session that has spent nothing new is not
// written down again, whatever the clock and the slice say.
func TestSameIgnoresWhenAndWhere(t *testing.T) {
	t.Parallel()
	tot := Totals{Input: 1, Output: 2, USD: 0.5, Models: []string{"m"}}
	a := Line("2026-01-01T10:00:00Z", "a", "claude", "s1", tot)
	b := Line("2026-01-01T11:00:00Z", "a", "claude", "s2", tot)
	if !Same(a, b) {
		t.Error("Same = false for two lines of one total")
	}
	more := tot
	more.Output++
	if Same(a, Line("2026-01-01T11:00:00Z", "a", "claude", "s1", more)) {
		t.Error("Same = true for a line with one more output token")
	}
	unpriced := tot
	unpriced.Unpriced = []string{"m"}
	if Same(a, Line("2026-01-01T11:00:00Z", "a", "claude", "s1", unpriced)) {
		t.Error("Same = true for a line that names an unpriced model the other does not")
	}
}
