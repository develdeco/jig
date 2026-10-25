package ready

import (
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
	// StateBlocked: a blocker in its ticket.yaml is not met: it has not
	// published, or its work has not merged.
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
	// Merge is whether the ticket's work has merged, as its caller read it
	// (the recorded pull request, the branch). A merged blocker is met by it;
	// nil when the caller read none, and then having published is all that is
	// known of the ticket. A stacked blocker is met by publishing either way.
	Merge  *Merge
	Slices []store.Slice
	States map[string]store.SliceState
}

// Status is the readiness of one ticket.
type Status struct {
	ID    string
	State string
	// Blocked is the blockers that are not met, in ticket.yaml order, and Why
	// says what each is missing, keyed by the blocking ticket.
	Blocked []Blocker
	Why     map[string]string
	// Ready is the ids of the slices ready to build, in slices.yaml order;
	// none for a ticket that is done or blocked.
	Ready []string
}

// Tickets works out the readiness of each of tickets, in the order given. A
// merged blocker is met when it is one of tickets and its work has merged
// (Ticket.Merge), or, with no merge read for it, when it has published. A
// stacked blocker is met by publishing. One that is not among tickets counts
// as unmet, so a ticket never reads as free because its blocker went missing.
func Tickets(tickets []Ticket) []Status {
	byID := make(map[string]Ticket, len(tickets))
	for _, t := range tickets {
		byID[t.ID] = t
	}
	out := make([]Status, 0, len(tickets))
	for _, t := range tickets {
		st := Status{ID: t.ID}
		for _, b := range t.Blockers {
			if why := unmet(b, byID); why != "" {
				st.Blocked = append(st.Blocked, b)
				if st.Why == nil {
					st.Why = map[string]string{}
				}
				st.Why[b.Ticket] = why
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

// unmet is what blocker b is missing, "" when it is met.
func unmet(b Blocker, byID map[string]Ticket) string {
	t, ok := byID[b.Ticket]
	switch {
	case !ok:
		return "it is not a ticket of the store"
	case b.Kind != "stacked" && t.Merge != nil:
		return t.Merge.Why()
	case !t.Published:
		return NotPublished
	}
	return ""
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
