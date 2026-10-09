package frontier

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/graphify"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// fakePlane is a code-graph plane that answers every query with nodes, or
// fails with err, and records the questions it was asked.
type fakePlane struct {
	nodes []graphify.Node
	err   error

	mu        sync.Mutex
	questions []string
	calls     []string
}

func (p *fakePlane) Enabled() bool { return true }
func (p *fakePlane) Affected(string, string) ([]string, error) {
	return nil, nil
}
func (p *fakePlane) Update(string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, "update")
	return nil
}
func (p *fakePlane) Query(_, question string) ([]graphify.Node, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.questions = append(p.questions, question)
	p.calls = append(p.calls, "query")
	return p.nodes, p.err
}

// readSliceJSON reads slice's attempt-1 slice.json.
func readSliceJSON(t *testing.T, st *store.Store, ticket, slice string) sliceJSONBody {
	t.Helper()
	data, err := os.ReadFile(sliceJSONPath(st, ticket, slice, 1))
	if err != nil {
		t.Fatalf("read %s's slice.json: %v", slice, err)
	}
	var body sliceJSONBody
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("parse %s's slice.json: %v", slice, err)
	}
	return body
}

// graphLines are the ticket's graph journal lines for slice.
func graphLines(t *testing.T, st *store.Store, ticket, slice string) []journal.Line {
	t.Helper()
	lines, err := journal.Read(st, ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	var out []journal.Line
	for _, l := range lines {
		if l.Event == "graph" && l.Slice == slice {
			out = append(out, l)
		}
	}
	return out
}

// TestRunHandsTheBuilderTheCodeTheGraphLinksToItsGoal covers ADR 0026 at
// frontier's dispatch: with a code graph, each slice's slice.json carries
// the nodes the graph links to its goal, grouped by file in the graph's
// order; the graph is updated before it is queried, the question carries
// the goal, the graph's output stays out of the lease's commits, and the
// run journals a graph line. A failing graph leaves related empty, journals
// why, and the slice still builds. With no graph, related is empty and
// nothing is journaled.
func TestRunHandsTheBuilderTheCodeTheGraphLinksToItsGoal(t *testing.T) {
	t.Parallel()

	t.Run("a graph that answers", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d, st := newDeps(t, fx)
		plane := &fakePlane{nodes: []graphify.Node{
			{Label: "Clamp()", File: "alpha/alpha.go", Line: 3},
			{Label: "TestClamp()", File: "alpha/alpha_test.go", Line: 8},
			{Label: "clampHelper()", File: "alpha/alpha.go", Line: 20},
			{Label: "README", File: "README.md"},
		}}
		d.Graph = plane
		if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
			t.Fatalf("Run: %v", err)
		}

		body := readSliceJSON(t, st, fx.Ticket, "a")
		want := []relatedFile{
			{File: "alpha/alpha.go", Symbols: []string{"Clamp() L3", "clampHelper() L20"}},
			{File: "alpha/alpha_test.go", Symbols: []string{"TestClamp() L8"}},
			{File: "README.md", Symbols: []string{"README"}},
		}
		if len(body.Related) != len(want) {
			t.Fatalf("a's related = %+v, want %+v", body.Related, want)
		}
		for i := range want {
			if body.Related[i].File != want[i].File || strings.Join(body.Related[i].Symbols, "|") != strings.Join(want[i].Symbols, "|") {
				t.Errorf("a's related[%d] = %+v, want %+v", i, body.Related[i], want[i])
			}
		}
		plane.mu.Lock()
		asked := strings.Join(plane.questions, "\n")
		calls := strings.Join(plane.calls, ",")
		plane.mu.Unlock()
		if !strings.Contains(asked, "Fix Clamp so TestClamp passes.") {
			t.Errorf("questions = %q, want slice a's goal among them", asked)
		}
		if calls == "" || strings.Count(calls, "update,query") != strings.Count(calls, "query") {
			t.Errorf("calls = %s, want an update right before each query", calls)
		}
		if got := graphLines(t, st, fx.Ticket, "a"); len(got) != 1 || got[0].Outcome != "pass" || got[0].Seconds < 1 {
			t.Errorf("a's graph lines = %+v, want one pass with its time", got)
		}

		exclude, err := os.ReadFile(filepath.Join(buildLeaseOf(t, fx), ".git", "info", "exclude"))
		if err != nil {
			t.Fatalf("read the lease's info/exclude: %v", err)
		}
		if !strings.Contains(string(exclude), "/"+graphify.OutDir+"/") {
			t.Errorf("the lease's info/exclude = %q, want %s/ in it", exclude, graphify.OutDir)
		}
	})

	t.Run("a graph that fails", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d, st := newDeps(t, fx)
		d.Graph = &fakePlane{err: errors.New("graph.json is corrupt")}
		if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if body := readSliceJSON(t, st, fx.Ticket, "a"); len(body.Related) != 0 {
			t.Errorf("a's related = %+v, want none when the graph fails", body.Related)
		}
		got := graphLines(t, st, fx.Ticket, "a")
		if len(got) != 1 || !strings.HasPrefix(got[0].Outcome, "fail: ") || !strings.Contains(got[0].Outcome, "corrupt") {
			t.Errorf("a's graph lines = %+v, want one fail with the reason", got)
		}
		if state, err := st.ReadSliceState(fx.Ticket, "a"); err != nil || state.State != "green" {
			t.Errorf("slice a state = %+v (%v), want green: a failing graph never fails the attempt", state, err)
		}
	})

	t.Run("no graph", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d, st := newDeps(t, fx)
		d.Graph = graphify.Noop{}
		if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if body := readSliceJSON(t, st, fx.Ticket, "a"); body.Related == nil || len(body.Related) != 0 {
			t.Errorf("a's related = %#v, want an empty list", body.Related)
		}
		if got := graphLines(t, st, fx.Ticket, "a"); len(got) != 0 {
			t.Errorf("a's graph lines = %+v, want none without a graph", got)
		}
	})
}

// TestRunWithTheRealGraphify drives frontier with the real graphify binary
// over the fixture repo: slice a's related must name a file under alpha/,
// the workspace its goal is about. It needs graphify on PATH and runs only
// with JIG_LIVE_GRAPHIFY=1.
func TestRunWithTheRealGraphify(t *testing.T) {
	t.Parallel()
	if os.Getenv("JIG_LIVE_GRAPHIFY") != "1" {
		t.Skip("set JIG_LIVE_GRAPHIFY=1 to run against the real graphify")
	}
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	cfg := d.Cfg
	cfg.Context = map[string]any{"graphify": true}
	d.Graph = graphify.Detect(cfg)
	if !d.Graph.Enabled() {
		t.Fatal("JIG_LIVE_GRAPHIFY=1 but graphify is not on PATH")
	}
	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body := readSliceJSON(t, st, fx.Ticket, "a")
	for _, r := range body.Related {
		if strings.HasPrefix(r.File, "alpha/") {
			return
		}
	}
	t.Fatalf("a's related = %+v (graph lines %+v), want a file under alpha/", body.Related, graphLines(t, st, fx.Ticket, "a"))
}

// TestExcludeInLease: the pattern is appended to the lease's info/exclude
// once, after any last line without a newline, and a second call adds
// nothing.
func TestExcludeInLease(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := gitx.Run(dir, "init", "-q"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	path := filepath.Join(dir, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("*.log"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := excludeInLease(dir, "/graphify-out/"); err != nil {
			t.Fatalf("excludeInLease call %d: %v", i+1, err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "*.log\n/graphify-out/\n" {
		t.Errorf("info/exclude = %q, want the old line, then the pattern once", got)
	}
}
