package ready

import (
	"slices"
	"sort"

	"github.com/develdeco/jig/internal/store"
)

// Ranked is a ticket as the owner's priority order sees it: its id and its
// rank (ticket.yaml's rank), nil when the owner gave it none.
type Ranked struct {
	ID   string
	Rank *int
}

// ByPriority returns the ids of tickets in the order to start them: by rank,
// lowest first, and the unranked after every ranked one. Tickets that hold
// the same rank, and the unranked among themselves, keep the order they were
// given in, so a caller that passes them by id gets rank, then id.
func ByPriority(tickets []Ranked) []string {
	ordered := slices.Clone(tickets)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i].Rank, ordered[j].Rank
		if a == nil || b == nil {
			return a != nil
		}
		return *a < *b
	})
	ids := make([]string, len(ordered))
	for i, t := range ordered {
		ids[i] = t.ID
	}
	return ids
}

// SharedRank is a rank that more than one ticket holds: the owner has not
// said which of them comes first.
type SharedRank struct {
	Rank    int
	Tickets []string
}

// SharedRanks lists, lowest rank first, every rank held by more than one of
// tickets with the ids that hold it, in the order they were given.
func SharedRanks(tickets []Ranked) []SharedRank {
	held := map[int][]string{}
	for _, t := range tickets {
		if t.Rank != nil {
			held[*t.Rank] = append(held[*t.Rank], t.ID)
		}
	}
	var out []SharedRank
	for rank, ids := range held {
		if len(ids) > 1 {
			out = append(out, SharedRank{Rank: rank, Tickets: ids})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	return out
}

// Site is one workspace of one repo: the place a slice builds in.
type Site struct {
	Repo      string
	Workspace string
}

// Sites lists, in the order the slices name them and each once, the sites that
// the slices build in, as Place put them. A workspace that no repo owns, or
// that more than one declares, is no site: it is not known which repo's it is.
func Sites(all []store.Slice, p Placement) []Site {
	var out []Site
	for _, s := range all {
		repo := p.Repo[s.Workspace]
		if repo == "" || repo == Unplaced {
			continue
		}
		if site := (Site{Repo: repo, Workspace: p.ID[s.Workspace]}); !slices.Contains(out, site) {
			out = append(out, site)
		}
	}
	return out
}

// Footprint is a ticket and the sites its slices build in.
type Footprint struct {
	Ticket string
	Sites  []Site
}

// Overlap is a site that two tickets both build in, so a scheduler should not
// run them at once.
type Overlap struct {
	Site
	// Tickets are the two, in the order they were given.
	Tickets [2]string
}

// Overlaps lists every site that two of footprints both build in, one entry
// for each pair of tickets and site: the pairs in the order of footprints, and
// for a pair the sites in the order the earlier ticket's slices name them.
func Overlaps(footprints []Footprint) []Overlap {
	var out []Overlap
	for i, a := range footprints {
		for _, b := range footprints[i+1:] {
			for _, site := range a.Sites {
				if slices.Contains(b.Sites, site) {
					out = append(out, Overlap{Site: site, Tickets: [2]string{a.Ticket, b.Ticket}})
				}
			}
		}
	}
	return out
}
