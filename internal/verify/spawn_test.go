package verify

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/spawn"
	"github.com/develdeco/jig/internal/store"
)

// starter is a Start that records the Specs it was asked for instead of
// starting anything.
type starter struct {
	pid int
	err error

	mu    sync.Mutex
	specs []spawn.Spec
}

func (s *starter) start(spec spawn.Spec) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.specs = append(s.specs, spec)
	return s.pid, s.err
}

func (s *starter) seen() []spawn.Spec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]spawn.Spec(nil), s.specs...)
}

// spawnRig is a rig whose Deps can Spawn through a recording starter.
func spawnRig(t *testing.T) (*rig, *starter) {
	t.Helper()
	r := newRig(t, fixture.Opts{})
	s := &starter{pid: 4242}
	r.deps.Executable = filepath.Join(t.TempDir(), "jig")
	r.deps.Start = s.start
	return r, s
}

// ledgerOf reads the machine ledger under the rig's jig home.
func ledgerOf(t *testing.T, r *rig) []journal.Line {
	t.Helper()
	lines, err := ledger.Read(r.fx.Home)
	if err != nil {
		t.Fatalf("ledger.Read: %v", err)
	}
	return lines
}

// TestSpawnStartsTheWorkerAndRecordsItsPid: the worker is the jig binary run
// as `jig verify --worker` in the operator's clone, its output going to the
// verification's log, and the ledger learns its pid. Spawn itself writes no
// journal line: the worker records the pending one.
func TestSpawnStartsTheWorkerAndRecordsItsPid(t *testing.T) {
	t.Parallel()
	r, s := spawnRig(t)
	sha := r.fixClamp()
	rq := r.request("a", sha[:12])

	got, err := Spawn(r.deps, rq)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	wantLog, err := LogPath(r.deps, r.fx.Ticket, sha, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Existing || got.PID != 4242 || got.SHA != sha || got.Log != wantLog || got.Warning != "" {
		t.Errorf("Spawned = %+v, want pid 4242 for %s logging to %s", got, sha, wantLog)
	}
	specs := s.seen()
	if len(specs) != 1 {
		t.Fatalf("started %d processes, want 1", len(specs))
	}
	spec := specs[0]
	wantArgs := []string{"verify", "--worker", "--store", r.st.Root, "--repo-dir", r.fx.RepoDir, "--slice", "a", "--session", "s-1", "--agent", "agent-1", r.fx.Ticket, sha}
	if spec.Path != r.deps.Executable || !reflect.DeepEqual(spec.Args, wantArgs) || spec.Dir != r.fx.RepoDir || spec.LogPath != wantLog || spec.Env != nil {
		t.Errorf("Spec = %+v\nwant args %v", spec, wantArgs)
	}
	if want := WorkerArgs(r.deps, rq, sha); !reflect.DeepEqual(spec.Args, want) {
		t.Errorf("Args = %v, WorkerArgs = %v", spec.Args, want)
	}
	if lines := r.lines(); len(lines) != 0 {
		t.Errorf("Spawn wrote journal lines %v, want none: the worker records them", events(lines))
	}
	led := ledgerOf(t, r)
	if len(led) != 1 {
		t.Fatalf("ledger lines = %+v, want one verify-spawned", led)
	}
	if l := led[0]; l.Event != journal.EventVerifySpawned || l.PID != 4242 || l.Ticket != r.fx.Ticket || l.Slice != "a" || l.Commit != sha || l.Session != "s-1" || l.Agent != "agent-1" {
		t.Errorf("ledger line = %+v", l)
	}

	// A rerun says so to the worker.
	rq.Rerun = true
	if _, err := Spawn(r.deps, rq); err != nil {
		t.Fatalf("Spawn(rerun): %v", err)
	}
	if args := s.seen()[1].Args; !reflect.DeepEqual(args[:5], []string{"verify", "--worker", "--store", r.st.Root, "--repo-dir"}) || !contains(args, "--rerun") {
		t.Errorf("rerun Args = %v, want --rerun among them", args)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestSpawnStartsNothingForAVerificationThatIsThere: any record of the commit
// for the slice stands, pending ones too, a worker holding the lock counts as
// one, and none of it starts a process or writes a line.
func TestSpawnStartsNothingForAVerificationThatIsThere(t *testing.T) {
	t.Parallel()
	r, s := spawnRig(t)
	sha := r.fixClamp()
	for state, line := range map[string]journal.Line{
		journal.VerifyPending: {Event: journal.EventVerifyPending, Attempt: 1},
		journal.VerifyPass:    {Event: journal.EventOracle, Outcome: "pass", Attempt: 1},
		journal.VerifyFail:    {Event: journal.EventOracle, Outcome: "fail", Attempt: 1},
		journal.VerifyError:   {Event: journal.EventVerifyError, Attempt: 1},
	} {
		slice := map[string]string{journal.VerifyPending: "a", journal.VerifyPass: "b", journal.VerifyFail: "c", journal.VerifyError: "d"}[state]
		line.Slice, line.Commit = slice, sha
		if err := journal.Append(r.st, r.fx.Ticket, line); err != nil {
			t.Fatal(err)
		}
		got, err := Spawn(r.deps, r.request(slice, sha))
		if err != nil || !got.Existing || got.State != state || got.Attempt != 1 || got.PID != 0 {
			t.Errorf("Spawn over a %s record = %+v, %v, want it to stand", state, got, err)
		}
	}
	if n := len(s.seen()); n != 0 {
		t.Errorf("%d processes were started, want none", n)
	}
	if led := ledgerOf(t, r); len(led) != 0 {
		t.Errorf("ledger lines %+v, want none", led)
	}
	if n := len(r.lines()); n != 4 {
		t.Errorf("journal has %d lines, want only the four the test wrote", n)
	}

	// A rerun starts one over an outcome.
	rq := r.request("b", sha)
	rq.Rerun = true
	if got, err := Spawn(r.deps, rq); err != nil || got.Existing || got.PID != 4242 {
		t.Errorf("Spawn(rerun) over a pass = %+v, %v, want a worker", got, err)
	}
}

// TestSpawnYieldsToAWorkerHoldingTheLock: a worker that has started and not yet
// written its pending line still blocks a second Spawn, even a rerun.
func TestSpawnYieldsToAWorkerHoldingTheLock(t *testing.T) {
	t.Parallel()
	r, s := spawnRig(t)
	sha := r.fixClamp()
	release, held, err := store.TryLock(r.worktreeDir(sha, "a"), 0)
	if err != nil || !held {
		t.Fatalf("test setup: %v, %v", held, err)
	}
	defer release()
	for _, rerun := range []bool{false, true} {
		rq := r.request("a", sha)
		rq.Rerun = rerun
		got, err := Spawn(r.deps, rq)
		if err != nil || !got.Existing || got.State != journal.VerifyPending {
			t.Errorf("Spawn(rerun=%v) = %+v, %v, want the worker's pending state", rerun, got, err)
		}
	}
	if len(s.seen()) != 0 {
		t.Error("a process was started while a worker held the lock")
	}
}

// TestSpawnReportsWhatItCannotStart: a start that fails is an error and leaves
// no ledger line; a request that names nothing real, or a Deps with no binary,
// is refused before anything starts.
func TestSpawnReportsWhatItCannotStart(t *testing.T) {
	t.Parallel()
	r, s := spawnRig(t)
	sha := r.fixClamp()

	s.err = errors.New("no such program")
	if got, err := Spawn(r.deps, r.request("a", sha)); err == nil || got.PID != 0 {
		t.Errorf("Spawn with a failing start = %+v, %v, want the error", got, err)
	}
	if led := ledgerOf(t, r); len(led) != 0 || len(r.lines()) != 0 {
		t.Errorf("a worker that never started left lines: ledger %+v, journal %v", led, events(r.lines()))
	}
	s.err = nil

	noBinary := r.deps
	noBinary.Executable = ""
	noStart := r.deps
	noStart.Start = nil
	for name, c := range map[string]struct {
		d  Deps
		rq Request
	}{
		"no binary":         {noBinary, r.request("a", sha)},
		"no way to start":   {noStart, r.request("a", sha)},
		"an unknown slice":  {r.deps, r.request("zz", sha)},
		"an unknown commit": {r.deps, r.request("a", "0123456789012345678901234567890123456789")},
	} {
		if _, err := Spawn(c.d, c.rq); err == nil {
			t.Errorf("%s: Spawn accepted the request", name)
		}
	}
	if n := len(s.seen()); n != 1 {
		t.Errorf("%d processes were started, want only the one that failed", n)
	}
}
