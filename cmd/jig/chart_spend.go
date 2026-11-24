package main

import (
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/recall"
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

// nextChartRound is what one ticket of the chart took in review: its reviewed
// rounds and whether it went back to build after publish, read by
// recall.FactsOf, the same reading `jig recall` shows in its receipts.
type nextChartRound struct {
	Ticket      string `json:"ticket"`
	Reviewed    int    `json:"reviewed"`
	BackToBuild bool   `json:"back_to_build"`
}

// chartFigures folds what the journals of the chart's tickets say, each read
// once: their spend, and the reviewed rounds of each ticket that took any. The
// spend is done per ticket and not over all the lines at once, because one
// session writes lines on several tickets and spend.Latest would keep only the
// newest of them. A ticket whose journal cannot be read adds nothing: a problem
// is a row of the chart, never a reason to hide it. The spend is nil when no
// ticket has any and the rounds nil when no ticket took a reviewed round.
func chartFigures(st *store.Store, c phase.Chart) (*nextChartSpend, []nextChartRound) {
	var each []spend.Totals
	var rounds []nextChartRound
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
		if f := recall.FactsOf(lines); f.Rounds > 0 {
			rounds = append(rounds, nextChartRound{Ticket: r.ID, Reviewed: f.Rounds, BackToBuild: f.Back})
		}
	}
	if len(each) == 0 {
		return nil, rounds
	}
	sum := spend.Sum(each...)
	return &nextChartSpend{
		Tickets: len(each), Agents: sum.Agents,
		Tokens: sum.Tokens(), Input: sum.Input, Output: sum.Output, CacheRead: sum.CacheRead, CacheWrite: sum.CacheWrite,
		USD: sum.USD, Unpriced: sum.Unpriced,
	}, rounds
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

// roundsLine is the one line of text the verdict block says the rounds in:
// "A-1 2; A-2 1; A-3 3 back to build", a ticket's id and its reviewed rounds,
// then "back to build" when it went back after publish, with no total and no
// mark. The entries are told apart by semicolons, not commas, so axi.Quote
// leaves the line be.
func roundsLine(rounds []nextChartRound) string {
	parts := make([]string, len(rounds))
	for i, r := range rounds {
		parts[i] = r.Ticket + " " + strconv.Itoa(r.Reviewed)
		if r.BackToBuild {
			parts[i] += " back to build"
		}
	}
	return strings.Join(parts, "; ")
}
