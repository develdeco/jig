package main

import (
	"strconv"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/spend"
	"github.com/develdeco/jig/internal/store"
)

// nextChartSpend is what a chart's run has cost so far: the sum of its tickets'
// spend, which is the latest line of each session and of each subagent on each
// ticket (spend.FromLines), added up. Tickets counts the tickets that have
// spend and Agents the subagent lines among them.
type nextChartSpend struct {
	Tickets    int      `json:"tickets"`
	Agents     int      `json:"agents"`
	Tokens     int64    `json:"tokens"`
	Input      int64    `json:"input"`
	Output     int64    `json:"output"`
	CacheRead  int64    `json:"cache_read"`
	CacheWrite int64    `json:"cache_write"`
	USD        float64  `json:"usd"`
	Unpriced   []string `json:"unpriced,omitempty"`
}

// chartSpend folds the spend of the chart's tickets. It is done per ticket and
// not over all the lines at once, because one session writes lines on several
// tickets and spend.Latest would keep only the newest of them. A ticket whose
// journal cannot be read adds nothing: a problem is a row of the chart, never a
// reason to hide it. It returns nil when no ticket has spend.
func chartSpend(st *store.Store, c phase.Chart) *nextChartSpend {
	var each []spend.Totals
	for _, r := range c.Rows {
		if r.ID == "" {
			continue
		}
		lines, err := journal.Read(st, r.ID)
		if err != nil {
			continue
		}
		if t := spend.FromLines(lines); t.Sessions > 0 {
			each = append(each, t)
		}
	}
	if len(each) == 0 {
		return nil
	}
	sum := spend.Sum(each...)
	return &nextChartSpend{
		Tickets: len(each), Agents: sum.Agents,
		Tokens: sum.Tokens(), Input: sum.Input, Output: sum.Output, CacheRead: sum.CacheRead, CacheWrite: sum.CacheWrite,
		USD: sum.USD, Unpriced: sum.Unpriced,
	}
}

// line is the one line of text the verdict block says it in:
// "31.2M tokens ($12.34) over 3 tickets with 2 subagents". The dollars show
// when above 0, the subagents when there are any, and the models that have no
// price end it. It has no colon and, with at most two unpriced models, no
// comma, so axi.Quote leaves it be.
func (s *nextChartSpend) line() string {
	out := spend.Compact(s.Tokens) + " tokens"
	if s.USD > 0 {
		out += " (" + spend.Dollars(s.USD) + ")"
	}
	out += " over " + countOf(s.Tickets, "ticket")
	if s.Agents > 0 {
		out += " with " + countOf(s.Agents, "subagent")
	}
	if len(s.Unpriced) > 0 {
		out += "; no price for " + andList(s.Unpriced)
	}
	return out
}

// countOf is "1 ticket" and "3 tickets".
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
