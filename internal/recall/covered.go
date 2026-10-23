package recall

import (
	"sort"
)

// Covered is shipped work that a goal comes near: a shipped ticket whose title
// does, or a ledger entry whose heading does (a ticket published before its
// record carried a title is known by its ledger entry alone).
type Covered struct {
	Ticket string  `json:"ticket"`
	Title  string  `json:"title"`
	Score  float64 `json:"score"`
	// Source is "ticket" for the title in a shipped ticket's record and
	// "ledger" for the heading of its ledger entry.
	Source string `json:"source"`
}

// MaxCovered is the most shipped works one goal is reported against.
const MaxCovered = 3

// CoveredBy returns the shipped tickets and ledger entries whose title comes
// near goal: those at or above Threshold, best first, one per ticket (its
// better score wins) and at most MaxCovered. shipped holds the tickets of the
// store that shipped; skip names a ticket to leave out, the one the goal
// belongs to.
func CoveredBy(goal string, shipped []Ticket, ledger []LedgerEntry, skip string) []Covered {
	g := words(goal)
	best := map[string]Covered{}
	var order []string
	consider := func(ticket, title, source string) {
		if ticket == "" || ticket == skip {
			return
		}
		score := jaccard(g, words(title))
		if score < Threshold {
			return
		}
		prev, seen := best[ticket]
		if !seen {
			order = append(order, ticket)
		}
		if !seen || score > prev.Score {
			best[ticket] = Covered{Ticket: ticket, Title: title, Score: round2(score), Source: source}
		}
	}
	for _, t := range shipped {
		consider(t.ID, t.Title, "ticket")
	}
	for _, e := range ledger {
		consider(e.Ticket, e.Title, "ledger")
	}
	out := make([]Covered, 0, len(order))
	for _, id := range order {
		out = append(out, best[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > MaxCovered {
		out = out[:MaxCovered]
	}
	return out
}
