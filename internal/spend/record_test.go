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

// TestLatestKeysAnAgentApartFromItsSession: a subagent's line carries its
// parent's session id and an agent id of its own, and the latest line of a
// session and of each of its agents are kept apart, by Key.
func TestLatestKeysAnAgentApartFromItsSession(t *testing.T) {
	t.Parallel()
	if got := Key("s-1", ""); got != "s-1" {
		t.Errorf("Key of a session = %q, want its id", got)
	}
	if got := Key("s-1", "a-1"); got != "s-1/a-1" {
		t.Errorf("Key of an agent = %q, want session/agent", got)
	}
	agent := func(ts, session, agent string, input int64) journal.Line {
		return AgentLine(ts, session, agent, "claude", "a", Totals{Input: input})
	}
	lines := []journal.Line{
		spendLine("2026-01-01T10:00:00Z", "s-1", 1000, 0, 0),
		agent("2026-01-01T10:00:01Z", "s-1", "a-1", 100),
		agent("2026-01-01T10:00:02Z", "s-1", "a-2", 200),
		agent("2026-01-01T10:00:03Z", "s-1", "a-1", 150),
		agent("2026-01-01T10:00:00Z", "s-1", "a-1", 7), // older, later in the file
		spendLine("2026-01-01T10:00:04Z", "s-1", 1100, 0, 0),
		agent("2026-01-01T10:00:05Z", "", "a-3", 9), // no session: left out
	}
	got := Latest(lines)
	if len(got) != 3 || got["s-1"].Spend.Input != 1100 || got["s-1/a-1"].Spend.Input != 150 || got["s-1/a-2"].Spend.Input != 200 {
		t.Errorf("Latest = %+v, want s-1 at 1100, s-1/a-1 at 150 and s-1/a-2 at 200", got)
	}
	if l := got["s-1/a-1"]; l.Agent != "a-1" || l.Session != "s-1" || l.Harness != "claude" {
		t.Errorf("agent line = %+v, want session s-1, agent a-1 on claude", l)
	}
}

// TestFromLinesCountsAgentsApartFromSessions: the ticket's spend adds the
// latest line of every key; Sessions counts the distinct session ids, so a
// session and its agents are one session, and a ticket whose only lines are a
// session's agents has that one session; Agents counts the agents.
func TestFromLinesCountsAgentsApartFromSessions(t *testing.T) {
	t.Parallel()
	ts := "2026-01-01T10:00:00Z"
	session := Line(ts, "s-1", "claude", "a", Totals{Input: 1000, USD: 1, Models: []string{"m1"}})
	a1 := AgentLine(ts, "s-1", "a-1", "claude", "a", Totals{Input: 100, USD: 0.5, Models: []string{"m2"}, Unpriced: []string{"m2"}})
	a2 := AgentLine(ts, "s-1", "a-2", "claude", "a", Totals{Output: 50, USD: 0.25, Models: []string{"m1"}})
	other := Line(ts, "s-2", "claude", "a", Totals{CacheRead: 10})

	got := FromLines([]journal.Line{session, a1, a2, other})
	if got.Sessions != 2 || got.Agents != 2 || got.Input != 1100 || got.Output != 50 || got.CacheRead != 10 || !near(got.USD, 1.75) {
		t.Errorf("totals = %+v, want 2 sessions and 2 agents: 1100 in, 50 out, 10 read, $1.75", got)
	}
	if !slices.Equal(got.Models, []string{"m1", "m2"}) || !slices.Equal(got.Unpriced, []string{"m2"}) {
		t.Errorf("models = %q, unpriced = %q, want m1 and m2, with m2 unpriced", got.Models, got.Unpriced)
	}

	only := FromLines([]journal.Line{a1, a2})
	if only.Sessions != 1 || only.Agents != 2 || only.Input != 100 || only.Output != 50 {
		t.Errorf("totals of a ticket with only agent lines = %+v, want 1 session and 2 agents", only)
	}

	// Lines with no agent total as they always did.
	plain := FromLines([]journal.Line{session, other})
	if plain.Sessions != 2 || plain.Agents != 0 || plain.Input != 1000 {
		t.Errorf("totals with no agent line = %+v, want 2 sessions and no agents", plain)
	}
}

// TestSumAddsTotals: Sum adds the counts, the sessions, the agents and the
// dollars, and merges the models and the unpriced ones sorted with no repeats;
// nothing sums to nothing.
func TestSumAddsTotals(t *testing.T) {
	t.Parallel()
	a := Totals{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, USD: 0.1, Sessions: 1, Agents: 2, Models: []string{"m2", "m1"}, Unpriced: []string{"m2"}}
	b := Totals{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, USD: 0.2, Sessions: 2, Agents: 1, Models: []string{"m3", "m1"}, Unpriced: []string{"m3", "m2"}}
	got := Sum(a, b)
	if got.Input != 11 || got.Output != 22 || got.CacheRead != 33 || got.CacheWrite != 44 || got.Tokens() != 110 {
		t.Errorf("counts = %+v, want 11 in, 22 out, 33 read, 44 write", got)
	}
	if got.Sessions != 3 || got.Agents != 3 {
		t.Errorf("sessions %d, agents %d, want 3 and 3", got.Sessions, got.Agents)
	}
	if got.USD != 0.3 {
		t.Errorf("usd = %v, want 0.3 (0.1 + 0.2 rounded to 1e-6)", got.USD)
	}
	if !slices.Equal(got.Models, []string{"m1", "m2", "m3"}) || !slices.Equal(got.Unpriced, []string{"m2", "m3"}) {
		t.Errorf("models = %q, unpriced = %q, want m1 m2 m3 and m2 m3", got.Models, got.Unpriced)
	}
	if got := Sum(); got.Tokens() != 0 || got.Sessions != 0 || got.Models != nil || got.Unpriced != nil {
		t.Errorf("Sum() = %+v, want nothing", got)
	}
	if got := Sum(a); got.Input != 10 || got.Agents != 2 || !slices.Equal(got.Models, []string{"m1", "m2"}) {
		t.Errorf("Sum(a) = %+v, want a with its models sorted", got)
	}
}
