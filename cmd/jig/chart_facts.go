package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/recall"
	"github.com/develdeco/jig/internal/store"
)

// chartMap is what a chart's map.md says, read for the chart's standing: its
// sections, and whether there is a map.md at all. jig reads it, never writes it.
type chartMap struct {
	recall.MapSections
	Exists bool
}

// deriveChart is where chart name stands (internal/phase's DeriveChart) over
// the facts chartFacts gathered, with the sections of its map.md. Every command
// that says where a chart stands asks it, so `jig next <chart>`, `jig status
// <chart>` and the TUI's chart screen agree.
func deriveChart(st *store.Store, _ project.Config, name string, merges mergeSource) (phase.Chart, chartMap, error) {
	facts, err := chartFacts(st, name, merges)
	if err != nil {
		return phase.Chart{}, chartMap{}, err
	}
	m := chartMap{MapSections: recall.ParseMap("")}
	if data, err := os.ReadFile(st.ChartMapFile(name)); err == nil {
		m = chartMap{MapSections: recall.ParseMap(string(data)), Exists: true}
	}
	return phase.DeriveChart(facts), m, nil
}

// chartFacts reads each entry of chart name's tickets.yaml with what the store
// says of it. A graduated entry takes its edges and rank from its ticket, the
// source of truth once it is minted (graduate only advises on a drift); one with
// no id yet takes them from the chart's refs, which must resolve as graduate
// resolves them. A ticket that cannot be read is a row with its problem, never
// a reason to hide the chart. Each edge is judged as a claim judges it
// (blockerProblem), so a stacked edge is met by the parent's clean gate round
// and a merged one by what merges says.
func chartFacts(st *store.Store, name string, merges mergeSource) (phase.ChartFacts, error) {
	entries, err := st.ReadChart(name)
	if err != nil {
		return phase.ChartFacts{}, &axi.Error{
			Msg:  fmt.Sprintf("failed to read chart %q: %v", name, err),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Check that charts/%s/tickets.yaml exists and is valid YAML", name)},
		}
	}
	// The refs of a graduated entry are history: its ticket holds the edges.
	scoped := make([]store.ChartEntry, len(entries))
	copy(scoped, entries)
	for i := range scoped {
		if scoped[i].ID != "" {
			scoped[i].BlockedBy = nil
		}
	}
	refs, err := resolveChartEntryRefs(st, name, scoped)
	if err != nil {
		return phase.ChartFacts{}, err
	}

	facts := phase.ChartFacts{Name: name}
	for i, e := range entries {
		pos := i + 1
		ce := phase.ChartEntry{Pos: pos, ID: e.ID, Title: e.Title}
		if e.ID == "" {
			for _, r := range refs[i] {
				if r.knownID != "" {
					ce.Needs = append(ce.Needs, chartNeed(st, fmt.Sprintf("%s#%d", name, pos), r.knownID, r.kind, merges))
					continue
				}
				ce.Needs = append(ce.Needs, phase.ChartNeed{
					Pos: r.pending, Kind: r.kind,
					Why: fmt.Sprintf("%s#%d waits for entry #%d, which has no ticket yet", name, pos, r.pending),
				})
			}
			facts.Entries = append(facts.Entries, ce)
			continue
		}
		rec, err := st.ReadTicket(e.ID)
		if err != nil {
			ce.Problem = err.Error()
			facts.Entries = append(facts.Entries, ce)
			continue
		}
		if rec.Title != "" {
			ce.Title = rec.Title
		}
		ce.Rank = rec.Rank
		for _, b := range rec.BlockedBy {
			blocker := b.Ticket
			if resolved, err := st.ResolveTicket(b.Ticket); err == nil {
				blocker = resolved
			}
			ce.Needs = append(ce.Needs, chartNeed(st, e.ID, blocker, b.Kind, merges))
		}
		if t, err := derivePhaseWith(st, e.ID, merges); err != nil {
			ce.Problem = err.Error()
		} else {
			ce.Ticket = &t
		}
		facts.Entries = append(facts.Entries, ce)
	}
	return facts, nil
}

// chartNeed is the edge from dependent (a ticket, or a chart entry that has no
// ticket yet) to blocker, judged as a claim judges it. A blocker the store
// cannot read is a need that is never met.
func chartNeed(st *store.Store, dependent, blocker, kind string, merges mergeSource) phase.ChartNeed {
	if kind == "" {
		kind = "merged"
	}
	n := phase.ChartNeed{Ticket: blocker, Kind: kind}
	p, err := blockerProblem(st, dependent, blocker, kind, claimDeps{merges: merges})
	switch {
	case err != nil:
		n.Code, n.Why = phase.CodeNeedUnknown, fmt.Sprintf("%s waits for %s, which cannot be read: %v", dependent, blocker, err)
	case p == nil:
		n.Met = true
	default:
		n.Code, n.Why, n.PRs = p.Code, p.Message, mergePRs(merges, blocker, kind)
	}
	return n
}

// chartNamed is the chart a word names: a ticket of that name (or alias) wins,
// then a chart that has a tickets.yaml. A ticket id is KEY-n and a chart name a
// word, so the two seldom clash; when they do the ticket is the answer.
func chartNamed(st *store.Store, word string) (string, bool) {
	if word == "" {
		return "", false
	}
	if id, err := st.ResolveTicket(word); err != nil || checkTicketID(id) != nil || st.TicketExists(id) {
		return "", false
	}
	if validateChartName(word) != nil {
		return "", false
	}
	if _, err := os.Stat(st.ChartFile(word)); err != nil {
		return "", false
	}
	return word, true
}

// orChartHint is err, the refusal for a word that is no ticket of the store,
// made to name charts too when the store has any: the word may have been meant
// as one. Any other error, or a store with no charts, is err as it was.
func orChartHint(st *store.Store, word string, err error) error {
	if err == nil {
		return err
	}
	names, cerr := st.ChartNames()
	if cerr != nil || len(names) == 0 || checkTicketID(word) != nil || st.TicketExists(word) {
		return err
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("no ticket or chart named %q in the store at %s", word, st.Root),
		Code: "VALIDATION_ERROR",
		Help: []string{
			"Run `jig next` to list the tickets",
			"The charts are " + strings.Join(names, ", ") + ": `jig next <chart>` says where one stands",
		},
	}
}
