package main

import (
	"github.com/develdeco/jig/internal/store"
)

// refreshMerges asks the host, through merges, for the pull request state of
// every ticket that published and is not closed, as far as this machine's kept
// one is older than the listing's age; a state that merged is kept for good and
// not asked again. It prints nothing and says how many tickets it looked at. It
// is what the screens run in the background so the roster, which asks the host
// nothing, reads fresh states.
func refreshMerges(st *store.Store, merges mergeSource) int {
	ids, err := st.Tickets()
	if err != nil {
		return 0
	}
	n := 0
	for _, id := range ids {
		rec, err := st.ReadTicket(id)
		if err != nil || rec.Closed != nil {
			continue
		}
		if published, err := isPublished(st, id); err != nil || !published {
			continue
		}
		_, _ = merges.Of(id)
		n++
	}
	return n
}
