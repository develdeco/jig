package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// TestParseReviewResultProbeRules: probes and skipped are optional, parse as
// written when given, and are held to their own field rules (ADR 0036).
func TestParseReviewResultProbeRules(t *testing.T) {
	t.Parallel()

	probe := ResultProbe{Seam: "a total", File: "billing/invoices.go", Line: 42, Mutation: "drop the factor", Command: "go test ./billing", Outcome: ProbeCaught}
	skip := ResultSkip{File: "billing/invoices_test.go", Why: "tests"}
	res, err := ParseReviewResult(validResultJSON(t, func(r *ReviewResult) {
		r.Probes = []ResultProbe{probe}
		r.Skipped = []ResultSkip{skip, {File: "billing/doc.go", From: 3, To: 9, Why: "a doc"}}
	}))
	if err != nil {
		t.Fatalf("ParseReviewResult: %v", err)
	}
	if len(res.Probes) != 1 || res.Probes[0] != probe || len(res.Skipped) != 2 || res.Skipped[0] != skip {
		t.Errorf("parsed probes %+v and skips %+v, want them as written", res.Probes, res.Skipped)
	}
	if res, err := ParseReviewResult(validResultJSON(t, nil)); err != nil || res.Probes != nil || res.Skipped != nil {
		t.Errorf("a result with neither key: %+v, %v; want it valid, with no probes or skips", res, err)
	}

	withProbe := func(edit func(*ResultProbe)) []byte {
		return validResultJSON(t, func(r *ReviewResult) {
			p := probe
			edit(&p)
			r.Probes = []ResultProbe{p}
		})
	}
	withSkip := func(s ResultSkip) []byte {
		return validResultJSON(t, func(r *ReviewResult) { r.Skipped = []ResultSkip{s} })
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
		{"empty why", withSkip(ResultSkip{File: "a.go"})},
		{"one end of a range", withSkip(ResultSkip{File: "a.go", From: 3, Why: "w"})},
		{"a range out of order", withSkip(ResultSkip{File: "a.go", From: 9, To: 3, Why: "w"})},
		{"case variant skip key", validResultJSONRaw(t, func(m map[string]any) {
			m["skipped"] = []any{map[string]any{"File": "a.go", "from": 0, "to": 0, "why": "w"}}
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

// TestCoverHunksCountsProbesInsideAndTakesACoveringSkip: a probe counts in
// the hunk its line falls inside and nowhere else, and a skip covers a hunk it
// contains whole, or every hunk of its file when it names the file alone.
func TestCoverHunksCountsProbesInsideAndTakesACoveringSkip(t *testing.T) {
	t.Parallel()

	hunks := []HunkEntry{{File: "a.go", From: 3, To: 5}, {File: "a.go", From: 10, To: 12}, {File: "b.go", From: 1, To: 4}, {File: "c.go", From: 2, To: 8}}
	got := coverHunks(hunks,
		[]ResultProbe{{File: "a.go", Line: 4}, {File: "a.go", Line: 5}, {File: "a.go", Line: 9}},
		[]ResultSkip{{File: "b.go", Why: "tests"}, {File: "c.go", From: 3, To: 8, Why: "partly"}},
	)
	want := []HunkEntry{
		{File: "a.go", From: 3, To: 5, Probes: 2},
		{File: "a.go", From: 10, To: 12},
		{File: "b.go", From: 1, To: 4, Skip: "tests"},
		{File: "c.go", From: 2, To: 8},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hunk %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if left := unprobedHunks(got); len(left) != 2 || left[0] != want[1] || left[1] != want[3] {
		t.Errorf("unprobed = %+v, want the second a.go hunk and c.go's", left)
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
	probed, current, _, err := currentLedger(rf, []ProbeRecord{
		{Round: 1, File: "a.go", Line: 5, Outcome: ProbeSurvived, Blob: blob},
		{Round: 1, File: "a.go", Line: 9, Outcome: ProbeCaught, Blob: blob},
		{Round: 2, File: "a.go", Line: 5, Outcome: ProbeCaught, Blob: blob},
	}, nil)
	if err != nil {
		t.Fatalf("currentLedger: %v", err)
	}
	states := []string{probed[0].State, probed[1].State, probed[2].State}
	if want := []string{ProbeStale, ProbeCurrent, ProbeCurrent}; strings.Join(states, ",") != strings.Join(want, ",") {
		t.Errorf("states = %v, want %v: round 2's probe of line 5 supersedes round 1's", states, want)
	}
	if len(current) != 2 || current[0].Line != 9 || current[1].Outcome != ProbeCaught {
		t.Errorf("current = %+v, want line 9 and round 2's caught probe of line 5", current)
	}
}
