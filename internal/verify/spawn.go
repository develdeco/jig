package verify

import (
	"errors"
	"fmt"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/spawn"
	"github.com/develdeco/jig/internal/store"
)

// WorkerArgs is the command line of the detached worker for r, after the
// program: `verify --worker` with the store, the clone, the slice, who asked
// and whether to rerun, then the ticket and the commit. `jig verify` parses
// the same line, so this is the one place that spells it.
func WorkerArgs(d Deps, r Request, sha string) []string {
	args := []string{"verify", "--worker", "--store", d.Store.Root, "--repo-dir", d.Clone, "--slice", r.Slice}
	if r.Session != "" {
		args = append(args, "--session", r.Session)
	}
	if r.Agent != "" {
		args = append(args, "--agent", r.Agent)
	}
	if r.Rerun {
		args = append(args, "--rerun")
	}
	return append(args, r.Ticket, sha)
}

// Spawned is what Spawn found or started.
type Spawned struct {
	Ticket string
	Slice  string
	// SHA is the full sha of the commit.
	SHA string
	// Existing is true when the journal already held a verification of the
	// commit for the slice (or a worker is already on it) and Rerun was not
	// set: nothing was started. State and Attempt are that verification's.
	Existing bool
	State    string
	Attempt  int
	// PID is the worker's process id, 0 when none was started.
	PID int
	// Log is the file the worker writes to.
	Log string
	// Warning is a problem that did not stop the worker from starting: the
	// ledger line that records its pid could not be written.
	Warning string
}

// Spawn starts the detached worker that verifies r, unless the journal
// already holds a verification of the commit for the slice (any state, a
// pending one included) and Rerun is not set, or a worker holds its lock
// (Rerun or not): then it starts nothing, records nothing and says what is
// there. The
// worker is the jig binary (Deps.Executable) run as `jig verify --worker`
// in the operator's clone, started through Deps.Start with its output going
// to the verification's log; Spawn then writes the ledger's verify-spawned
// line with its pid. The worker records the verify-pending line itself, as
// the first thing it does under its lock, so the work is recorded from the
// worker's side whatever starts it.
//
// Two Spawns made close together may both start a worker; the workers take
// the lock one after the other and the second finds the first's record, so
// the verification still happens once.
func Spawn(d Deps, r Request) (Spawned, error) {
	x, err := d.prepare(r)
	out := Spawned{Ticket: r.Ticket, Slice: r.Slice, SHA: x.res.SHA}
	if err != nil {
		return out, err
	}
	switch {
	case d.Executable == "":
		return out, errors.New("verify: no jig binary to start as the worker")
	case d.Start == nil:
		return out, errors.New("verify: no way to start a detached process")
	}

	// A worker holds the lock for as long as it runs: probe it without
	// waiting. A rerun cannot take the place of a run that is under way.
	release, held, err := store.TryLock(x.dir, 0)
	if err != nil {
		return out, fmt.Errorf("verify: lock %s: %w", x.dir, err)
	}
	release()
	if !held {
		out.Existing, out.State = true, journal.VerifyPending
		return out, nil
	}
	lines, err := journal.Read(d.Store, r.Ticket)
	if err != nil {
		return out, fmt.Errorf("verify: read the journal of %s: %w", r.Ticket, err)
	}
	if x.existing(lines) {
		out.Existing, out.State, out.Attempt = true, x.res.State, x.res.Attempt
		return out, nil
	}

	if out.Log, err = logPath(d.JigHome, x.storeID, r.Ticket, x.sha, r.Slice); err != nil {
		return out, err
	}
	pid, err := d.Start(spawn.Spec{
		Path:    d.Executable,
		Args:    WorkerArgs(d, r, x.sha),
		Dir:     d.Clone,
		LogPath: out.Log,
	})
	if err != nil {
		return out, fmt.Errorf("verify: start the worker: %w", err)
	}
	out.PID = pid
	if err := ledger.Append(d.JigHome, journal.Line{
		Ticket:  r.Ticket,
		Slice:   r.Slice,
		Event:   journal.EventVerifySpawned,
		Commit:  x.sha,
		PID:     pid,
		Session: r.Session,
		Agent:   r.Agent,
	}); err != nil {
		out.Warning = fmt.Sprintf("the worker (pid %d) is running but the ledger could not record it: %v", pid, err)
	}
	return out, nil
}
