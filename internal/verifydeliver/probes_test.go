package verifydeliver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
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

// TestGateHandsTheNextRoundItsProbeLedger drives two real gate rounds (ADR
// 0036). Round 1 starts from no probe and every hunk of the change unprobed;
// its reviewer probes one hunk of an alpha file and one of a beta file and
// skips another file whole, and the round keeps them in findings.yaml with
// their files' blobs and counts what it left. The branch then changes the beta
// file only. Round 2's request lists the alpha probe current and the beta one
// stale, and its hunks are the whole change's, not the delta's: the alpha
// hunk still has its probe, the skipped file is still set aside, and the beta
// file's hunks are unprobed again.
func TestGateHandsTheNextRoundItsProbeLedger(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)

	var reqs []ReviewRequest
	var alpha, beta HunkEntry
	skipped := ""
	backend := stubBackend{run: func(sd session.Dispatch) error {
		data, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(data, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}
		reqs = append(reqs, req)
		result := ReviewResult{ReviewedPaths: req.MustReview, Summary: "probed"}
		if len(reqs) == 1 {
			for _, h := range req.Hunks {
				switch {
				case alpha.File == "" && strings.HasPrefix(h.File, "alpha/"):
					alpha = h
				case beta.File == "" && strings.HasPrefix(h.File, "beta/"):
					beta = h
				}
			}
			for _, h := range req.Hunks {
				if h.File != alpha.File && h.File != beta.File {
					skipped = h.File
				}
			}
			if alpha.File == "" || beta.File == "" || skipped == "" {
				t.Fatalf("round 1 hunks = %+v, want hunks in alpha and beta and a third file", req.Hunks)
			}
			result.Probes = []ResultProbe{
				{Seam: "alpha's path", File: alpha.File, Line: alpha.From, Mutation: "negate it", Command: "go test ./alpha", Outcome: ProbeCaught},
				{Seam: "beta's path", File: beta.File, Line: beta.From, Mutation: "drop it", Command: "go test ./beta", Outcome: ProbeSurvived},
			}
			result.Skipped = []ResultSkip{{File: skipped, Why: "tests"}}
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := newReviewerGateSource(backend)

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	req1 := reqs[0]
	if len(req1.Probed) != 0 {
		t.Errorf("round 1 probed = %+v, want none", req1.Probed)
	}
	for _, h := range req1.Hunks {
		if !h.Unprobed() {
			t.Errorf("round 1 hunk %+v, want every hunk unprobed", h)
		}
	}
	covered := 0
	for _, h := range req1.Hunks {
		if h == alpha || h == beta || h.File == skipped {
			covered++
		}
	}
	if report1.Probes != 2 || report1.Unprobed != len(req1.Hunks)-covered {
		t.Errorf("round 1 report: probes %d, unprobed %d; want 2 and %d", report1.Probes, report1.Unprobed, len(req1.Hunks)-covered)
	}
	ff1, ok, err := readFindingsYAML(d.Store, fx.Ticket, 1)
	if err != nil || !ok {
		t.Fatalf("read round 1 findings.yaml: ok=%v err=%v", ok, err)
	}
	if len(ff1.Probes) != 2 || len(ff1.Skipped) != 1 {
		t.Fatalf("round 1 findings.yaml probes %+v, skipped %+v; want 2 and 1", ff1.Probes, ff1.Skipped)
	}
	head1 := req1.HeadSHA
	for _, p := range ff1.Probes {
		want, err := gitx.RevParse(buildLeaseDir(t, fx), head1+":"+p.File)
		if err != nil || p.Blob != want {
			t.Errorf("probe of %s keeps blob %q, want %q (err %v)", p.File, p.Blob, want, err)
		}
	}

	// The branch changes the beta file and nothing else.
	build := buildLeaseDir(t, fx)
	path := filepath.Join(build, filepath.FromSlash(beta.File))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, []byte("\n// changed after round 1\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(build, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.RunEnv(build, buildGitEnv, "commit", "-m", "change beta after round 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(build, "push", "origin", ticketBranch(fx.Ticket)); err != nil {
		t.Fatal(err)
	}

	if _, err := Gate(d, src, GateOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if len(reqs) != 2 {
		t.Fatalf("reviewer dispatched %d times, want 2", len(reqs))
	}
	req2 := reqs[1]
	if req2.Scope != "delta" {
		t.Errorf("round 2 scope = %q, want delta", req2.Scope)
	}
	states := map[string]string{}
	for _, p := range req2.Probed {
		if p.Round != 1 {
			t.Errorf("round 2 probed %+v, want round 1", p)
		}
		states[p.File] = p.State
	}
	if states[alpha.File] != ProbeCurrent || states[beta.File] != ProbeStale {
		t.Errorf("round 2 probed states = %v, want %s current and %s stale", states, alpha.File, beta.File)
	}
	var sawAlpha bool
	for _, h := range req2.Hunks {
		switch {
		case h == HunkEntry{File: alpha.File, From: alpha.From, To: alpha.To, Probes: 1}:
			sawAlpha = true
		case h.File == skipped && h.Skip != "tests":
			t.Errorf("round 2 hunk %+v, want it set aside as tests", h)
		case h.File == beta.File && !h.Unprobed():
			t.Errorf("round 2 hunk %+v of the changed file, want it unprobed", h)
		}
	}
	if !sawAlpha {
		t.Errorf("round 2 hunks = %+v, want the whole change's, with alpha's probed hunk %+v", req2.Hunks, alpha)
	}
}

// TestGateTimesTheOraclesItRunsAndNamesTheirSlices: every oracles_passed
// entry carries the seconds of the run it stands for, at least 1 for a run
// the gate made, and the slices whose oracle it is in its workspace.
func TestGateTimesTheOraclesItRunsAndNamesTheirSlices(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)
	slices, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}

	got := gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed
	if len(got) == 0 {
		t.Fatal("oracles_passed is empty")
	}
	named := 0
	for _, run := range got {
		named += len(run.Slices)
		if run.ReusedFrom == "" && run.Seconds < 1 {
			t.Errorf("%+v: a run the gate made, want at least 1 second", run)
		}
		var want []string
		for _, s := range slices {
			if s.Oracle == run.Oracle && s.Workspace == run.Workspace {
				want = append(want, s.ID)
			}
		}
		if strings.Join(run.Slices, ",") != strings.Join(want, ",") {
			t.Errorf("%+v: slices %v, want %v", run, run.Slices, want)
		}
	}
	if named == 0 {
		t.Errorf("oracles_passed = %+v, want the fixture's slices named on their oracle's runs", got)
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

// TestWithSlicesReadsAWorkspaceAsASliceWritesIt: a run names the slices of its
// workspace whether the slice writes it bare, as a bare id holding a slash, or
// as <repo>/<id>, and a slice of one workspace is never put on another's run.
func TestWithSlicesReadsAWorkspaceAsASliceWritesIt(t *testing.T) {
	t.Parallel()

	man := manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "cmd/jig"}, {ID: "x"}, {ID: "a/x"}}}
	runs := []OracleRun{{Oracle: "test", Workspace: "cmd/jig"}, {Oracle: "test", Workspace: "x"}, {Oracle: "test", Workspace: "a/x"}}
	got := withSlices(runs, "proj", man, []store.Slice{
		{ID: "s1", Workspace: "cmd/jig", Oracle: "test"},
		{ID: "s2", Workspace: "a/x", Oracle: "test"},
		{ID: "s3", Workspace: "proj/x", Oracle: "test"},
		{ID: "s4", Workspace: "x", Oracle: "lint"},
		{ID: "s5", Workspace: "other/x", Oracle: "test"},
	})
	want := map[string]string{"cmd/jig": "s1", "x": "s3", "a/x": "s2"}
	for _, run := range got {
		if strings.Join(run.Slices, ",") != want[run.Workspace] {
			t.Errorf("run of %s names slices %v, want %s", run.Workspace, run.Slices, want[run.Workspace])
		}
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
