package graphify

import (
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
}

func TestDetectNoopWhenGraphifyNotOnPath(t *testing.T) {
	// Even with opt-in Context, a missing graphify binary on PATH keeps
	// jig on the Noop plane.
	t.Setenv("PATH", t.TempDir())
	cfg := project.Config{Context: map[string]any{"graphify": true}}
	if p := Detect(cfg); p.Enabled() {
		t.Fatal("expected Noop plane when graphify is not on PATH")
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
