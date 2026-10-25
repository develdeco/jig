package main

import (
	"time"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/mergefacts"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

// listingMergeAge is how long `jig next` over the whole store takes an open or
// closed pull request on this machine's word before asking the host again: a
// listing of many tickets must not wait on the network for each blocker. A
// merged pull request is never asked again, and a command about one ticket (a
// claim, `jig next <ticket>`) asks about the pull requests it is held on every
// time. An answer older than this run's is dated where it is shown.
const listingMergeAge = 2 * time.Minute

// newMergeReader reads whether the work of a ticket has merged
// (internal/mergefacts), for the project cfg of st. Pull request states come
// from the repo host through gh, and are kept on this machine under jigHome;
// maxAge is how long an open or closed one stands before the host is asked
// again (0: every time).
func newMergeReader(e env, st *store.Store, cfg project.Config, mp project.MachineProject, jigHome string, maxAge time.Duration) *mergefacts.Reader {
	gh, _ := e.lookPath("gh")
	cache := &mergefacts.PRCache{
		Ask:    func(url string) (string, bool) { return repohost.PRState(gh, nil, url) },
		MaxAge: maxAge,
		Now:    e.now,
	}
	if jigHome != "" {
		cache.Path = home.PullRequestStatesPath(jigHome)
	}
	return &mergefacts.Reader{Store: st, Cfg: cfg, Clones: mp.Clones, PRs: cache}
}

// readMerges reads, for the tickets of inputs that have not published, the
// merge fact of each blocker that is a ticket of inputs, has published and is
// not stacked (a stacked blocker is met by publishing, and one that has not
// published has no merge to look for), and lays it on that input. A fact that
// cannot be read is one that is not met, saying why, so a ticket never reads as
// free because its blocker could not be looked at. nil merges reads none.
func readMerges(merges mergeSource, inputs []ready.Ticket) {
	if merges == nil {
		return
	}
	at := make(map[string]int, len(inputs))
	for i, in := range inputs {
		at[in.ID] = i
	}
	for _, in := range inputs {
		if in.Published {
			continue
		}
		for _, b := range in.Blockers {
			i, ok := at[b.Ticket]
			if !ok || b.Kind == "stacked" || !inputs[i].Published || inputs[i].Merge != nil {
				continue
			}
			m, err := merges.Of(b.Ticket)
			if err != nil {
				m = ready.Merge{Published: true, Repos: []ready.RepoMerge{{Why: "its merge could not be read: " + err.Error()}}}
			}
			inputs[i].Merge = &m
		}
	}
}

// blockerFact is the blocker b that is not met, with what it is missing (why)
// and the time of the host's answer behind that when it was not read in this
// run, from the merge fact laid on its input.
func blockerFact(b ready.Blocker, why string, inputs []ready.Ticket) nextBlocker {
	nb := nextBlocker{Ticket: b.Ticket, Kind: b.Kind, Fact: why}
	for _, in := range inputs {
		if in.ID == b.Ticket && in.Merge != nil {
			if at := in.Merge.AsOf(); !at.IsZero() {
				nb.AsOf = at.UTC().Format(time.RFC3339)
			}
		}
	}
	return nb
}
