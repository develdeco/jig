package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// recordProbe journals a builder's probe of file:line for slice, with the blob
// the file has in the clone now, as `jig probe` would.
func (r *verifyRig) recordProbe(slice, file string, line int, outcome, mutation string) {
	r.t.Helper()
	blob := gitMust(r.t, r.fx.RepoDir, "hash-object", file)
	dr := verifydeliver.Draft{Probe: &verifydeliver.ResultProbe{
		Seam: "the clamp", File: file, Line: line, Mutation: mutation, Command: "go test ./alpha", Outcome: outcome,
	}}
	l := verifydeliver.BuilderLine(slice, "s-1", blob, time.Now().UTC().Format(time.RFC3339Nano), dr)
	if err := journal.Append(r.st, r.fx.Ticket, l); err != nil {
		r.t.Fatal(err)
	}
}

// addFixSlice adds a fix slice to the ticket, with an oracle that passes.
func (r *verifyRig) addFixSlice(id string) {
	r.t.Helper()
	err := r.st.AppendSlices(r.fx.Ticket, []store.Slice{{
		ID: id, Workspace: "alpha", Goal: "Fix it.", Oracle: "test", BlockedBy: []string{}, FromBrief: []string{}, FromGate: 1, Findings: []string{"r1-f1"},
	}})
	if err != nil {
		r.t.Fatal(err)
	}
	r.setOracle(id, "echo ran-"+id)
}

// probesBlock is the part of out from the probes block on, "" when none.
func probesBlock(out string) string {
	if i := strings.Index(out, "probes:\n"); i >= 0 {
		return out[i:]
	}
	return ""
}

// TestVerifyProbesShowsZerosForAFixSliceWithNone: a fix slice's run prints the
// block with 0 each when it has recorded no probe, and a build slice with none
// prints no block; neither changes what the run covers.
func TestVerifyProbesShowsZerosForAFixSliceWithNone(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	r.fixClamp()
	r.addFixSlice("fix-1")
	r.setOracle("a", "echo ran-a")

	out, code := r.run("verify", "--slice", "a")
	if code != 0 || !strings.Contains(out, "covers: yes") || probesBlock(out) != "" {
		t.Fatalf("a build slice with no probe: exit %d, want a covering pass and no probes block\n%s", code, out)
	}
	r.commit("fix-1", "fix it", map[string]string{"alpha/extra.go": "package alpha\n"})
	out, code = r.run("verify")
	want := "probes:\n  slice: fix-1\n  current: 0\n  stale: 0\n  survived: 0\n"
	if code != 0 || !strings.Contains(out, "covers: yes") || !strings.Contains(probesBlock(out), want) || strings.Contains(out, "probe_rows") {
		t.Fatalf("a fix slice with no probe: exit %d, want zeros and a covering pass\n%s", code, out)
	}
}

// TestVerifyProbesCountsAndListsStaleAndSurvivedProbes: a stale probe and a
// current survivor are counted and listed with file:line, outcome and
// mutation, a current caught one counts current only, and covers stays yes
// through all of them.
func TestVerifyProbesCountsAndListsStaleAndSurvivedProbes(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	r.setOracle("a", "echo ran-a")
	r.fixClamp()
	file := "alpha/alpha.go"
	r.recordProbe("a", file, 3, verifydeliver.ProbeCaught, "raise the bound")
	r.recordProbe("a", file, 5, verifydeliver.ProbeSurvived, "negate the test")

	out, code := r.run("verify")
	block := probesBlock(out)
	if code != 0 || !strings.Contains(out, "covers: yes") || !strings.Contains(block, "current: 2\n  stale: 0\n  survived: 1\n") {
		t.Fatalf("a caught and a surviving probe: exit %d, want 2 current, 0 stale, 1 survived and covers yes\n%s", code, out)
	}
	if !strings.Contains(block, "probe_rows[1]{file:line,outcome,state,mutation}:\n  \"alpha/alpha.go:5\",survived,current,negate the test") {
		t.Errorf("probes block lacks the survivor's row:\n%s", block)
	}

	// An edit to the file makes both probes stale; the run still covers.
	path := filepath.Join(r.fx.RepoDir, "alpha", "alpha.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r.commit("a", "edit after the probes", map[string]string{file: string(data) + "\n// edited\n"})
	out, code = r.run("verify")
	block = probesBlock(out)
	if code != 0 || !strings.Contains(out, "covers: yes") || !strings.Contains(block, "current: 0\n  stale: 2\n  survived: 0\n") || !strings.Contains(block, "probe_rows[2]") {
		t.Fatalf("after an edit: exit %d, want both probes stale, none survived, covers yes\n%s", code, out)
	}
	for _, want := range []string{"\"alpha/alpha.go:3\",caught,stale,raise the bound", "\"alpha/alpha.go:5\",survived,stale,negate the test"} {
		if !strings.Contains(block, want) {
			t.Errorf("probes block lacks the row %q:\n%s", want, block)
		}
	}

	// A later probe of a line, a re-probe after the edit, supersedes the stale one.
	r.recordProbe("a", file, 5, verifydeliver.ProbeCaught, "negate the test")
	out, _ = r.run("verify", "--no-record")
	if block = probesBlock(out); !strings.Contains(block, "current: 1\n  stale: 2\n  survived: 0\n") {
		t.Errorf("after a re-probe of line 5: want 1 current, 2 stale\n%s", block)
	}
}
