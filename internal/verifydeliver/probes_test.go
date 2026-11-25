package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// TestParseReviewResultProbeRules: probes are optional, parse as written when
// given, and are held to their own field rules (ADR 0036); a result that names
// skipped, which jig no longer takes, is refused as any unknown field.
func TestParseReviewResultProbeRules(t *testing.T) {
	t.Parallel()

	probe := ResultProbe{Seam: "a total", File: "billing/invoices.go", Line: 42, Mutation: "drop the factor", Command: "go test ./billing", Outcome: ProbeCaught}
	res, err := ParseReviewResult(validResultJSON(t, func(r *ReviewResult) {
		r.Probes = []ResultProbe{probe}
	}))
	if err != nil {
		t.Fatalf("ParseReviewResult: %v", err)
	}
	if len(res.Probes) != 1 || res.Probes[0] != probe {
		t.Errorf("parsed probes %+v, want them as written", res.Probes)
	}
	if res, err := ParseReviewResult(validResultJSON(t, nil)); err != nil || res.Probes != nil {
		t.Errorf("a result with no probes key: %+v, %v; want it valid, with no probes", res, err)
	}
	if _, err := ParseReviewResult(validResultJSONRaw(t, func(m map[string]any) { m["skipped"] = []any{} })); err == nil || !strings.Contains(err.Error(), `key "skipped" is not a recognized field`) {
		t.Errorf("a result with a skipped list: %v, want REVIEW_INVALID naming the unknown field", err)
	}

	withProbe := func(edit func(*ResultProbe)) []byte {
		return validResultJSON(t, func(r *ReviewResult) {
			p := probe
			edit(&p)
			r.Probes = []ResultProbe{p}
		})
	}
	cases := []struct {
		name string
		data []byte
	}{
		{"null probes", validResultJSONRaw(t, func(m map[string]any) { m["probes"] = nil })},
		{"empty seam", withProbe(func(p *ResultProbe) { p.Seam = " " })},
		{"empty mutation", withProbe(func(p *ResultProbe) { p.Mutation = "" })},
		{"empty command", withProbe(func(p *ResultProbe) { p.Command = "" })},
		{"unknown outcome", withProbe(func(p *ResultProbe) { p.Outcome = "killed" })},
		{"negative line", withProbe(func(p *ResultProbe) { p.Line = -1 })},
		{"absolute file", withProbe(func(p *ResultProbe) { p.File = "/abs/x.go" })},
		{"unknown probe key", validResultJSONRaw(t, func(m map[string]any) {
			m["probes"] = []any{map[string]any{"seam": "s", "file": "a.go", "line": 1, "mutation": "m", "command": "c", "outcome": "caught", "risk": "high"}}
		})},
		{"case variant probe key", validResultJSONRaw(t, func(m map[string]any) {
			m["probes"] = []any{map[string]any{"Seam": "s", "file": "a.go", "line": 1, "mutation": "m", "command": "c", "outcome": "caught"}}
		})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseReviewResult(c.data)
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("ParseReviewResult = %v, want REVIEW_INVALID", err)
			}
		})
	}
}

// TestALaterProbeOfALineSupersedesTheEarlierOne: with the file unchanged, a
// survivor that a later round probed again at the same line is stale, and the
// later probe is the current one; a probe of another line stays current.
func TestALaterProbeOfALineSupersedesTheEarlierOne(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.name", "f"}, {"config", "user.email", "f@example.invalid"}} {
		if _, err := gitx.Run(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "c1"}} {
		if _, err := gitx.Run(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	blob, err := gitx.RevParse(dir, "HEAD:a.go")
	if err != nil {
		t.Fatal(err)
	}
	rf := roundFiles{repos: []roundRepo{{Dir: dir, Head: "HEAD"}}}
	probed, err := probedEntries(rf, []ProbeRecord{
		{Round: 1, File: "a.go", Line: 5, Outcome: ProbeSurvived, Blob: blob},
		{Round: 1, File: "a.go", Line: 9, Outcome: ProbeCaught, Blob: blob},
		{Round: 2, File: "a.go", Line: 5, Outcome: ProbeCaught, Blob: blob},
	})
	if err != nil {
		t.Fatalf("probedEntries: %v", err)
	}
	states := []string{probed[0].State, probed[1].State, probed[2].State}
	if want := []string{ProbeStale, ProbeCurrent, ProbeCurrent}; strings.Join(states, ",") != strings.Join(want, ",") {
		t.Errorf("states = %v, want %v: round 2's probe of line 5 supersedes round 1's", states, want)
	}
}
