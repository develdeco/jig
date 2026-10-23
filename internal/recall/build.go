package recall

import (
	"sort"
	"strings"
)

// Blocker is one edge out of a ticket: the ticket it waits on, resolved to
// that ticket's current id, and the kind of wait ("merged" or "stacked").
type Blocker struct {
	Ticket string
	Kind   string
}

// Ticket is one ticket of the store as the caller read it: its record, the
// state the readiness rules gave it and whether it shipped (published).
type Ticket struct {
	ID        string
	Title     string
	Body      string
	State     string
	Shipped   bool
	BlockedBy []Blocker
}

// Open is an open (not yet shipped) ticket in a report.
type Open struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	State string `json:"state"`
}

// Edge is a blocked_by edge out of an open ticket. BlockerShipped says the
// ticket it waits on already shipped; Dangling that the store has no such
// ticket.
type Edge struct {
	Ticket         string `json:"ticket"`
	BlockedBy      string `json:"blocked_by"`
	Kind           string `json:"kind,omitempty"`
	BlockerShipped bool   `json:"blocker_shipped,omitempty"`
	Dangling       bool   `json:"dangling,omitempty"`
}

// Duplicate is an open ticket that comes near the query: Score is the Jaccard
// index of the query's words against the ticket's title or its body, whichever
// is higher, and In says which.
type Duplicate struct {
	Ticket string  `json:"ticket"`
	Title  string  `json:"title,omitempty"`
	State  string  `json:"state"`
	Score  float64 `json:"score"`
	In     string  `json:"in"`
}

// MaxAnswerEntries is the most ledger entries a report carries.
const MaxAnswerEntries = 10

// Report is what recall tells a planner. Every list is present, empty when it
// has nothing, so a reader of the JSON never meets null.
type Report struct {
	// Query is what the report was asked about; empty for a report of the
	// store alone.
	Query string `json:"query,omitempty"`
	// Tickets are the open tickets, in store order; Shipped counts the
	// tickets that are not among them.
	Tickets []Open `json:"tickets"`
	Shipped int    `json:"shipped"`
	// Edges are the blocked_by edges out of the open tickets.
	Edges []Edge `json:"edges"`
	// Duplicates are the open tickets that come near the query, best first;
	// always empty without a query.
	Duplicates []Duplicate `json:"duplicates"`
	// Fog is the not-yet-specified part of each chart that has one.
	Fog []Fog `json:"fog"`
	// Platform are the platform documents.
	Platform []Doc `json:"platform"`
	// Answers are the ledger entries that have answers, newest first: the
	// latest MaxAnswerEntries, or with a query the ones that share a word
	// with it, the most shared first.
	Answers []LedgerEntry `json:"answers"`
}

// Build assembles the report for query (which may be empty) over tickets, all
// of the store, and the Material read from its files.
func Build(tickets []Ticket, m Material, query string) Report {
	query = strings.TrimSpace(query)
	r := Report{
		Query: query, Tickets: []Open{}, Edges: []Edge{}, Duplicates: []Duplicate{},
		Fog: []Fog{}, Platform: []Doc{}, Answers: []LedgerEntry{},
	}
	known := map[string]Ticket{}
	for _, t := range tickets {
		known[t.ID] = t
	}
	var open []Ticket
	for _, t := range tickets {
		if t.Shipped {
			r.Shipped++
			continue
		}
		open = append(open, t)
		r.Tickets = append(r.Tickets, Open{ID: t.ID, Title: t.Title, State: t.State})
		for _, b := range t.BlockedBy {
			blocker, ok := known[b.Ticket]
			r.Edges = append(r.Edges, Edge{
				Ticket: t.ID, BlockedBy: b.Ticket, Kind: b.Kind,
				BlockerShipped: ok && blocker.Shipped, Dangling: !ok,
			})
		}
	}
	r.Duplicates = Duplicates(query, open)
	for _, c := range m.Charts {
		items := FogItems(c.Map)
		if len(items) == 0 {
			continue
		}
		f := Fog{Chart: c.Name, Items: []string{}}
		for i, it := range items {
			if i == MaxFogItems {
				f.More = len(items) - MaxFogItems
				break
			}
			f.Items = append(f.Items, clip(it, MaxItemRunes))
		}
		r.Fog = append(r.Fog, f)
	}
	q := words(query)
	for i, d := range m.Docs {
		if i == MaxDocs {
			break
		}
		r.Platform = append(r.Platform, docOf(d, q))
	}
	r.Answers = answerTails(ParseLedger(m.Ledger), q)
	return r
}

// Duplicates returns the tickets whose title or body comes near query: those
// at or above Threshold, best first (equal scores keep the order the tickets
// came in). An empty query has none.
func Duplicates(query string, tickets []Ticket) []Duplicate {
	q := words(query)
	out := []Duplicate{}
	if len(q) == 0 {
		return out
	}
	for _, t := range tickets {
		score, in := jaccard(q, words(t.Title)), "title"
		if b := jaccard(q, words(t.Body)); b > score {
			score, in = b, "body"
		}
		if score >= Threshold {
			out = append(out, Duplicate{Ticket: t.ID, Title: t.Title, State: t.State, Score: round2(score), In: in})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// answerTails picks the ledger entries a report carries. Entries without
// answers are of no use to a planner and are left out. Without a query the
// latest MaxAnswerEntries come, newest first; with one, the entries whose
// title and answers share a word with it, the most words first and the newest
// first among equals.
func answerTails(entries []LedgerEntry, query map[string]bool) []LedgerEntry {
	type scored struct {
		e     LedgerEntry
		score int
		age   int // 0 is the newest
	}
	var cand []scored
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if len(e.Answers) == 0 {
			continue
		}
		s := scored{e: e, age: len(entries) - 1 - i}
		if len(query) > 0 {
			text := e.Title
			for _, a := range e.Answers {
				text += " " + a.Text
			}
			if s.score = shares(query, text); s.score == 0 {
				continue
			}
		}
		cand = append(cand, s)
	}
	sort.SliceStable(cand, func(i, j int) bool {
		if cand[i].score != cand[j].score {
			return cand[i].score > cand[j].score
		}
		return cand[i].age < cand[j].age
	})
	out := []LedgerEntry{}
	for _, c := range cand {
		if len(out) == MaxAnswerEntries {
			break
		}
		e := c.e
		answers := make([]Answer, len(e.Answers))
		for i, a := range e.Answers {
			answers[i] = Answer{ID: a.ID, Text: clip(a.Text, MaxItemRunes)}
		}
		e.Answers = answers
		out = append(out, e)
	}
	return out
}
