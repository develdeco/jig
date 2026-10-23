package main

import (
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/spend"
	"github.com/develdeco/jig/internal/store"
)

// init registers the spend block of `jig status` (sections.go). The block is
// there once the ticket has spend recorded or a budget set, and absent
// before, so a ticket nobody has measured reads as it always did.
func init() {
	registerStatusSection(statusSection{Order: 390, Name: "spend", Blocks: spendStatusBlocks})
}

func spendStatusBlocks(st *store.Store, ticket string) ([]string, error) {
	block, ok, err := spendBlock(st, ticket)
	if err != nil || !ok {
		return nil, err
	}
	return []string{block}, nil
}

// spendBlock renders what ticket has spent, summed from the latest spend line
// of each of its sessions, and how that stands against its budget record.
// ok is false when there is neither spend nor a budget to show.
//
//	spend:
//	  sessions: 2
//	  tokens: 31200000
//	  input: 162
//	  output: 429129
//	  cache_write: 1073306
//	  cache_read: 29664647
//	  usd: 12.34
//	  budget_tokens: 5000000
//	  budget_usd: 10.00
//	  budget: over
//
// tokens is the sum of the four counts below it. budget is none, ok, near
// (at 80% of a limit) or over; budget_tokens and budget_usd are the limits
// that are set. unpriced names the models whose tokens are counted but whose
// dollars are not, because no price is known for them.
func spendBlock(st *store.Store, ticket string) (string, bool, error) {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return "", false, err
	}
	budget, recorded, err := spend.ReadBudget(st, ticket)
	if err != nil {
		return "", false, err
	}
	t := spend.FromLines(lines)
	limited := recorded && spend.Limited(budget)
	if t.Sessions == 0 && !limited {
		return "", false, nil
	}
	pairs := [][2]string{
		{"sessions", strconv.Itoa(t.Sessions)},
		{"tokens", strconv.FormatInt(t.Tokens(), 10)},
		{"input", strconv.FormatInt(t.Input, 10)},
		{"output", strconv.FormatInt(t.Output, 10)},
		{"cache_write", strconv.FormatInt(t.CacheWrite, 10)},
		{"cache_read", strconv.FormatInt(t.CacheRead, 10)},
		{"usd", strconv.FormatFloat(t.USD, 'f', 2, 64)},
	}
	if limited {
		if budget.Tokens > 0 {
			pairs = append(pairs, [2]string{"budget_tokens", strconv.FormatInt(budget.Tokens, 10)})
		}
		if budget.USD > 0 {
			pairs = append(pairs, [2]string{"budget_usd", strconv.FormatFloat(budget.USD, 'f', 2, 64)})
		}
	}
	pairs = append(pairs, [2]string{"budget", string(spend.Check(t, budget).State)})
	if len(t.Unpriced) > 0 {
		pairs = append(pairs, [2]string{"unpriced", strings.Join(t.Unpriced, ",")})
	}
	return axi.KV("spend", pairs), true, nil
}
