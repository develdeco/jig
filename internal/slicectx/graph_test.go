package slicectx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/graphify"
	"github.com/develdeco/jig/internal/journal"
)

// fakePlane is a code graph that answers from a script and remembers what it
// was asked.
type fakePlane struct {
	enabled             bool
	nodes               []graphify.Node
	updateErr, queryErr error
	updated             []string
	questions           []string
}

func (p *fakePlane) Enabled() bool                             { return p.enabled }
func (p *fakePlane) Affected(string, string) ([]string, error) { return nil, nil }
func (p *fakePlane) Update(dir string) error {
	p.updated = append(p.updated, dir)
	return p.updateErr
}
func (p *fakePlane) Query(dir, q string) ([]graphify.Node, error) {
	p.questions = append(p.questions, q)
	return p.nodes, p.queryErr
}

// TestBuildAsksTheGraphForStartingPoints: an enabled plane is updated, then
// asked the goal of the slice with the files of the earlier slices as seeds;
// its nodes come back grouped by file, the output of the graph is excluded in
// the clone, and a graph line is recorded with the files.
func TestBuildAsksTheGraphForStartingPoints(t *testing.T) {
	w := newWorld(t)
	w.write("alpha/clamp.go", "package alpha\n")
	sha := w.commit("JIG-1 a", "a", "alpha/clamp.go")
	w.line(journal.Line{Slice: "a", Event: journal.EventVerified, Commit: sha, Attempt: 1})
	plane := &fakePlane{enabled: true, nodes: []graphify.Node{
		{Label: "Clamp", File: "alpha/clamp.go", Line: 12},
		{Label: "clamp_test", File: "alpha/clamp_test.go"},
		{Label: "Min", File: "alpha/clamp.go", Line: 30},
	}}
	var recorded []journal.Line
	in := w.input("b")
	in.Graph = plane
	in.Session = "s-9"
	in.RecordGraph = func(l journal.Line) error { recorded = append(recorded, l); return nil }

	c, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plane.updated) != 1 || len(plane.questions) != 1 {
		t.Fatalf("updated %v, asked %v, want one update and one question", plane.updated, plane.questions)
	}
	if want := "Add a caller. Related files: alpha/clamp.go"; plane.questions[0] != want {
		t.Errorf("question = %q, want %q", plane.questions[0], want)
	}
	if len(c.Related) != 2 || c.Related[0].File != "alpha/clamp.go" ||
		strings.Join(c.Related[0].Symbols, "|") != "Clamp L12|Min L30" || c.Related[1].Symbols[0] != "clamp_test" {
		t.Errorf("related = %+v, want two files, symbols with their lines", c.Related)
	}
	if len(recorded) != 1 || recorded[0].Outcome != "pass" || recorded[0].Slice != "b" || recorded[0].Session != "s-9" ||
		recorded[0].Ticket != "JIG-1" || strings.Join(recorded[0].Files, ",") != "alpha/clamp.go,alpha/clamp_test.go" {
		t.Errorf("recorded = %+v, want one pass line for slice b with the files", recorded)
	}
	exclude, err := os.ReadFile(filepath.Join(w.clone, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(exclude), "/graphify-out/") {
		t.Errorf("info/exclude = %q, %v, want graphify-out excluded", exclude, err)
	}
	text := strings.Join(c.Blocks(), "\n")
	if !strings.Contains(text, "related[2]{file,symbols}:\n  alpha/clamp.go,Clamp L12+Min L30\n") {
		t.Errorf("blocks lack the related table:\n%s", text)
	}
}

// TestAGraphThatFailsCostsOnlyTheContext: a failed update leaves related
// empty and records the reason; a plane that is not enabled, or no clone,
// asks nothing and records nothing.
func TestAGraphThatFailsCostsOnlyTheContext(t *testing.T) {
	w := newWorld(t)
	plane := &fakePlane{enabled: true, updateErr: errors.New("graphify update: out of\nmemory")}
	var recorded []journal.Line
	in := w.input("b")
	in.Graph = plane
	in.RecordGraph = func(l journal.Line) error { recorded = append(recorded, l); return nil }
	c, err := Build(in)
	if err != nil {
		t.Fatalf("a failed graph failed the context: %v", err)
	}
	if len(c.Related) != 0 || len(recorded) != 1 || recorded[0].Outcome != "fail: graphify update: out of memory" {
		t.Errorf("related %+v, recorded %+v, want none and one fail line with the flattened reason", c.Related, recorded)
	}

	recorded = nil
	for _, in := range []Input{
		func() Input { x := w.input("b"); x.Graph = &fakePlane{enabled: false}; return x }(),
		func() Input { x := w.input("b"); x.Graph = plane; x.RepoDir = ""; return x }(),
		w.input("b"),
	} {
		in.RecordGraph = func(l journal.Line) error { recorded = append(recorded, l); return nil }
		plane.updated = nil
		if _, err := Build(in); err != nil {
			t.Fatal(err)
		}
		if len(recorded) != 0 || len(plane.updated) != 0 {
			t.Errorf("recorded %+v, updated %v, want nothing when the graph is off or there is no clone", recorded, plane.updated)
		}
	}
}

// TestGroupRelatedKeepsTheCaps: at most maxRelatedFiles files and
// maxRelatedSymbols symbols a file.
func TestGroupRelatedKeepsTheCaps(t *testing.T) {
	var nodes []graphify.Node
	for i := 0; i < maxRelatedFiles+5; i++ {
		nodes = append(nodes, graphify.Node{Label: "sym", File: "f" + string(rune('a'+i)) + ".go"})
	}
	for i := 0; i < maxRelatedSymbols+4; i++ {
		nodes = append(nodes, graphify.Node{Label: "more", File: "fa.go", Line: i + 1})
	}
	got := groupRelated(nodes)
	if len(got) != maxRelatedFiles || len(got[0].Symbols) != maxRelatedSymbols {
		t.Errorf("%d files, %d symbols in the first, want %d and %d", len(got), len(got[0].Symbols), maxRelatedFiles, maxRelatedSymbols)
	}
}
