package ready

import (
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/store"
)

// ComputeReady returns, in slices.yaml order, every slice that is queued and
// whose every blocking slice is green: the set that can be built right now.
// A slice that is building, parked (needs-input), env-blocked or stalled is
// not ready, and neither is one that waits on a slice that is not green.
// states maps a slice id to its state; a slice with no entry reads as not
// queued, so it is never ready.
func ComputeReady(slices []store.Slice, states map[string]store.SliceState) []store.Slice {
	var out []store.Slice
	for _, s := range slices {
		if states[s.ID].State != "queued" {
			continue
		}
		ok := true
		for _, dep := range s.BlockedBy {
			if states[dep].State != "green" {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, s)
		}
	}
	return out
}

// Schedule groups slices into batches: each inner slice-id list belongs to
// one repo and runs serially, in the order slices appears; the outer list of
// batches may run concurrently, one per repo. wsRepo maps a slice's
// workspace id to its repo name (Place builds it). Batches come in the order
// their repos first appear.
func Schedule(slices []store.Slice, wsRepo map[string]string) [][]string {
	batches := Batches(slices, wsRepo)
	out := make([][]string, 0, len(batches))
	for _, b := range batches {
		out = append(out, b.Slices)
	}
	return out
}

// Batch is one repo's serial run of slices, in build order.
type Batch struct {
	Repo   string
	Slices []string
}

// Batches is Schedule with each batch named by its repo.
func Batches(slices []store.Slice, wsRepo map[string]string) []Batch {
	var order []string
	groups := map[string][]string{}
	for _, s := range slices {
		repo := wsRepo[s.Workspace]
		if _, ok := groups[repo]; !ok {
			order = append(order, repo)
		}
		groups[repo] = append(groups[repo], s.ID)
	}
	out := make([]Batch, 0, len(order))
	for _, repo := range order {
		out = append(out, Batch{Repo: repo, Slices: groups[repo]})
	}
	return out
}

// Unplaced is the repo name Place gives a workspace no repo declares, so a
// batch of such slices still has a name to print.
const Unplaced = "?"

// Repo is one repo of a project and the workspace ids its manifest declares
// (its .claude/jig.yaml, or the detected default "root").
type Repo struct {
	Name       string
	Workspaces []string
}

// Placement is the workspace-to-repo map of one ticket's slices.
type Placement struct {
	// Repo maps a workspace id to the name of the repo that owns it. A
	// workspace no repo declares maps to Unplaced.
	Repo map[string]string
	// Unplaced lists, sorted, the workspace ids of the slices that no repo
	// declares.
	Unplaced []string
	// Ambiguous lists, sorted, the workspace ids that more than one repo
	// declares; the first repo in project order owns it.
	Ambiguous []string
}

// Place maps each workspace the slices name to the repo that owns it. A
// project with one repo gives that repo every workspace, declared or not
// (the frontier's own rule for a single-repo project); with several, a
// workspace belongs to the first repo, in project order, whose manifest
// declares it. A workspace that two repos declare is reported in Ambiguous
// and one nobody declares in Unplaced.
func Place(repos []Repo, slices []store.Slice) Placement {
	p := Placement{Repo: map[string]string{}}
	if len(repos) == 1 {
		for _, s := range slices {
			p.Repo[s.Workspace] = repos[0].Name
		}
		return p
	}
	owner := map[string]string{}
	ambiguous := map[string]bool{}
	for _, r := range repos {
		for _, ws := range r.Workspaces {
			if have, ok := owner[ws]; ok {
				if have != r.Name {
					ambiguous[ws] = true
				}
				continue
			}
			owner[ws] = r.Name
		}
	}
	for _, s := range slices {
		name, ok := owner[s.Workspace]
		if !ok {
			name = Unplaced
			p.Unplaced = appendOnce(p.Unplaced, s.Workspace)
		}
		p.Repo[s.Workspace] = name
		if ambiguous[s.Workspace] {
			p.Ambiguous = appendOnce(p.Ambiguous, s.Workspace)
		}
	}
	sort.Strings(p.Unplaced)
	sort.Strings(p.Ambiguous)
	return p
}

func appendOnce(list []string, v string) []string {
	for _, have := range list {
		if have == v {
			return list
		}
	}
	return append(list, v)
}

// Waves numbers each slice by the longest chain of slices it waits on: 0 for
// a slice that waits on none, one more than its deepest blocker otherwise.
// A blocker that is not one of the slices is ignored. A slice on a cycle, or
// waiting on one, gets -1.
func Waves(slices []store.Slice) map[string]int {
	byID := make(map[string]store.Slice, len(slices))
	for _, s := range slices {
		byID[s.ID] = s
	}
	const (
		unseen   = -3
		visiting = -2
	)
	wave := make(map[string]int, len(slices))
	for _, s := range slices {
		wave[s.ID] = unseen
	}
	var depth func(id string) int
	depth = func(id string) int {
		switch w := wave[id]; w {
		case unseen:
		case visiting:
			return -1
		default:
			return w
		}
		wave[id] = visiting
		best := 0
		for _, dep := range byID[id].BlockedBy {
			if _, ok := byID[dep]; !ok {
				continue
			}
			d := depth(dep)
			if d < 0 {
				best = -1
				break
			}
			if d+1 > best {
				best = d + 1
			}
		}
		wave[id] = best
		return best
	}
	for _, s := range slices {
		depth(s.ID)
	}
	return wave
}

// The state a ticket is in for `jig next`.
const (
	// StateDone: the ticket's pull requests were published (its journal has
	// publish-done).
	StateDone = "done"
	// StateBlocked: a blocker in its ticket.yaml has not published yet.
	StateBlocked = "blocked"
	// StateReady: not blocked, and at least one slice is ready to build.
	StateReady = "ready"
	// StateActive: not blocked, nothing ready, and work is in flight or
	// parked (a slice is building, waiting on an answer, stalled or
	// env-blocked, or waits on one that is).
	StateActive = "active"
	// StateGreen: every slice is green and the ticket is not published yet:
	// it waits for its gate and its publish.
	StateGreen = "green"
	// StateNew: not blocked and no slices yet: it needs a brief.
	StateNew = "new"
)

// Blocker is one blocker of a ticket: the blocking ticket's id and its kind
// (merged or stacked), as ticket.yaml records them.
type Blocker struct {
	Ticket string
	Kind   string
}

// Ticket is what the readiness of one ticket depends on.
type Ticket struct {
	ID string
	// Blockers are the ticket's own blockers, their ids already resolved to
	// current ids.
	Blockers []Blocker
	// Published is whether the ticket's journal has a publish-done line.
	Published bool
	Slices    []store.Slice
	States    map[string]store.SliceState
}

// Status is the readiness of one ticket.
type Status struct {
	ID    string
	State string
	// Blocked is the blockers that have not published, in ticket.yaml order.
	Blocked []Blocker
	// Ready is the ids of the slices ready to build, in slices.yaml order;
	// none for a ticket that is done or blocked.
	Ready []string
}

// Tickets works out the readiness of each of tickets, in the order given. A
// blocker is met when it is one of tickets and has published; one that is
// not among them counts as unmet, so a ticket never reads as free because
// its blocker went missing.
func Tickets(tickets []Ticket) []Status {
	published := make(map[string]bool, len(tickets))
	for _, t := range tickets {
		published[t.ID] = t.Published
	}
	out := make([]Status, 0, len(tickets))
	for _, t := range tickets {
		st := Status{ID: t.ID}
		for _, b := range t.Blockers {
			if !published[b.Ticket] {
				st.Blocked = append(st.Blocked, b)
			}
		}
		// A published ticket has nothing left to build and a blocked one
		// cannot start, so neither offers slices.
		if !t.Published && len(st.Blocked) == 0 {
			for _, s := range ComputeReady(t.Slices, t.States) {
				st.Ready = append(st.Ready, s.ID)
			}
		}
		switch {
		case t.Published:
			st.State = StateDone
		case len(st.Blocked) > 0:
			st.State = StateBlocked
		case len(t.Slices) == 0:
			st.State = StateNew
		case len(st.Ready) > 0:
			st.State = StateReady
		case allGreen(t.Slices, t.States):
			st.State = StateGreen
		default:
			st.State = StateActive
		}
		out = append(out, st)
	}
	return out
}

func allGreen(slices []store.Slice, states map[string]store.SliceState) bool {
	for _, s := range slices {
		if states[s.ID].State != "green" {
			return false
		}
	}
	return true
}

// JoinBlockers renders blockers as "JIG-1(merged)+JIG-2(stacked)", the form
// of a table cell; "-" when there are none.
func JoinBlockers(blockers []Blocker) string {
	if len(blockers) == 0 {
		return "-"
	}
	parts := make([]string, len(blockers))
	for i, b := range blockers {
		parts[i] = b.Ticket
		if b.Kind != "" {
			parts[i] += "(" + b.Kind + ")"
		}
	}
	return strings.Join(parts, "+")
}
