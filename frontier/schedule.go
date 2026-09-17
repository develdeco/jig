package frontier

import "github.com/develdeco/jig/store"

// Schedule groups slices into batches for the frontier loop: each inner
// slice-id list belongs to one repo and runs serially, in the order slices
// appears; the outer list of batches runs concurrently, one goroutine per
// repo. wsRepo maps a slice's workspace id to its repo name.
func Schedule(slices []store.Slice, wsRepo map[string]string) [][]string {
	var order []string
	groups := map[string][]string{}
	for _, s := range slices {
		repo := wsRepo[s.Workspace]
		if _, ok := groups[repo]; !ok {
			order = append(order, repo)
		}
		groups[repo] = append(groups[repo], s.ID)
	}
	out := make([][]string, 0, len(order))
	for _, repo := range order {
		out = append(out, groups[repo])
	}
	return out
}
