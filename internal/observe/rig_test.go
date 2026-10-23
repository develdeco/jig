package observe

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/spawn"
	"github.com/develdeco/jig/internal/store"
)

// testClock is a clock the test moves; each read moves it a millisecond, so
// two lines never share a stamp. It starts at the real time: the verify package
// stamps its own ledger lines with the real clock.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Millisecond)
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// rig is a fixture with the core wired to it: a clock of its own, a process
// starter that records what it was asked to start, and a store opened the way
// cmd/jig opens one.
type rig struct {
	t     *testing.T
	fx    *fixture.Fixture
	st    *store.Store
	d     Deps
	clock *testClock

	mu     sync.Mutex
	starts []spawn.Spec
}

func newRig(t *testing.T) *rig {
	t.Helper()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, fx: fx, st: st, clock: &testClock{now: time.Now()}}
	r.d = Deps{
		JigHome:    fx.Home,
		Now:        r.clock.Now,
		Executable: filepath.Join(fx.Dir, "bin", "jig"),
		Start: func(s spawn.Spec) (int, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.starts = append(r.starts, s)
			return 4200 + len(r.starts), nil
		},
		OpenStore: func(w locate.Where) (*store.Store, project.Config, error) {
			cfg, err := project.Load(filepath.Join(w.StorePath, "project.yaml"))
			if err != nil {
				return nil, cfg, err
			}
			s, err := store.Open(w.StorePath)
			return s, cfg, err
		},
		Here: func(w locate.Where, _ *store.Store) (string, error) {
			return fmt.Sprintf("here:\n  project: %s\n  ticket: %s\n  slice: %s\n", w.Project, w.Ticket, w.Slice), nil
		},
	}
	return r
}

// onTicketBranch puts the clone on the branch that binds the fixture ticket.
func (r *rig) onTicketBranch() {
	r.t.Helper()
	if _, err := gitx.Run(r.fx.RepoDir, "checkout", "-q", "-b", "jig/"+r.fx.Ticket); err != nil {
		r.t.Fatal(err)
	}
}

// commit makes a commit in the clone, with the slice trailer when slice is
// not empty, and returns its sha.
func (r *rig) commit(slice, name string) string {
	r.t.Helper()
	path := filepath.Join(r.fx.RepoDir, name)
	if err := os.WriteFile(path, []byte(name+"\n"), 0o644); err != nil {
		r.t.Fatal(err)
	}
	args := []string{"commit", "-q", "-m", "work on " + name}
	if slice != "" {
		args = append(args, "-m", "Jig-Slice: "+slice)
	}
	for _, a := range [][]string{{"add", "--", name}, args} {
		if _, err := gitx.Run(r.fx.RepoDir, a...); err != nil {
			r.t.Fatalf("git %v: %v", a, err)
		}
	}
	sha, err := gitx.RevParse(r.fx.RepoDir, "HEAD")
	if err != nil {
		r.t.Fatal(err)
	}
	return sha
}

// ev is an event of the session s1 in the clone.
func (r *rig) ev(kind string, edit ...func(*Event)) Event {
	e := Event{Harness: "claude", Kind: kind, Session: "s1", Cwd: r.fx.RepoDir}
	for _, f := range edit {
		f(&e)
	}
	return e
}

// handle runs the event and fails the test on an error.
func (r *rig) handle(e Event) Result {
	r.t.Helper()
	res, err := Handle(r.d, e)
	if err != nil {
		r.t.Fatalf("Handle(%s): %v", e.Kind, err)
	}
	return res
}

func (r *rig) ledger() []journal.Line {
	r.t.Helper()
	lines, err := ledger.Read(r.fx.Home)
	if err != nil {
		r.t.Fatal(err)
	}
	return lines
}

// ledgerOf is the ledger lines of one event.
func (r *rig) ledgerOf(event string) []journal.Line {
	var out []journal.Line
	for _, l := range r.ledger() {
		if l.Event == event {
			out = append(out, l)
		}
	}
	return out
}

func (r *rig) journal() []journal.Line {
	r.t.Helper()
	lines, err := journal.Read(r.st, r.fx.Ticket)
	if err != nil {
		r.t.Fatal(err)
	}
	return lines
}

// add appends a line to the ticket journal, stamped by the clock unless it
// has a stamp.
func (r *rig) add(l journal.Line) {
	r.t.Helper()
	if l.TS == "" {
		l.TS = ledger.Stamp(r.clock.Now())
	}
	if err := journal.Append(r.st, r.fx.Ticket, l); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) started() []spawn.Spec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]spawn.Spec(nil), r.starts...)
}
