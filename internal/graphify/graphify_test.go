package graphify

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/project"
)

func TestDetectNoopWithoutOptIn(t *testing.T) {
	cfg := project.Config{}
	if _, ok := Detect(cfg).(cliPlane); ok {
		t.Fatal("Detect returned cliPlane without an opt-in Context entry")
	}
	p := Detect(cfg)
	if p.Enabled() {
		t.Fatal("Noop plane must report Enabled() == false")
	}
	nodes, err := p.Affected("anything", t.TempDir())
	if err != nil || nodes != nil {
		t.Fatalf("Noop.Affected = (%v, %v), want (nil, nil)", nodes, err)
	}
	if err := p.Update(t.TempDir()); err != nil {
		t.Fatalf("Noop.Update = %v, want nil", err)
	}
	if got, err := p.Query(t.TempDir(), "anything"); err != nil || got != nil {
		t.Fatalf("Noop.Query = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestDetectNoopWhenGraphifyNotOnPath(t *testing.T) {
	// Even with opt-in Context, a missing graphify binary keeps jig on the
	// Noop plane; a found one gives the cli plane.
	cfg := project.Config{Context: map[string]any{"graphify": true}}
	missing := func(string) (string, error) { return "", errors.New("not found") }
	if p := DetectWith(cfg, missing); p.Enabled() {
		t.Fatal("expected Noop plane when graphify is not on PATH")
	}
	found := func(string) (string, error) { return "/opt/bin/graphify", nil }
	if p := DetectWith(cfg, found); !p.Enabled() {
		t.Fatal("expected the cli plane when graphify is found")
	}
}

func TestParseAffected(t *testing.T) {
	const canned = `Affected nodes for pkg.Foo
Relations: calls, imports
Depth: 2
- pkg.Bar [calls] pkg/bar.go:12
- pkg.Baz [imports] pkg/baz.go:3
`
	got := parseAffected(canned)
	want := []string{"pkg.Bar", "pkg.Baz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseAffected = %v, want %v", got, want)
	}
}

func TestParseAffectedNoMatches(t *testing.T) {
	if got := parseAffected("No unique node match for pkg.Foo\n"); got != nil {
		t.Fatalf("parseAffected(no unique match) = %v, want nil", got)
	}
	const empty = `Affected nodes for pkg.Foo
Relations: calls
Depth: 2
No affected nodes found.
`
	if got := parseAffected(empty); got != nil {
		t.Fatalf("parseAffected(no affected) = %v, want nil", got)
	}
}

// TestParseQuery: graphify query's NODE lines become nodes in order, with
// forward-slash files and line numbers; nodes with no source file and every
// other line are skipped. The lines are graphify 0.9.77's own output.
func TestParseQuery(t *testing.T) {
	const canned = "Graph: graphify-out/graph.json (3657 nodes) | Traversal: BFS depth=2 | Start: ['ApplyRound()'] | 1511 nodes found\n" +
		"\n" +
		"[!] TRUNCATED: showing 56 of 1511 nodes (~1500-token budget).\n" +
		"\n" +
		"NODE ExpandStillPresent() [src=internal/verifydeliver/findings.go loc=L192 community=]\n" +
		`NODE Store schema is the API [src=docs\adr\0003-store-schema-is-the-api.md loc=L1 community=]` + "\n" +
		"NODE go_pkg_os [src= loc= community=go_pkg_os]\n" +
		"NODE testing.T [src= loc= community=]\n" +
		"EDGE ApplyRound() -> Finding [calls]\n"
	got := parseQuery(canned)
	want := []Node{
		{Label: "ExpandStillPresent()", File: "internal/verifydeliver/findings.go", Line: 192},
		{Label: "Store schema is the API", File: "docs/adr/0003-store-schema-is-the-api.md", Line: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseQuery =\n%+v\nwant\n%+v", got, want)
	}
}

// TestGraphifyLive runs the real graphify binary: an update over a small Go
// repo, then a query that must find its function. It needs graphify on
// PATH (PyPI graphifyy) and runs only with JIG_LIVE_GRAPHIFY=1.
func TestGraphifyLive(t *testing.T) {
	if os.Getenv("JIG_LIVE_GRAPHIFY") != "1" {
		t.Skip("set JIG_LIVE_GRAPHIFY=1 to run against the real graphify")
	}
	cfg := project.Config{Context: map[string]any{"graphify": true}}
	p := Detect(cfg)
	if !p.Enabled() {
		t.Fatal("JIG_LIVE_GRAPHIFY=1 but graphify is not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/clamp\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package clamp\n\n// Clamp limits v to [lo, hi].\nfunc Clamp(v, lo, hi int) int {\n\tif v < lo {\n\t\treturn lo\n\t}\n\tif v > hi {\n\t\treturn hi\n\t}\n\treturn v\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "clamp.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Update(dir); err != nil {
		t.Fatalf("Update: %v", err)
	}
	nodes, err := p.Query(dir, "fix Clamp so it limits a value to a range")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, n := range nodes {
		if n.File == "clamp.go" {
			return
		}
	}
	t.Fatalf("Query nodes = %+v, want one in clamp.go", nodes)
}
