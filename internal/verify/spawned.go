package verify

import (
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/workerlive"
)

// SpawnWindow is how far back the ledger is searched for a worker's spawned
// line. A pending verification older than that has no spawned line to find
// and is judged by its age.
const SpawnWindow = 1 << 20

// LatestSpawned reads the ledger once, backward, and returns the latest
// verify-spawned line of the ticket for each of the wanted (commit, slice)
// pairs that has one. The walk stops as soon as every pair is found; with
// none wanted it reads nothing.
func LatestSpawned(jigHome, ticket string, want []phase.CommitRef) (map[phase.CommitRef]phase.Spawned, error) {
	out := map[phase.CommitRef]phase.Spawned{}
	if len(want) == 0 {
		return out, nil
	}
	wanted := map[phase.CommitRef]bool{}
	for _, w := range want {
		wanted[w] = true
	}
	err := ledger.ReadBackward(jigHome, SpawnWindow, func(l journal.Line) bool {
		if l.Event != journal.EventVerifySpawned || l.Ticket != ticket || l.PID <= 0 {
			return true
		}
		ref := phase.CommitRef{SHA: l.Commit, Slice: l.Slice}
		if _, done := out[ref]; !wanted[ref] || done {
			return true
		}
		at, err := time.Parse(time.RFC3339Nano, l.TS)
		if err != nil {
			return true
		}
		out[ref] = phase.Spawned{PID: l.PID, At: at}
		return len(out) < len(wanted)
	})
	return out, err
}

// Alive is the liveness check a derivation of verifications uses when its
// Deps do not name one.
func Alive(pid int, notBefore time.Time) bool { return workerlive.Alive(pid, notBefore) }
