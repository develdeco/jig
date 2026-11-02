package spend

import (
	"math"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// A spend line is the ticket journal's record of what one session, or one of
// its subagents, has spent:
//
//	{"event":"spend","session":"s-1","harness":"claude","slice":"a",
//	 "model":"claude-sonnet-4-5","provenance":"jig",
//	 "spend":{"input":..,"output":..,"cache_read":..,"cache_write":..,"usd":..}}
//
// It is cumulative: it holds the session's whole spend so far, so the latest
// line of a session is the session's spend and the lines before it are its
// history. A subagent's line is the same line with its agent id set (agent):
// it carries the parent session's id and holds that agent's transcript alone,
// and the session's own line leaves out the agents that have a line of their
// own (Key says how the two are kept apart). Slice is the slice the session
// was on when the line was written (the difference between two lines is what
// the session spent between them). Model lists the models the usage came
// from, comma separated, and Summary says "unpriced: <models>" for those no
// price is known for.

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

// AgentLine makes the spend line of one subagent of session at ts: Line with
// the agent's id set. t is the agent's own spend, not the session's.
func AgentLine(ts, session, agent, harness, slice string, t Totals) journal.Line {
	l := Line(ts, session, harness, slice, t)
	l.Agent = agent
	return l
}

// Key is what a spend line is kept by: the session id for the session's own
// line (agent ""), and the session id and the agent id for an agent's. The
// latest line of each key is that key's spend.
func Key(session, agent string) string {
	if agent == "" {
		return session
	}
	return session + "/" + agent
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

// Latest returns the latest spend line of each session and of each of its
// agents in lines, by Key. Lines of no session are not a session's spend and
// are left out. Two lines are ordered by their stamps; when a stamp does not
// parse, the one that comes later in the journal is the later.
func Latest(lines []journal.Line) map[string]journal.Line {
	out := map[string]journal.Line{}
	for _, l := range lines {
		if l.Event != journal.EventSpend || l.Session == "" || l.Spend == nil {
			continue
		}
		k := Key(l.Session, l.Agent)
		if prev, ok := out[k]; ok && before(l.TS, prev.TS) {
			continue
		}
		out[k] = l
	}
	return out
}

// before reports whether stamp a is strictly earlier than stamp b.
func before(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339Nano, a)
	tb, errB := time.Parse(time.RFC3339Nano, b)
	return errA == nil && errB == nil && ta.Before(tb)
}

// FromLines adds up the latest spend line of every session and of every agent
// in lines: the ticket's spend. Sessions counts the distinct session ids, so
// a ticket whose only lines are a session's agents has one session, and
// Agents the latest lines that are an agent's.
func FromLines(lines []journal.Line) Totals {
	var t Totals
	models, unpriced := map[string]bool{}, map[string]bool{}
	sessions := map[string]bool{}
	for _, l := range Latest(lines) {
		sessions[l.Session] = true
		if l.Agent != "" {
			t.Agents++
		}
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
	t.Sessions = len(sessions)
	t.USD = math.Round(t.USD*1e6) / 1e6
	t.Models, t.Unpriced = sortedKeys(models), sortedKeys(unpriced)
	return t
}

// Sum adds Totals up: the counts, the sessions and the agents, the dollars
// (rounded as FromLines rounds them) and the models and unpriced models of
// all of them, each sorted with no repeats. A chart's spend is the Sum of its
// tickets' FromLines: a session that wrote lines on two tickets counts on each,
// as it spent on both.
func Sum(ts ...Totals) Totals {
	var out Totals
	models, unpriced := map[string]bool{}, map[string]bool{}
	for _, t := range ts {
		out.Input += t.Input
		out.Output += t.Output
		out.CacheRead += t.CacheRead
		out.CacheWrite += t.CacheWrite
		out.USD += t.USD
		out.Sessions += t.Sessions
		out.Agents += t.Agents
		for _, m := range t.Models {
			models[m] = true
		}
		for _, m := range t.Unpriced {
			unpriced[m] = true
		}
	}
	out.USD = math.Round(out.USD*1e6) / 1e6
	out.Models, out.Unpriced = sortedKeys(models), sortedKeys(unpriced)
	return out
}
