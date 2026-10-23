package verify

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// errExit is the error a failing oracle stub returns.
var errExit = errors.New("exit status 1")

// rig is a fixture, its store, and a Deps wired to them with a stub oracle
// that passes: the common ground of the tests of Run and Spawn.
type rig struct {
	t      *testing.T
	fx     *fixture.Fixture
	st     *store.Store
	deps   Deps
	oracle *oracleStub
	clock  *testClock
	log    *strings.Builder
}

// newRig generates a fixture and wires a rig to it. opts.Home is set here.
func newRig(t *testing.T, opts fixture.Opts) *rig {
	t.Helper()
	opts.Home = t.TempDir()
	fx := fixture.Generate(t, opts)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	r := &rig{t: t, fx: fx, st: st, oracle: &oracleStub{}, clock: &testClock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}, log: &strings.Builder{}}
	r.oracle.clock = r.clock
	r.deps = Deps{
		Store:   st,
		JigHome: fx.Home,
		Clone:   fx.RepoDir,
		Oracle:  r.oracle.run,
		Now:     r.clock.Now,
		Log:     r.log,
		Sleep:   func(time.Duration) {},
	}
	return r
}

// testClock is a clock the oracle stub moves.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// oracleCall is one run the stub oracle was asked for, with what the
// worktree looked like at that moment.
type oracleCall struct {
	cmd   string
	dir   string
	env   []string
	limit time.Duration
	head  string // the worktree's HEAD
	alpha string // alpha/alpha.go as the worktree has it
	git   bool   // dir/.git exists
}

// oracleStub stands in for the oracle: it records the calls, writes the files
// it is given into the recording directory its env names, takes elapsed on the
// test clock, and passes or fails.
type oracleStub struct {
	clock   *testClock
	out     string
	err     error
	elapsed time.Duration
	files   map[string]string // written into JIG_RECORD_DIR

	mu    sync.Mutex
	calls []oracleCall
}

func (o *oracleStub) run(cmd, dir string, env []string, limit time.Duration) (string, error) {
	call := oracleCall{cmd: cmd, dir: dir, env: env, limit: limit}
	call.head, _ = gitx.RevParse(dir, "HEAD")
	if data, err := os.ReadFile(filepath.Join(dir, "alpha", "alpha.go")); err == nil {
		call.alpha = string(data)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		call.git = true
	}
	o.mu.Lock()
	o.calls = append(o.calls, call)
	o.mu.Unlock()
	if o.clock != nil {
		o.clock.advance(o.elapsed)
	}
	if rd := envValue(env, recordDirEnv); rd != "" {
		for name, content := range o.files {
			if err := os.WriteFile(filepath.Join(rd, name), []byte(content), 0o644); err != nil {
				return "", err
			}
		}
	}
	return o.out, o.err
}

func (o *oracleStub) seen() []oracleCall {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]oracleCall(nil), o.calls...)
}

// envValue is the value of name in a KEY=VALUE list, "" when it is not there.
func envValue(env []string, name string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

// commit writes files (clone-relative path to content) in the fixture's clone
// and commits them, with the Jig-Slice trailer for slice when it is not
// empty. It returns the commit.
func (r *rig) commit(slice, msg string, files map[string]string) string {
	r.t.Helper()
	for rel, content := range files {
		full := filepath.Join(r.fx.RepoDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	if _, err := gitx.Run(r.fx.RepoDir, "add", "-A"); err != nil {
		r.t.Fatalf("git add: %v", err)
	}
	args := []string{"commit", "-m", msg}
	if slice != "" {
		args = append(args, "-m", "Jig-Slice: "+slice)
	}
	if _, err := gitx.Run(r.fx.RepoDir, args...); err != nil {
		r.t.Fatalf("git commit: %v", err)
	}
	sha, err := gitx.RevParse(r.fx.RepoDir, "HEAD")
	if err != nil {
		r.t.Fatal(err)
	}
	return sha
}

// fixClamp commits the change slice a is about: Clamp enforces its upper
// bound.
func (r *rig) fixClamp() string {
	r.t.Helper()
	path := filepath.Join(r.fx.RepoDir, "alpha", "alpha.go")
	data, err := os.ReadFile(path)
	if err != nil {
		r.t.Fatal(err)
	}
	fixed := strings.Replace(string(data), "\treturn v\n", "\tif v > hi {\n\t\treturn hi\n\t}\n\treturn v\n", 1)
	if fixed == string(data) {
		r.t.Fatal("test setup: alpha.go has no Clamp to fix")
	}
	return r.commit("a", "fix Clamp", map[string]string{"alpha/alpha.go": fixed})
}

// lines reads the ticket's journal.
func (r *rig) lines() []journal.Line {
	r.t.Helper()
	lines, err := journal.Read(r.st, r.fx.Ticket)
	if err != nil {
		r.t.Fatalf("journal.Read: %v", err)
	}
	return lines
}

// events lists the journal's events in order, each as "event" or
// "event:outcome" when it has an outcome.
func events(lines []journal.Line) []string {
	var out []string
	for _, l := range lines {
		e := l.Event
		if l.Outcome != "" {
			e += ":" + l.Outcome
		}
		out = append(out, e)
	}
	return out
}

// state reads a slice's state.
func (r *rig) state(slice string) store.SliceState {
	r.t.Helper()
	st, err := r.st.ReadSliceState(r.fx.Ticket, slice)
	if err != nil {
		r.t.Fatalf("ReadSliceState(%s): %v", slice, err)
	}
	return st
}

// request is a request for the fixture's ticket.
func (r *rig) request(slice, sha string) Request {
	return Request{Ticket: r.fx.Ticket, Slice: slice, SHA: sha, Session: "s-1", Agent: "agent-1"}
}

// worktreeDir is where a verification of sha for slice puts its worktree.
func (r *rig) worktreeDir(sha, slice string) string {
	r.t.Helper()
	id, err := r.st.ID()
	if err != nil {
		r.t.Fatal(err)
	}
	dir, err := home.VerifyWorktreeDir(r.fx.Home, id, r.fx.Ticket, sha, slice)
	if err != nil {
		r.t.Fatal(err)
	}
	return dir
}

// worktreesOf lists the clone's worktrees by path, the main one included.
func worktreesOf(t *testing.T, clone string) []string {
	t.Helper()
	out, err := gitx.Run(clone, "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatalf("git worktree list: %v", err)
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree "); ok {
			paths = append(paths, p)
		}
	}
	return paths
}
